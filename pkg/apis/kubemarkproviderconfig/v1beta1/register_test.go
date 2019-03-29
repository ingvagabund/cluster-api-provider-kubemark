package v1beta1

import (
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestEncodeAndDecodeProviderStatus(t *testing.T) {

	codec, err := NewCodec()
	if err != nil {
		t.Fatal(err)
	}
	time := metav1.Time{
		Time: time.Date(2018, 6, 3, 0, 0, 0, 0, time.Local),
	}

	instanceState := "running"
	instanceID := "id"
	providerStatus := &KubemarkMachineProviderStatus{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubemarkMachineProviderStatus",
			APIVersion: "kubemarkproviderconfig.k8s.io/v1alpha1",
		},
		InstanceState: &instanceState,
		InstanceID:    &instanceID,
		Conditions: []KubemarkMachineProviderCondition{
			{
				Type:               "MachineCreation",
				Status:             "True",
				Reason:             "MachineCreationSucceeded",
				Message:            "machine successfully created",
				LastTransitionTime: time,
				LastProbeTime:      time,
			},
		},
	}
	providerStatusEncoded, err := codec.EncodeProviderStatus(providerStatus)
	if err != nil {
		t.Error(err)
	}

	// without deep copy
	{
		providerStatusDecoded := &KubemarkMachineProviderStatus{}
		codec.DecodeProviderStatus(providerStatusEncoded, providerStatusDecoded)
		if !reflect.DeepEqual(providerStatus, providerStatusDecoded) {
			t.Errorf("failed EncodeProviderStatus/DecodeProviderStatus. Expected: %+v, got: %+v", providerStatus, providerStatusDecoded)
		}
	}

	// with deep copy
	{
		providerStatusDecoded := &KubemarkMachineProviderStatus{}
		codec.DecodeProviderStatus(providerStatusEncoded.DeepCopy(), providerStatusDecoded)
		if !reflect.DeepEqual(providerStatus, providerStatusDecoded) {
			t.Errorf("failed EncodeProviderStatus/DecodeProviderStatus. Expected: %+v, got: %+v", providerStatus, providerStatusDecoded)
		}
	}
}

func TestEncodeAndDecodeProviderSpec(t *testing.T) {
	codec, err := NewCodec()
	if err != nil {
		t.Fatal(err)
	}

	providerConfig := &KubemarkMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubemarkMachineProviderConfig",
			APIVersion: "kubemarkproviderconfig.k8s.io/v1alpha1",
		},
		TurnUnhealthyAfter:        false,
		UnhealthyDuration:         metav1.Duration{UnhealthyDuration: 5 * time.Second},
		HealthyDuration:           metav1.Duration{HealthyDuration: 5 * time.Second},
		TurnUnhealthyPeriodically: true,
	}

	providerConfigEncoded, err := codec.EncodeProviderSpec(providerConfig)
	if err != nil {
		t.Fatal(err)
	}

	// Without deep copy
	{
		providerConfigDecoded := &KubemarkMachineProviderConfig{}
		codec.DecodeProviderSpec(providerConfigEncoded, providerConfigDecoded)

		if !reflect.DeepEqual(providerConfig, providerConfigDecoded) {
			t.Errorf("failed EncodeProviderSpec/DecodeProviderSpec. Expected: %+v, got: %+v", providerConfig, providerConfigDecoded)
		}
	}

	// With deep copy
	{
		providerConfigDecoded := &KubemarkMachineProviderConfig{}
		codec.DecodeProviderSpec(providerConfigEncoded, providerConfigDecoded)

		if !reflect.DeepEqual(providerConfig.DeepCopy(), providerConfigDecoded) {
			t.Errorf("failed EncodeProviderSpec/DecodeProviderSpec. Expected: %+v, got: %+v", providerConfig, providerConfigDecoded)
		}
	}
}
