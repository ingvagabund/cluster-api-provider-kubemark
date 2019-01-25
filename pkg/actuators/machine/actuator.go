/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-log/log/info"
	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	providerconfigv1 "github.com/openshift/cluster-api-provider-kubemark/pkg/apis/kubemarkproviderconfig/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	clustererror "sigs.k8s.io/cluster-api/pkg/controller/error"
	apierrors "sigs.k8s.io/cluster-api/pkg/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	kubedrain "github.com/openshift/kubernetes-drain"
)

const (
	requeueAfterSeconds = 20

	// MachineCreationSucceeded indicates success for machine creation
	MachineCreationSucceeded = "MachineCreationSucceeded"

	// MachineCreationFailed indicates that machine creation failed
	MachineCreationFailed = "MachineCreationFailed"
)

// Actuator is the AWS-specific actuator for the Cluster API machine controller
type Actuator struct {
	client client.Client
	config *rest.Config

	codec         *providerconfigv1.KubemarkProviderConfigCodec
	eventRecorder record.EventRecorder
}

// ActuatorParams holds parameter information for Actuator
type ActuatorParams struct {
	Client        client.Client
	Config        *rest.Config
	Codec         *providerconfigv1.KubemarkProviderConfigCodec
	EventRecorder record.EventRecorder
}

// NewActuator returns a new AWS Actuator
func NewActuator(params ActuatorParams) (*Actuator, error) {
	actuator := &Actuator{
		client:        params.Client,
		config:        params.Config,
		codec:         params.Codec,
		eventRecorder: params.EventRecorder,
	}
	return actuator, nil
}

const (
	createEventAction = "Create"
	deleteEventAction = "Delete"
	noEventAction     = ""
)

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *clusterv1.Machine, err *apierrors.MachineError, eventAction string) error {
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v", err.Reason)
	}

	glog.Errorf("Machine error: %v", err.Message)
	return err
}

// Create runs a new kubemark instance
func (a *Actuator) Create(context context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	glog.Info("creating machine")
	instance, err := a.CreateMachine(cluster, machine)
	if err != nil {
		glog.Errorf("error creating machine: %v", err)
		updateConditionError := a.updateMachineProviderConditions(machine, providerconfigv1.MachineCreation, MachineCreationFailed, err.Error())
		if updateConditionError != nil {
			glog.Errorf("error updating machine conditions: %v", updateConditionError)
		}
		return err
	}
	return a.updateStatus(machine, instance)
}

func (a *Actuator) updateMachineStatus(machine *clusterv1.Machine, kubemarkStatus *providerconfigv1.KubemarkMachineProviderStatus, networkAddresses []corev1.NodeAddress) error {
	kubemarkStatusRaw, err := a.codec.EncodeProviderStatus(kubemarkStatus)
	if err != nil {
		glog.Errorf("error encoding Kubemark provider status: %v", err)
		return err
	}

	machineCopy := machine.DeepCopy()
	machineCopy.Status.ProviderStatus = kubemarkStatusRaw
	if networkAddresses != nil {
		machineCopy.Status.Addresses = networkAddresses
	}

	oldKubemarkStatus := &providerconfigv1.KubemarkMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, oldKubemarkStatus); err != nil {
		glog.Errorf("error updating machine status: %v", err)
		return err
	}

	// TODO(vikasc): Revisit to compare complete machine status objects
	if !equality.Semantic.DeepEqual(kubemarkStatus, oldKubemarkStatus) || !equality.Semantic.DeepEqual(machine.Status.Addresses, machineCopy.Status.Addresses) {
		glog.Infof("machine status has changed, updating")
		time := metav1.Now()
		machineCopy.Status.LastUpdated = &time

		if err := a.client.Status().Update(context.Background(), machineCopy); err != nil {
			glog.Errorf("error updating machine status: %v", err)
			return err
		}
	} else {
		glog.Info("status unchanged")
	}

	return nil
}

