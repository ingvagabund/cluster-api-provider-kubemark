package main

import (
	"fmt"
	"strings"
	"time"

	"context"

	"github.com/golang/glog"
	machinev1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	kappsapi "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	kpolicyapi "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	workerRoleLabel = "node-role.kubernetes.io/worker"
)

func verifyControllersAreReady() error {
	name := "clusterapi-manager-controllers"
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	d := &kappsapi.Deployment{}

	err := wait.PollImmediate(1*time.Second, 1*time.Minute, func() (bool, error) {
		if err := F.Client.Get(context.TODO(), key, d); err != nil {
			glog.Errorf("error querying api for Deployment object: %v, retrying...", err)
			return false, nil
		}
		if d.Status.ReadyReplicas < 1 {
			return false, nil
		}
		return true, nil
	})
	return err
}

var nodeDrainLabels = map[string]string{
	workerRoleLabel:      "",
	"node-draining-test": "",
}

func machineFromMachineset(machineset *machinev1beta1.MachineSet) *machinev1beta1.Machine {
	randomUUID := string(uuid.NewUUID())

	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineset.Namespace,
			Name:      "machine-" + randomUUID[:6],
			Labels:    machineset.Labels,
		},
		Spec: machineset.Spec.Template.Spec,
	}
	if machine.Spec.ObjectMeta.Labels == nil {
		machine.Spec.ObjectMeta.Labels = map[string]string{}
	}
	for key := range nodeDrainLabels {
		if _, exists := machine.Spec.ObjectMeta.Labels[key]; exists {
			continue
		}
		machine.Spec.ObjectMeta.Labels[key] = nodeDrainLabels[key]
	}
	return machine
}

func replicationControllerWorkload(namespace string) *v1.ReplicationController {
	var replicas int32 = 20
	return &v1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pdb-workload",
			Namespace: namespace,
		},
		Spec: v1.ReplicationControllerSpec{
			Replicas: &replicas,
			Selector: map[string]string{
				"app": "nginx",
			},
			Template: &v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nginx",
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:    "work",
							Image:   "busybox",
							Command: []string{"sleep", "10h"},
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									"cpu":    resource.MustParse("50m"),
									"memory": resource.MustParse("50Mi"),
								},
							},
						},
					},
					NodeSelector: nodeDrainLabels,
					Tolerations: []v1.Toleration{
						{
							Key:      "kubemark",
							Operator: v1.TolerationOpExists,
						},
					},
				},
			},
		},
	}
}

func podDisruptionBudget(namespace string) *kpolicyapi.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt(1)
	return &kpolicyapi.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-pdb",
			Namespace: namespace,
		},
		Spec: kpolicyapi.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			MaxUnavailable: &maxUnavailable,
		},
	}
}

func ExpectControllersAreReady() error {
	return verifyControllersAreReady()
}

// 1. create two machines (without machineset) and wait until nodes are registered and ready
// 1. create rc
// 1. create pdb
// 1. pick a node that has at least half of the rc pods
// 1. drain node
// 1. observe the machine object is not deleted before a node is drained,
//    i.e. as long as there is at least one pod running on the drained node,
//    the machine object can not be deleted

func isNodeReady(node *v1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}

