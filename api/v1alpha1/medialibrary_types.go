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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MediaLibrarySpec defines the desired state of MediaLibrary
type MediaLibrarySpec struct {
	// CrossNamespaceAccess defines whether the MediaLibrary should be accessible from other namespaces.
	// +kubebuilder:default:=true
	CrossNamespaceAccess bool `json:"crossNamespaceAccess,omitempty"`

	// PVC defines the PersistentVolumeClaim to use for the MediaLibrary.
	// +kubebuilder:validation:Required
	PVC MediaLibraryPVC `json:"pvc"`

	// Permissions defines POSIX-like permissions to set on the MediaLibrary.
	// +kubebuilder:validation:Optional
	Permissions *MediaPermissions `json:"permissions,omitempty"`
}

// MediaLibraryPVC defines the PersistentVolumeClaim to use for the MediaLibrary.
type MediaLibraryPVC struct {
	// PVCName is name of a pre-existing PersistentVolumeClaim to use for the MediaLibrary.
	// +kubebuilder:validation:Optional
	PVCName *string `json:"pvcName,omitempty"`

	// Size defines the size of the PersistentVolumeClaim to create for the MediaLibrary.
	// +kubebuilder:validation:Optional
	Size resource.Quantity `json:"size"`

	// StorageClassName is the name of the StorageClass to use for the PersistentVolumeClaim.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Annotations to add to the PersistentVolumeClaim.
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`

	//+kubebuilder:validation:XValidation:rule="has(self.pvcName) or has(self.resources)",message="Either pvcName or resources must be set"
	//+kubebuilder:validation:XValidation:rule="!(has(self.pvcName) and (has(self.resources) or has(self.storageClassName) or has(self.annotations))",message="Only pvcName or resources/storageClassName/annotations can be set, not both"
}

// MediaPermissions defines POSIX-like permissions to set on the MediaLibrary.
type MediaPermissions struct {
	// RunAsUser is the user ID to run the MediaLibrary as.
	// +kubebuilder:default:=5000
	RunAsUser *int64 `json:"runAsUser,omitempty"`

	// RunAsGroup is the group ID to run the MediaLibrary as.
	// +kubebuilder:default:=5000
	RunAsGroup *int64 `json:"runAsGroup,omitempty"`

	// FSGroup is the group ID to set on the MediaLibrary files.
	// +kubebuilder:default:=5000
	FSGroup *int64 `json:"fsGroup,omitempty"`

	// SupplementalGroups is the supplemental group IDs to set on the MediaLibrary files.
	// +kubebuilder:validation:Optional
	SupplementalGroups *int64 `json:"supplementalGroups,omitempty"`
}

// MediaLibraryStatus defines the observed state of MediaLibrary
type MediaLibraryStatus struct {
	// Conditions represent the latest available observations of an object's state
	// +kubebuilder:subresource:status
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// EffectivePVC is the name of the PersistentVolumeClaim used for this MediaLibrary.
	EffectivePVC *string `json:"effectivePVC,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ml
// +kubebuilder:printcolumn:name="PVC",type=string,JSONPath=`.status.actualPVCName`,description="The name of the PersistentVolumeClaim"
// +kubebuilder:printcolumn:name="Size",type=date,JSONPath=`.status.resources.requests.storage`,description="The size of the MediaLibrary"
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
