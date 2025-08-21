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

// SonarrSpec defines the desired state of Sonarr
type SonarrSpec struct {
	PodSpec SonarrPodTemplateSpec `json:"sonarrPodTemplateSpec"`
}

type ConfigVolumeSpec struct {
	AccessModes      []corev1.PersistentVolumeAccessMode `json:"accessModes"`
	Size             resource.Quantity                   `json:"resources"`
	StorageClassName *string                             `json:"StorageClassName,omitempty"`
}

type SonarrPodTemplateSpec struct {
	Image            string                        `json:"image"`
	ImagePullPolicy  corev1.PullPolicy             `json:"imagePullPolicy,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	NodeName     string              `json:"nodeName,omitempty"`
	Affinity     *corev1.Affinity    `json:"affinity,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`

	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	SecurityContext          *corev1.PodSecurityContext `json:"securityContext"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"ContainerSecurityContext"`

	ConfigVolumeSpec *ConfigVolumeSpec       `json:"configVolumeSpec"`
	MediaLibraryRef  *corev1.ObjectReference `json:"mediaLibraryRef,omitempty"`
}

// SonarrStatus defines the observed state of Sonarr
type SonarrStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Conditions represent the latest available observations of an object's state
	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Sonarr is the Schema for the sonarrs API
type Sonarr struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SonarrSpec   `json:"spec,omitempty"`
	Status SonarrStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SonarrList contains a list of Sonarr
type SonarrList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Sonarr `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Sonarr{}, &SonarrList{})
}