// updateMachineProviderConditions updates conditions set within machine provider status.
func (a *Actuator) updateMachineProviderConditions(machine *clusterv1.Machine, conditionType providerconfigv1.KubemarkMachineProviderConditionType, reason string, msg string) error {

	glog.Info("updating machine conditions")

	kubemarkStatus := &providerconfigv1.KubemarkMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, kubemarkStatus); err != nil {
		glog.Errorf("error decoding machine provider status: %v", err)
		return err
	}

	kubemarkStatus.Conditions = setMachineProviderCondition(kubemarkStatus.Conditions, conditionType, corev1.ConditionTrue, reason, msg, updateConditionIfReasonOrMessageChange)

	if err := a.updateMachineStatus(machine, kubemarkStatus, nil); err != nil {
		return err
	}

	return nil
}

// CreateMachine starts a new AWS instance as described by the cluster and machine resources
func (a *Actuator) CreateMachine(cluster *clusterv1.Cluster, machine *clusterv1.Machine) (*corev1.Pod, error) {
	machineProviderConfig, err := providerConfigFromMachine(a.client, machine, a.codec)
	if err != nil {
		return nil, a.handleMachineError(machine, apierrors.InvalidMachineConfiguration("error decoding MachineProviderConfig: %v", err), createEventAction)
	}

	// We explicitly do NOT want to remove stopped masters.
	if !isMaster(machine) {
		// TODO(jchaloup): remove broken pods (whatever that means)
	}

	var testFlags string
	if machineProviderConfig.TurnUnhealthyAfter {
		testFlags = fmt.Sprintf("--turn-unhealthy-after=true --healthy-duration=%v", machineProviderConfig.HealthyDuration.Duration)
	} else if machineProviderConfig.TurnUnhealthyPeriodically {
		testFlags = fmt.Sprintf("--turn-unhealthy-periodically=true --unhealthy-duration=%v --healthy-duration=%v", machineProviderConfig.UnhealthyDuration.Duration, machineProviderConfig.HealthyDuration.Duration)
	} else {
		// TODO(jchaloup): be more descriptive here
		return nil, fmt.Errorf("Testing configuration not recognized")
	}

	// TODO(jchaloup): generate unique kubeconfig for the hollow node (so we don't
	// have to remove NodeRestriction admission plagin)
	// sudo kubectl create secret generic "kubeconfig" --type=Opaque --from-literal=kubelet.kubeconfig="$(cat ~/.kube/kubelet.conf)"
	// Right know it's up to the infrastructure to provide valid kubeconfig.
	// Though, it could be generated for each kubemark node separately later.

	// Just create pod/RC deploying hollow node.
	// Preferring just pods since RC creates new pods which creates new nodes.
	// Thus, it's important to have the machine controller actually respin the pod
	// instead of the RC.
	podNamedPair := machine2pod(machine)
	privileged := true
	hollowNode := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: podNamedPair.Namespace,
			Name:      podNamedPair.Name,
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Name:    "init-inotify-limit",
					Image:   "busybox",
					Command: []string{"sysctl", "-w", "fs.inotify.max_user_instances=1000"},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "kubeconfig-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "kubeconfig",
						},
					},
				},
				{
					Name: "logs-volume",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/var/log",
						},
					},
				},
				{
					Name: "no-serviceaccount-access-to-real-master",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "hollow-kubelet",
					Image:           "docker.io/gofed/kubemark:v1.11.3-4",
					ImagePullPolicy: corev1.PullAlways,
					Ports: []corev1.ContainerPort{
						{ContainerPort: 4194},
						{ContainerPort: 10250},
						{ContainerPort: 10255},
					},
					Env: []corev1.EnvVar{
						{
							Name: "NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.name",
								},
							},
						},
					},
					Command: []string{"/bin/sh", "-c", fmt.Sprintf("/kubemark --morph=kubelet --name=$(NODE_NAME) %v --node-status-update-frequency=2s --no-schedule=true --kubeconfig=/kubeconfig/kubelet.kubeconfig --kube-api-content-type=application/vnd.kubernetes.protobuf --alsologtostderr 1>>/var/log/kubelet-$(NODE_NAME).log 2>&1", testFlags)},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kubeconfig-volume",
							MountPath: "/kubeconfig",
							ReadOnly:  true,
						},
						{
							Name:      "logs-volume",
							MountPath: "/var/log",
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"cpu":    resource.MustParse("40m"),
							"memory": resource.MustParse("100M"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &privileged,
					},
				},
				{
					Name:            "hollow-proxy",
					Image:           "docker.io/gofed/kubemark:v1.11.3-4",
					ImagePullPolicy: corev1.PullAlways,
					Env: []corev1.EnvVar{
						{
							Name: "NODE_NAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "metadata.name",
								},
							},
						},
					},
					Command: []string{"/bin/sh", "-c", "/kubemark --morph=proxy --name=$(NODE_NAME) --kubeconfig=/kubeconfig/kubelet.kubeconfig --kube-api-content-type=application/vnd.kubernetes.protobuf --alsologtostderr 1>>/var/log/kubelet-$(NODE_NAME).log 2>&1"},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "kubeconfig-volume",
							MountPath: "/kubeconfig",
							ReadOnly:  true,
						},
						{
							Name:      "logs-volume",
							MountPath: "/var/log",
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"cpu":    resource.MustParse("40m"),
							"memory": resource.MustParse("500Ki"),
						},
					},
				},
			},
		},
	}

	kubeClient, err := kubernetes.NewForConfig(a.config)
	if err != nil {
		return nil, fmt.Errorf("unable to build kube client: %v", err)
	}

	if _, err := kubeClient.CoreV1().Pods(hollowNode.Namespace).Create(hollowNode); err != nil {
		return nil, a.handleMachineError(machine, apierrors.CreateMachine("error launching machine pod: %v", err), createEventAction)
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Created Machine %v", machine.Name)
	return hollowNode, nil
}

