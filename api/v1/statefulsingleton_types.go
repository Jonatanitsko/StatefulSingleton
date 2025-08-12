/*
Copyright 2025.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatefulSingletonSpec defines the desired state of StatefulSingleton
type StatefulSingletonSpec struct {
	// Selector identifies the pods managed by this StatefulSingleton
	// +required
	Selector metav1.LabelSelector `json:"selector"`

	// MaxTransitionTime is the maximum time in seconds to wait for a transition
	// +optional
	// +kubebuilder:default:=300
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	MaxTransitionTime int `json:"maxTransitionTime,omitempty"`

	// TerminationGracePeriod is the grace period in seconds for pod termination
	// +optional
	// +kubebuilder:default:=30
	TerminationGracePeriod int `json:"terminationGracePeriod,omitempty"`

	// RespectPodGracePeriod determines whether to use the pod's own grace period if it's longer
	// +optional
	// +kubebuilder:default:=true
	RespectPodGracePeriod bool `json:"respectPodGracePeriod,omitempty"`
}

// StatefulSingletonStatus defines the observed state of StatefulSingleton
type StatefulSingletonStatus struct {
	// ActivePod is the name of the currently active pod
	// +optional
	ActivePod string `json:"activePod,omitempty"`

	// Phase represents the current state of the StatefulSingleton
	// +optional
	// +kubebuilder:validation:Enum=Running;Transitioning;Failed
	Phase string `json:"phase,omitempty"`

	// TransitionTimestamp is when the last pod-to-pod transition started (only set during Transitioning phase)
	// +optional
	TransitionTimestamp *metav1.Time `json:"transitionTimestamp,omitempty"`

	// StatusTimestamp is when the status was last updated
	// +optional
	StatusTimestamp *metav1.Time `json:"statusTimestamp,omitempty"`

	// Message provides more information about the current phase
	// +optional
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Active Pod",type="string",JSONPath=".status.activePod"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// StatefulSingleton is a resource that manages controlled transitions for stateful singleton applications
type StatefulSingleton struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StatefulSingletonSpec   `json:"spec,omitempty"`
	Status StatefulSingletonStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// StatefulSingletonList contains a list of StatefulSingleton resources
type StatefulSingletonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StatefulSingleton `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StatefulSingleton{}, &StatefulSingletonList{})
}
