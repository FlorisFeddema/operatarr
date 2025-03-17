/*
Copyright 2025 Floris Feddema.

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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// LibrarySpec defines the desired state of Library
type LibrarySpec struct {
	AccessModes      []corev1.PersistentVolumeAccessMode `json:"accessModes"`
	Size             resource.Quantity                   `json:"size"`
	StorageClassName *string                             `json:"StorageClassName"`
}

// LibraryStatus defines the observed state of Library
type LibraryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Library is the Schema for the libraries API
type Library struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LibrarySpec   `json:"spec,omitempty"`
	Status LibraryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LibraryList contains a list of Library
type LibraryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Library `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Library{}, &LibraryList{})
}
