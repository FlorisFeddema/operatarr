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
	"fmt"
	"strconv"

	"github.com/FlorisFeddema/operatarr/internal/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
)

// SonarrReconciler reconciles a Sonarr object
type SonarrReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Timezone string
}

type sonarrReconcile struct {
	SonarrReconciler

	ctx          context.Context
	log          logr.Logger
	object       feddemadevv1alpha1.Sonarr
	mediaLibrary *feddemadevv1alpha1.MediaLibrary
}

// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *SonarrReconciler) SetupWithManager(mgr ctrl.Manager) error {
	genChangedPredicate := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.Sonarr{}).
		Owns(&appsv1.StatefulSet{},
			builder.WithPredicates(genChangedPredicate),
		).
		Owns(&corev1.Service{},
			builder.WithPredicates(genChangedPredicate),
		).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SonarrReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	log.Info("Starting reconcile iteration for Sonarr", "req", req)

	reconcileHandler := sonarrReconcile{}
	reconcileHandler.SonarrReconciler = *r
	reconcileHandler.ctx = ctx
	reconcileHandler.log = log
	reconcileHandler.object.Name = req.Name
	reconcileHandler.object.Namespace = req.Namespace

	err := reconcileHandler.reconcile()
	if err != nil {
		log.Error(err, "Failed to reconcile Sonarr")
	} else {
		log.Info("Successfully reconciled Sonarr")
	}
	return ctrl.Result{}, err
}

func (r *sonarrReconcile) reconcile() error {
	// Load the object desired state based on resource, operator config and default values.
	if err := r.loadDesiredState(); err != nil {
		return err
	}

	// Preconcile step to handle deletion of resources
	if s, err := r.preconcile(); err != nil || s {
		return err
	}

	r.log.Info("Running Sonarr reconcilers")
	//TODO: add step to create media volume based on MediaLibrary
	reconcilers := []func() error{
		r.reconcileStatefulSet,
		r.reconcileHeadlessService,
		r.reconcileService,
	}
	err := utils.RunConcurrently(reconcilers...)
	//TODO: change this to aggregate errors/condition updates
	if err != nil {
		return err
	}
	return nil
}

// loadDesiredState loads the desired state of the Sonarr resource
func (r *sonarrReconcile) loadDesiredState() error {
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(&r.object), &r.object); err != nil {
		if client.IgnoreNotFound(err) != nil {
			r.log.Error(err, "unable to fetch Sonarr")
			return err
		}
		// Resource not found, could have been deleted before the reconcile request.
		return fmt.Errorf("sonarr resource not found. Ignoring since object must be deleted")
	}

	// Get MediaLibrary referenced by Sonarr
	mediaLibrary, err := getMediaLibraryFromRef(r.ctx, r.Client, r.object.Spec.MediaLibraryRef)
	if err != nil {
		r.log.Error(err, "unable to get MediaLibrary from reference", "MediaLibraryRef", r.object.Spec.MediaLibraryRef)
		return err
	}
	r.mediaLibrary = mediaLibrary

	r.log.Info("Desired state loaded")
	return nil
}

// preconcile handles finalizers and deletion logic
// Returns true if reconciliation should stop
func (r *sonarrReconcile) preconcile() (bool, error) {
	// Check if there is a finalizer present, if not, add it
	if !controllerutil.ContainsFinalizer(&r.object, finalizerName) {
		if ok := controllerutil.AddFinalizer(&r.object, finalizerName); !ok {
			return true, errors.New("unable to add finalizer to sonarr")
		}
		if err := r.Update(r.ctx, &r.object); err != nil {
			r.log.Error(err, "failed to update with finalizer")
			return true, err
		}
		return true, nil
	}

	//Check if the resource is being deleted
	if !r.object.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(&r.object, finalizerName) {
			r.log.Info("performing finalization before deletion of sonarr")

			// TODO: perform finalization

			// Remove finalizer when done
			if ok := controllerutil.RemoveFinalizer(&r.object, finalizerName); !ok {
				return true, errors.New("unable to remove finalizer")
			}
			return true, nil
		}
		return true, errors.New("deletion requested but finalizer not found")
	}
	return false, nil
}

