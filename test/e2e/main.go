package main

import (
	"flag"
	"strings"

	"k8s.io/client-go/kubernetes/scheme"

	"github.com/golang/glog"
	mapiv1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	caName = "default"
)

var focus string
var namespace string

func init() {
	flag.StringVar(&focus, "focus", "[k8s]", "If set, run only tests containing focus string. E.g. [k8s]")
	flag.StringVar(&namespace, "namespace", "default", "cluster-autoscaler-operator namespace")

	if err := mapiv1beta1.AddToScheme(scheme.Scheme); err != nil {
		glog.Fatal(err)
	}
}

var F *Framework

type Framework struct {
	Client client.Client
}

func newClient() error {
	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		return err
	}

	client, err := client.New(cfg, client.Options{})
	if err != nil {
		return err
	}

	F = &Framework{Client: client}

	return nil
}

func main() {
	flag.Parse()

	// if err := apis.AddToScheme(scheme.Scheme); err != nil {
	// 	glog.Fatal(err)
	// }

	if err := newClient(); err != nil {
		glog.Fatal(err)
	}

	if err := runSuite(); err != nil {
		glog.Fatal(err)
	}
}

func runSuite() error {

	expectations := []struct {
		expect func() error
		name   string
	}{
		{
			expect: ExpectControllersAreReady,
			name:   "[k8s] Expect cluster API controllers to be available",
		},
		{
			expect: ExpectNodeToBeDrainedBeforeMachineIsDeleted,
			name:   "[k8s] Expect node to be drained before machine is deleted",
		},
	}

	for _, tc := range expectations {
		if strings.HasPrefix(tc.name, focus) {
			if err := tc.expect(); err != nil {
				glog.Errorf("FAIL: %v: %v", tc.name, err)
				return err
			}
			glog.Infof("PASS: %v", tc.name)
		} else {
			glog.Infof("SKIPPING: %v", tc.name)
		}
	}

	return nil
}
