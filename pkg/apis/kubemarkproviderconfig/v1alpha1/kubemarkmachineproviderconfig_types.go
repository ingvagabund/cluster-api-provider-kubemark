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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Annotation constants
const (
	// ClusterIDLabel is the label that a machineset must have to identify the
	// cluster to which it belongs.
	ClusterIDLabel   = "sigs.k8s.io/cluster-api-cluster"
	MachineRoleLabel = "sigs.k8s.io/cluster-api-machine-role"
	MachineTypeLabel = "sigs.k8s.io/cluster-api-machine-type"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// KubemarkMachineProviderStatus is the type that will be embedded in a Machine.Status.ProviderStatus field.
// It contains Kubemark-specific status information.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type KubemarkMachineProviderStatus struct {
	metav1.TypeMeta `json:",inline"`

	// InstanceID is the instance ID of the machine created in Kubemark
	// +optional
	InstanceID *string `json:"instanceId,omitempty"`

	// InstanceState is the state of the Kubemark instance for this machine
	// +optional
	InstanceState *string `json:"instanceState,omitempty"`

	// Conditions is a set of conditions associated with the Machine to indicate
	// errors or other status
	Conditions []KubemarkMachineProviderCondition `json:"conditions,omitempty"`
}

// KubemarkMachineProviderConditionType is a valid value for KubemarkMachineProviderCondition.Type
type KubemarkMachineProviderConditionType string

// Valid conditions for an Kubemark machine instance
const (
	// MachineCreation indicates whether the machine has been created or not. If not,
	// it should include a reason and message for the failure.
	MachineCreation KubemarkMachineProviderConditionType = "MachineCreation"
)

// KubemarkMachineProviderCondition is a condition in a KubemarkMachineProviderStatus
type KubemarkMachineProviderCondition struct {
	// Type is the type of the condition.
	Type KubemarkMachineProviderConditionType `json:"type"`
	// Status is the status of the condition.
	Status corev1.ConditionStatus `json:"status"`
	// LastProbeTime is the last time we probed the condition.
	// +optional
	LastProbeTime metav1.Time `json:"lastProbeTime,omitempty"`
	// LastTransitionTime is the last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// Reason is a unique, one-word, CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Message is a human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubemarkMachineProviderConfig is the Schema for the kubemarkmachineproviderconfigs API
// +k8s:openapi-gen=true
type KubemarkMachineProviderConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// TurnUnhealthyAfter configures kubemark node to go Unready
	TurnUnhealthyAfter bool `json:"turnUnhealthyAfter"`

	// UnhealthyDuration specifies for how long kubemark node stays in Unready state
	UnhealthyDuration metav1.Duration `json:"unhealthyDuration"`

	// HealthyDuration specifies for how long kubemark node stays in Ready state
	HealthyDuration metav1.Duration `json:"healthyDuration"`

	// TurnUnhealthyPeriodically configures kubemark node to go unready and ready
	// periodically indefinitely
	TurnUnhealthyPeriodically bool `json:"turnUnhealthyPeriodically"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KubemarkMachineProviderConfigList contains a list of KubemarkMachineProviderConfig
type KubemarkMachineProviderConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KubemarkMachineProviderConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubemarkMachineProviderConfig{}, &KubemarkMachineProviderConfigList{}, &KubemarkMachineProviderStatus{})
}