func ExpectNodeToBeDrainedBeforeMachineIsDeleted() error {
	delObjects := make(map[string]runtime.Object)

	defer func() {
		// Remove resources
		for key := range delObjects {
			glog.Infof("Deleting object %q", key)
			if err := F.Client.Delete(context.TODO(), delObjects[key]); err != nil {
				glog.Errorf("unable to delete object %q: %v", key, err)
			}
		}
	}()

	// Take the first worker machineset (assuming only worker machines are backed by machinesets)
	machinesets := machinev1beta1.MachineSetList{}
	if err := wait.PollImmediate(1*time.Second, 1*time.Minute, func() (bool, error) {
		if err := F.Client.List(context.TODO(), &client.ListOptions{}, &machinesets); err != nil {
			glog.Errorf("error querying api for machineset object: %v, retrying...", err)
			return false, nil
		}
		if len(machinesets.Items) < 1 {
			glog.Errorf("Expected at least one machineset, have none")
			return false, nil
		}
		return true, nil
	}); err != nil {
		return err
	}

	// Create two machines
	machine1 := machineFromMachineset(&machinesets.Items[0])
	machine1.Name = "machine1"

	if err := F.Client.Create(context.TODO(), machine1); err != nil {
		return fmt.Errorf("unable to create machine %q: %v", machine1.Name, err)
	}

	delObjects["machine1"] = machine1

	machine2 := machineFromMachineset(&machinesets.Items[0])
	machine2.Name = "machine2"

	if err := F.Client.Create(context.TODO(), machine2); err != nil {
		return fmt.Errorf("unable to create machine %q: %v", machine2.Name, err)
	}

	delObjects["machine2"] = machine2

	// Wait until both new nodes are ready
	if err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		nodes := v1.NodeList{}
		listOpt := &client.ListOptions{}
		listOpt.MatchingLabels(nodeDrainLabels)
		if err := F.Client.List(context.TODO(), listOpt, &nodes); err != nil {
			glog.Errorf("error querying api for Node object: %v, retrying...", err)
			return false, nil
		}
		// expecting nodeGroupSize nodes
		nodeCounter := 0
		for _, node := range nodes.Items {
			if _, exists := node.Labels[workerRoleLabel]; !exists {
				continue
			}

			if !isNodeReady(&node) {
				continue
			}

			nodeCounter++
		}

		if nodeCounter < 2 {
			glog.Errorf("Expecting 2 nodes with %#v labels in Ready state, got %v", nodeDrainLabels, nodeCounter)
			return false, nil
		}

		glog.Infof("Expected number (2) of nodes with %v label in Ready state found", nodeDrainLabels)
		return true, nil
	}); err != nil {
		return err
	}

	rc := replicationControllerWorkload(namespace)
	if err := F.Client.Create(context.TODO(), rc); err != nil {
		return fmt.Errorf("unable to create RC %q: %v", rc.Name, err)
	}

	delObjects["rc"] = rc

	pdb := podDisruptionBudget(namespace)
	if err := F.Client.Create(context.TODO(), pdb); err != nil {
		return fmt.Errorf("unable to create PDB %q: %v", pdb.Name, err)
	}

	delObjects["pdb"] = pdb

	// Wait until all replicas are ready
	if err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		rcObj := v1.ReplicationController{}
		key := types.NamespacedName{
			Namespace: rc.Namespace,
			Name:      rc.Name,
		}
		if err := F.Client.Get(context.TODO(), key, &rcObj); err != nil {
			glog.Errorf("error querying api RC %q object: %v, retrying...", rc.Name, err)
			return false, nil
		}
		if rcObj.Status.ReadyReplicas == 0 {
			glog.Infof("Waiting for at least one RC ready replica (%v/%v)", rcObj.Status.ReadyReplicas, rcObj.Status.Replicas)
			return false, nil
		}
		glog.Infof("Waiting for RC ready replicas (%v/%v)", rcObj.Status.ReadyReplicas, rcObj.Status.Replicas)
		if rcObj.Status.Replicas != rcObj.Status.ReadyReplicas {
			return false, nil
		}
		return true, nil
	}); err != nil {
		return err
	}

	// All pods are distrubution evenly among all nodes so it's fine to drain
	// random node and observe reconciliation of pods on the other one.
	if err := F.Client.Delete(context.TODO(), machine1); err != nil {
		return fmt.Errorf("unable to delete machine %q: %v", machine1.Name, err)
	}

	delete(delObjects, "machine1")

	// We still should be able to list the machine as until rc.replicas-1 are running on the other node
	var drainedNodeName string
	if err := wait.PollImmediate(1*time.Second, 10*time.Minute, func() (bool, error) {
		machine := machinev1beta1.Machine{}

		key := types.NamespacedName{
			Namespace: machine1.Namespace,
			Name:      machine1.Name,
		}
		if err := F.Client.Get(context.TODO(), key, &machine); err != nil {
			glog.Errorf("error querying api machine %q object: %v, retrying...", machine1.Name, err)
			return false, nil
		}
		if machine.Status.NodeRef == nil || machine.Status.NodeRef.Kind != "Node" {
			glog.Error("machine %q not linked to a node", machine.Name)
		}

		drainedNodeName = machine.Status.NodeRef.Name

		pods := v1.PodList{}
		listOpt := &client.ListOptions{}
		listOpt.MatchingLabels(rc.Spec.Selector)
		if err := F.Client.List(context.TODO(), listOpt, &pods); err != nil {
			glog.Errorf("error querying api for Pods object: %v, retrying...", err)
			return false, nil
		}

		// expecting nodeGroupSize nodes
		podCounter := 0
		for _, pod := range pods.Items {
			if pod.Spec.NodeName != machine.Status.NodeRef.Name {
				continue
			}
			if !pod.DeletionTimestamp.IsZero() {
				continue
			}
			podCounter++
		}

		glog.Infof("Have %v pods scheduled to node %q", podCounter, machine.Status.NodeRef.Name)

		// Verify we have enough pods running as well
		rcObj := v1.ReplicationController{}
		key = types.NamespacedName{
			Namespace: rc.Namespace,
			Name:      rc.Name,
		}
		if err := F.Client.Get(context.TODO(), key, &rcObj); err != nil {
			glog.Errorf("error querying api RC %q object: %v, retrying...", rc.Name, err)
			return false, nil
		}

		// The point of the test is to make sure majority of the pods is rescheduled
		// to other nodes. Pod disruption budget makes sure at most one pod
		// owned by the RC is not Ready. So no need to test it. Though, usefull to have it printed.
		glog.Infof("RC ReadyReplicas/Replicas: %v/%v", rcObj.Status.ReadyReplicas, rcObj.Status.Replicas)

		// This makes sure at most one replica is not ready
		if rcObj.Status.Replicas-rcObj.Status.ReadyReplicas > 1 {
			return false, fmt.Errorf("pod disruption budget not respecpted, node was not properly drained")
		}

		// Depends on timing though a machine can be deleted even before there is only
		// one pod left on the node (that is being evicted).
		if podCounter > 2 {
			glog.Infof("Expecting at most 2 pods to be scheduled to drained node %v, got %v", machine.Status.NodeRef.Name, podCounter)
			return false, nil
		}

		glog.Info("Expected result: all pods from the RC up to last one or two got scheduled to a different node while respecting PDB")
		return true, nil
	}); err != nil {
		return err
	}

	// Validate the machine is deleted
	if err := wait.PollImmediate(5*time.Second, 1*time.Minute, func() (bool, error) {
		machine := machinev1beta1.Machine{}

		key := types.NamespacedName{
			Namespace: machine1.Namespace,
			Name:      machine1.Name,
		}
		err := F.Client.Get(context.TODO(), key, &machine)
		if err == nil {
			glog.Errorf("Machine %q not yet deleted", machine1.Name)
			return false, nil
		}

		if !strings.Contains(err.Error(), "not found") {
			glog.Errorf("error querying api machine %q object: %v, retrying...", machine1.Name, err)
			return false, nil
		}

		glog.Infof("Machine %q successfully deleted", machine1.Name)
		return true, nil
	}); err != nil {
		return err
	}

	// Validate underlying node is removed as well
	if err := wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		node := v1.Node{}

		key := types.NamespacedName{
			Name: drainedNodeName,
		}
		err := F.Client.Get(context.TODO(), key, &node)
		if err == nil {
			glog.Errorf("Node %q not yet deleted", drainedNodeName)
			return false, nil
		}

		if !strings.Contains(err.Error(), "not found") {
			glog.Errorf("error querying api node %q object: %v, retrying...", drainedNodeName, err)
			return false, nil
		}

		glog.Infof("Node %q successfully deleted", drainedNodeName)
		return true, nil
	}); err != nil {
		return err
	}

	return nil
}