// Delete deletes a machine and updates its finalizer
func (a *Actuator) Delete(context context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	glog.Info("deleting machine")
	if err := a.DeleteMachine(cluster, machine); err != nil {
		glog.Errorf("error deleting machine: %v", err)
		return err
	}
	return nil
}

type glogLogger struct{}

func (gl *glogLogger) Log(v ...interface{}) {
	glog.Info(v...)
}

func (gl *glogLogger) Logf(format string, v ...interface{}) {
	glog.Infof(format, v...)
}

// DeleteMachine deletes an AWS instance
func (a *Actuator) DeleteMachine(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	// Drain node before deleting
	if machine.ObjectMeta.Annotations["openshift.io/drain-node"] == "True" && machine.Status.NodeRef != nil {
		glog.Infof("Draining node before delete")
		if a.config == nil {
			err := fmt.Errorf("missing client config, unable to build kube client")
			glog.Error(err)
			return err
		}
		kubeClient, err := kubernetes.NewForConfig(a.config)
		if err != nil {
			return fmt.Errorf("unable to build kube client: %v", err)
		}
		node, err := kubeClient.CoreV1().Nodes().Get(machine.Status.NodeRef.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to get node %q: %v", machine.Status.NodeRef.Name, err)
		}

		if err := kubedrain.Drain(
			kubeClient,
			[]*corev1.Node{node},
			&kubedrain.DrainOptions{
				Force:              true,
				IgnoreDaemonsets:   true,
				DeleteLocalData:    true,
				GracePeriodSeconds: -1,
				Logger:             info.New(glog.V(0)),
			},
		); err != nil {
			// Machine still tries to terminate after drain failure
			glog.Warningf("drain failed for machine %q: %v", machine.Name, err)
		} else {
			glog.Infof("drain successful for machine %q", machine.Name)
			a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Node %q drained", node.Name)
		}
	}

	machinePod, err := a.getMachinePod(machine)
	if err != nil {
		// TODO(jchaloup): maybe requeue after?
		err := fmt.Errorf("unable to get machine pod for %#v: %v", machine2pod(machine), err)
		glog.Error(err)
		return err
	}

	if machinePod == nil {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %v", machine.Name)
		return nil
	}

	kubeClient, err := kubernetes.NewForConfig(a.config)
	if err != nil {
		return fmt.Errorf("unable to build kube client: %v", err)
	}

	// TODO(jchaloup): Wait until the pod object is really deleted from the cluster.
	// Thus, re-queue with error.
	// (pods may stay in terminated state for a while)
	if err := kubeClient.CoreV1().Pods(machinePod.Namespace).Delete(machinePod.Name, nil); err != nil {
		glog.Warning("Unable to delete machine pod %#v: %v", machine2pod(machine), err)
		return a.handleMachineError(machine, apierrors.DeleteMachine(err.Error()), noEventAction)
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %v", machine.Name)

	return nil
}

// Update attempts to sync machine state with an existing instance. Today this just updates status
// for details that may have changed. (IPs and hostnames) We do not currently support making any
// changes to actual machines in AWS. Instead these will be replaced via MachineDeployments.
func (a *Actuator) Update(context context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	glog.Info("updating machine")

	machinePod, err := a.getMachinePod(machine)
	if err != nil {
		glog.Error(err)
		return err
	}

	if machinePod == nil {
		glog.Warningf("attempted to update machine but no machine pod found")
		a.handleMachineError(machine, apierrors.CreateMachine("no machine pod found, reason unknown"), noEventAction)

		// Update status to clear out machine details.
		if err := a.updateStatus(machine, nil); err != nil {
			return err
		}

		glog.Errorf("attempted to update machine but no machine pods found")
		return fmt.Errorf("attempted to update machine but no machine pods found")
	}

	glog.Infof("found machine pod %#v for machine", machine2pod(machine))

	// We do not support making changes to pre-existing instances, just update status.
	return a.updateStatus(machine, machinePod)
}

// Exists determines if the given machine currently exists. For AWS we query for instances in
// running state, with a matching name tag, to determine a match.
func (a *Actuator) Exists(context context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine) (bool, error) {
	glog.Info("checking if machine exists")

	machinePod, err := a.getMachinePod(machine)
	if err != nil {
		glog.Error(err)
		return false, err
	}

	if machinePod == nil {
		return false, nil
	}

	// If more than one result was returned, it will be handled in Update.
	glog.Infof("machine pod exists as %#v", machine2pod(machine))
	return true, nil
}

func (a *Actuator) getMachinePod(machine *clusterv1.Machine) (*corev1.Pod, error) {
	machinePodNamePair := machine2pod(machine)
	kubeClient, err := kubernetes.NewForConfig(a.config)
	if err != nil {
		return nil, fmt.Errorf("unable to build kube client: %v", err)
	}

	machinePod, err := kubeClient.CoreV1().Pods(machinePodNamePair.Namespace).Get(machinePodNamePair.Name, metav1.GetOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to get machine pod for %#v: %v", machinePodNamePair, err)
	}

	return machinePod, nil
}

// updateStatus calculates the new machine status, checks if anything has changed, and updates if so.
func (a *Actuator) updateStatus(machine *clusterv1.Machine, instance *corev1.Pod) error {
	glog.Info("updating status")

	// Starting with a fresh status as we assume full control of it here.
	kubemarkStatus := &providerconfigv1.KubemarkMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, kubemarkStatus); err != nil {
		glog.Errorf("error decoding machine provider status: %v", err)
		return err
	}

	// Save this, we need to check if it changed later.
	networkAddresses := []corev1.NodeAddress{}

	// Instance may have existed but been deleted outside our control, clear it's status if so:
	if instance == nil {
		kubemarkStatus.InstanceID = nil
		kubemarkStatus.InstanceState = nil
	} else {
		instanceID := fmt.Sprintf("%v/%v", instance.Namespace, instance.Name)
		kubemarkStatus.InstanceID = &instanceID
		phase := string(instance.Status.Phase)
		kubemarkStatus.InstanceState = &phase
		networkAddresses = append(networkAddresses, corev1.NodeAddress{
			Type:    corev1.NodeInternalIP,
			Address: instance.Status.PodIP,
		})
	}
	glog.Info("finished calculating Kubemark status")

	kubemarkStatus.Conditions = setMachineProviderCondition(kubemarkStatus.Conditions, providerconfigv1.MachineCreation, corev1.ConditionTrue, MachineCreationSucceeded, "machine successfully created", updateConditionIfReasonOrMessageChange)
	if err := a.updateMachineStatus(machine, kubemarkStatus, networkAddresses); err != nil {
		return err
	}

	// If machine state is still pending, we will return an error to keep the controllers
	// attempting to update status until it hits a more permanent state. This will ensure
	// we get a public IP populated more quickly.
	if kubemarkStatus.InstanceState != nil && *kubemarkStatus.InstanceState == string(corev1.PodPending) {
		glog.Infof("instance state still pending, returning an error to requeue")
		return &clustererror.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
	}
	return nil
}

func getClusterID(machine *clusterv1.Machine) (string, bool) {
	clusterID, ok := machine.Labels[providerconfigv1.ClusterIDLabel]
	return clusterID, ok
}
