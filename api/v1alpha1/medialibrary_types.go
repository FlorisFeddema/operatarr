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

// MediaLibrarySpec defines the desired state of MediaLibrary
type MediaLibrarySpec struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	// +kubebuilder:default={"ReadWriteMany"}
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// +kubebuilder:validation:Optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	CreatePV bool `json:"createPv"`

	// +kubebuilder:validation:Optional
	PVSpec *PVSpec `json:"pvSpec,omitempty"`
}

type PVSpec struct {
	// +kubebuilder:validation:Enum=Retain;Recycle;Delete
	// +kubebuilder:default=Retain
	PersistentVolumeReclaimPolicy corev1.PersistentVolumeReclaimPolicy `json:"reclaimPolicy,omitempty"`

	// +kubebuilder:validation:Enum=Filesystem;Block
	// +kubebuilder:default=Filesystem
	VolumeMode *corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`

	// +kubebuilder:validation:Required
	CSI *corev1.CSIPersistentVolumeSource `json:"csi,omitempty"`
}

// MediaLibraryStatus defines the observed state of MediaLibrary
type MediaLibraryStatus struct {
	// +kubebuilder:subresource:status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ml
// +kubebuilder:printcolumn:name="Size",type="string",JSONPath=".spec.size",description="Size of the MediaLibrary"
// +kubebuilder:resource:scope=Namespaced

// MediaLibrary is the Schema for the medialibraries API
type MediaLibrary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MediaLibrarySpec   `json:"spec,omitempty"`
	Status MediaLibraryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MediaLibraryList contains a list of MediaLibrary
type MediaLibraryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MediaLibrary `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MediaLibrary{}, &MediaLibraryList{})
}