func (r *sonarrReconcile) reconcileStatefulSet() error {
	r.log.Info("Creating or patching StatefulSet")
	ss := &appsv1.StatefulSet{}
	ss.Name = r.object.Name
	ss.Namespace = r.object.Namespace

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, ss, func() error {
		if err := ctrl.SetControllerReference(&r.object, ss, r.Scheme); err != nil {
			return errors.Join(err, errors.New("unable to set controller reference for StatefulSet"))
		}

		ss.SetLabels(mergeMap(ss.GetLabels(), labelsForSonarr(r.object.Name)))

		ss.Spec = appsv1.StatefulSetSpec{
			Replicas:             new(int32(1)),
			RevisionHistoryLimit: new(int32(0)),

			ServiceName: getHeadlessServiceName(r.object.Name),
			Selector: &metav1.LabelSelector{
				MatchLabels: labelsForSonarr(r.object.Name),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labelsForSonarr(r.object.Name),
				},
				Spec: corev1.PodSpec{
					Affinity:                  r.object.Spec.PodSpec.Affinity,
					NodeSelector:              r.object.Spec.PodSpec.NodeSelector,
					Tolerations:               r.object.Spec.PodSpec.Tolerations,
					TopologySpreadConstraints: r.object.Spec.PodSpec.TopologySpreadConstraints,
					NodeName:                  r.object.Spec.PodSpec.NodeName,
					SecurityContext: &corev1.PodSecurityContext{
						SELinuxOptions:     r.object.Spec.PodSpec.SecurityContext.SELinuxOptions,
						SeccompProfile:     r.object.Spec.PodSpec.SecurityContext.SeccompProfile,
						AppArmorProfile:    r.object.Spec.PodSpec.SecurityContext.AppArmorProfile,
						RunAsNonRoot:       new(true),
						RunAsUser:          r.mediaLibrary.Spec.Permissions.RunAsUser,
						RunAsGroup:         r.mediaLibrary.Spec.Permissions.RunAsGroup,
						FSGroup:            r.mediaLibrary.Spec.Permissions.FSGroup,
						SupplementalGroups: r.mediaLibrary.Spec.Permissions.SupplementalGroups,
					},
					ImagePullSecrets: r.object.Spec.PodSpec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "sonarr",
							Image:           r.object.Spec.PodSpec.Image,
							ImagePullPolicy: r.object.Spec.PodSpec.ImagePullPolicy,
							Ports: []corev1.ContainerPort{{
								ContainerPort: 8989,
								Name:          "http",
								Protocol:      corev1.ProtocolTCP,
							}},
							LivenessProbe: &corev1.Probe{
								FailureThreshold:    5,
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/ping",
										Scheme: corev1.URISchemeHTTP,
										Port:   intstr.FromInt32(8989),
									},
								},
							},
							ReadinessProbe: &corev1.Probe{
								FailureThreshold:    5,
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/ping",
										Scheme: corev1.URISchemeHTTP,
										Port:   intstr.FromInt32(8989),
									},
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "TZ",
									Value: r.Timezone,
								},
								{
									Name:  "PUID",
									Value: strconv.FormatInt(*r.mediaLibrary.Spec.Permissions.RunAsUser, 10),
								},
								{
									Name:  "PGID",
									Value: strconv.FormatInt(*r.mediaLibrary.Spec.Permissions.RunAsGroup, 10),
								},
							},
							Resources: r.object.Spec.PodSpec.Resources,
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								Privileged:               new(false),
								ReadOnlyRootFilesystem:   new(true),
								AllowPrivilegeEscalation: new(false),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/config",
								},
								{
									Name:      "media",
									MountPath: "/media",
								},
							},
						},
					},
					//TODO: Add media volume
					Volumes: []corev1.Volume{
						{
							Name: "media",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: "mediaPvcName",
								},
							},
						},
					},
					//TODO: Add media volume
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "config",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      r.object.Spec.PodSpec.ConfigVolumeSpec.AccessModes,
						Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: r.object.Spec.PodSpec.ConfigVolumeSpec.Size}},
						StorageClassName: r.object.Spec.PodSpec.ConfigVolumeSpec.StorageClassName,
					},
				},
			},
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, fmt.Errorf("unable to create StatefulSet with status %s", opResult))
	}

	//TODO: return status to aggregate errors/condition updates
	return nil
}

func (r *sonarrReconcile) reconcileService() error {
	r.log.Info("Creating or patching Service")
	svc := &corev1.Service{}
	svc.Name = r.object.Name
	svc.Namespace = r.object.Namespace

	labels := labelsForSonarr(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(&r.object, svc, r.Scheme); err != nil {
			return errors.Join(err, errors.New("unable to set controller owner reference for Service"))
		}

		svc.SetLabels(mergeMap(svc.GetLabels(), labels))
		svc.Spec = corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       8989,
				TargetPort: intstr.FromInt32(8989),
				Protocol:   corev1.ProtocolTCP,
			}},
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
		}

		return nil
	})
	if err != nil {
		return errors.Join(err, fmt.Errorf("unable to create or patch Service with status %s", opResult))
	}

	//TODO: return status to aggregate errors/condition updates
	return nil
}

func (r *sonarrReconcile) reconcileHeadlessService() error {
	r.log.Info("Creating or patching Headless Service")
	svc := &corev1.Service{}
	svc.Name = getHeadlessServiceName(r.object.Name)
	svc.Namespace = r.object.Namespace

	labels := labelsForSonarr(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(&r.object, svc, r.Scheme); err != nil {
			return errors.Join(err, errors.New("unable to set controller owner reference for Headless Service"))
		}

		svc.SetLabels(mergeMap(svc.GetLabels(), labels))
		svc.Spec = corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       8989,
				TargetPort: intstr.FromInt32(8989),
				Protocol:   corev1.ProtocolTCP,
			}},
			Selector:  labels,
			ClusterIP: corev1.ClusterIPNone,
			Type:      corev1.ServiceTypeClusterIP,
		}

		return nil
	})
	if err != nil {
		return errors.Join(err, fmt.Errorf("unable to create or patch Headless Service with status %s", opResult))
	}

	//TODO: return status to aggregate errors/condition updates
	return nil
}

func labelsForSonarr(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "operatarr",
	}
}

func getHeadlessServiceName(name string) string {
	return fmt.Sprintf("%s-headless", name)
}
