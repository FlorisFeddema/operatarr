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

package controller

import (
	"context"
	"errors"
	"fmt" //nolint:goimports

	controllerruntime "github.com/FlorisFeddema/operatarr/internal/controller/controller-runtime"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
)

// SonarrReconciler reconciles a Sonarr object
type SonarrReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Sonarr object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *SonarrReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	sonarr := &feddemadevv1alpha1.Sonarr{}

	if err := r.Get(ctx, req.NamespacedName, sonarr); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("sonarr instance not found. Ignoring since object must be deleted")
			return controllerruntime.Success()
		}

		log.Error(err, "unable to fetch sonarr")
		return controllerruntime.Fail(err)
	}

	if len(sonarr.Status.Conditions) == 0 {
		meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionUnknown, Reason: "Reconciliation", Message: "Starting reconciliation"})

		if err := r.Status().Update(ctx, sonarr); err != nil {
			log.Error(err, "unable to update Sonarr status")
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Success()
	}

	if !controllerutil.ContainsFinalizer(sonarr, finalizerName) {
		log.Info("adding finalizer to sonarr")
		if ok := controllerutil.AddFinalizer(sonarr, finalizerName); !ok {
			return controllerruntime.Fail(errors.New("unable to add finalizer to sonarr"))
		}
		if err := r.Update(ctx, sonarr); err != nil {
			log.Error(err, "failed to update sonarr with finalizer")
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Success()
	}

	if sonarr.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(sonarr, finalizerName) {
			log.Info("performing finalization for sonarr before deletion")
			meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionUnknown, Reason: "Finalizing", Message: fmt.Sprintf("Finalizing %s before deletion", sonarr.Name)})

			if err := r.Status().Update(ctx, sonarr); err != nil {
				log.Error(err, "failed to update sonarr status")
				return controllerruntime.Fail(err)
			}

			// TODO: perform finalization

			if err := r.Get(ctx, req.NamespacedName, sonarr); err != nil {
				log.Error(err, "failed to re-fetch sonarr")
				return controllerruntime.Fail(err)
			}

			meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionTrue, Reason: "Finalizing", Message: fmt.Sprintf("Finalized %s before deletion", sonarr.Name)})

			if err := r.Status().Update(ctx, sonarr); err != nil {
				log.Error(err, "failed to update sonarr status")
				return controllerruntime.Fail(err)
			}

			log.Info("removing finalizer from sonarr")
			if ok := controllerutil.RemoveFinalizer(sonarr, finalizerName); !ok {
				log.Info("failed to remove finalizer from sonarr")
				return controllerruntime.Success()
			}

			if err := r.Update(ctx, sonarr); err != nil {
				log.Error(err, "failed to remove finalizer from sonarr")
				return controllerruntime.Fail(err)
			}
		}
		return controllerruntime.Success()
	}

	if err := r.createOrUpdateObjects(ctx, log, sonarr); err != nil {
		return ctrl.Result{}, err
	}

	// TODO: create the other resources for the Sonarr object

	//err := r.ensureStatefulSet(ctx, log, sonarr)
	//if err != nil {
	//	meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeDegraded})
	//}
	//
	meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionTrue, Message: "Ready", Reason: "Ready"})
	meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionFalse, Message: "Healthy", Reason: "Healthy"})
	if err := r.Status().Update(ctx, sonarr); err != nil {
		log.Error(err, "unable to update sonarr status")
		return controllerruntime.Fail(err)
	}

	log.Info("sonarr is ready")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SonarrReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.Sonarr{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

func (r *SonarrReconciler) createOrUpdateObjects(ctx context.Context, log logr.Logger, sonarr *feddemadevv1alpha1.Sonarr) error {
	//TODO: Create this function
	found := &appsv1.StatefulSet{}
	err := r.Get(ctx, client.ObjectKey{Namespace: sonarr.Namespace, Name: sonarr.Name}, found)
	if err != nil && apierrors.IsNotFound(err) {
		ss, err := r.statefulSetForSonarr(sonarr)
		if err != nil {
			log.Error(err, "failed to generate StatefulSet for sonarr")
			meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeAvailable, Status: metav1.ConditionFalse, Reason: "Reconciliation", Message: "Failed to generate StatefulSet"})
			return err
		}

		log.Info("creating a new StatefulSet", "StatefulSet.Namespace", ss.Namespace, "StatefulSet.Name", ss.Name)
		if err = r.Create(ctx, ss); err != nil {
			log.Error(err, "failed to create new StatefulSet", "StatefulSet.Namespace", ss.Namespace, "StatefulSet.Name", ss.Name)
			return err
		}

		// StatefulSet created successfully - continue with the reconciliation
	}
	return nil
}

func (r *SonarrReconciler) statefulSetForSonarr(sonarr *feddemadevv1alpha1.Sonarr) (*appsv1.StatefulSet, error) {
	labels := labelsForSonarr(sonarr.Name)

	statefulset := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sonarr.Name,
			Namespace: sonarr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:             ptr.To(int32(1)),
			RevisionHistoryLimit: ptr.To(int32(1)),

			ServiceName: sonarr.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Affinity:         sonarr.Spec.PodSpec.Affinity,
					NodeSelector:     sonarr.Spec.PodSpec.NodeSelector,
					Tolerations:      sonarr.Spec.PodSpec.Tolerations,
					NodeName:         sonarr.Spec.PodSpec.NodeName,
					SecurityContext:  sonarr.Spec.PodSpec.SecurityContext,
					ImagePullSecrets: sonarr.Spec.PodSpec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "sonarr",
							Image:           sonarr.Spec.PodSpec.Image,
							ImagePullPolicy: sonarr.Spec.PodSpec.ImagePullPolicy,
							Ports: []corev1.ContainerPort{{
								ContainerPort: 8989,
								Name:          "http",
								Protocol:      corev1.ProtocolTCP,
							}},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/ping",
										Port: intstr.FromString("http")},
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/ping",
										Port: intstr.FromString("http"),
									},
								},
							},
							Resources:       sonarr.Spec.PodSpec.Resources,
							SecurityContext: sonarr.Spec.PodSpec.ContainerSecurityContext,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/config",
								},
								//TODO: Add media volume
							},
						},
					},
					//TODO: Volumes
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "config",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      sonarr.Spec.PodSpec.ConfigVolumeSpec.AccessModes,
						Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: sonarr.Spec.PodSpec.ConfigVolumeSpec.Size}},
						StorageClassName: sonarr.Spec.PodSpec.ConfigVolumeSpec.StorageClassName,
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(sonarr, statefulset, r.Scheme); err != nil {
		return nil, err
	}
	return statefulset, nil
}

func (r *SonarrReconciler) serviceForSonarr(sonarr *feddemadevv1alpha1.Sonarr) (*corev1.Service, error) {
	labels := labelsForSonarr(sonarr.Name)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sonarr.Name,
			Namespace: sonarr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       8989,
				TargetPort: intstr.FromInt32(8989),
				Protocol:   corev1.ProtocolTCP,
			}},
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
		},
	}

	if err := ctrl.SetControllerReference(sonarr, service, r.Scheme); err != nil {
		return nil, err
	}
	return service, nil
}

func labelsForSonarr(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "operatarr",
	}
}
