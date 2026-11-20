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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	lg "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
)

const SonarrPort int32 = 8989

const sonarrHTTPPortName = "http"

var errCrossNamespaceMediaLibraryUnsupported = errors.New("cross-namespace MediaLibrary references are not supported")

// SonarrReconciler reconciles a Sonarr object
type SonarrReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Timezone            string
	GatewayAPIAvailable bool
}

type sonarrReconcile struct {
	SonarrReconciler

	ctx          context.Context
	log          logr.Logger
	object       feddemadevv1alpha1.Sonarr
	mediaLibrary *feddemadevv1alpha1.MediaLibrary
	mediaPVC     *corev1.PersistentVolumeClaim
}

// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=sonarrs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *SonarrReconciler) SetupWithManager(mgr ctrl.Manager) error {
	genChangedPredicate := predicate.GenerationChangedPredicate{}

	b := ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.Sonarr{}).
		Watches(&feddemadevv1alpha1.MediaLibrary{}, handler.EnqueueRequestsFromMapFunc(r.mapMediaLibraryToSonarrs)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(r.mapMediaLibraryPVCToSonarrs)).
		Owns(&appsv1.StatefulSet{},
			builder.WithPredicates(genChangedPredicate),
		).
		Owns(&corev1.Service{})

	if r.GatewayAPIAvailable {
		b = b.Owns(&gatewayv1.HTTPRoute{}, builder.WithPredicates(genChangedPredicate))
	}

	return b.Complete(r)
}

func (r *SonarrReconciler) mapMediaLibraryToSonarrs(ctx context.Context, object client.Object) []reconcile.Request {
	mediaLibrary, ok := object.(*feddemadevv1alpha1.MediaLibrary)
	if !ok {
		return nil
	}

	sonarrs := &feddemadevv1alpha1.SonarrList{}
	if err := r.List(ctx, sonarrs); err != nil {
		lg.FromContext(ctx).Error(err, "unable to list Sonarr resources for MediaLibrary watch",
			"mediaLibrary", client.ObjectKeyFromObject(mediaLibrary))
		return nil
	}

	requests := make([]reconcile.Request, 0, len(sonarrs.Items))
	for i := range sonarrs.Items {
		sonarr := &sonarrs.Items[i]
		ref := sonarr.Spec.MediaLibraryRef
		namespace := ref.Namespace
		if namespace == "" {
			namespace = sonarr.Namespace
		}
		if ref.Kind != mediaLibraryKind || ref.Name != mediaLibrary.Name || namespace != mediaLibrary.Namespace {
			continue
		}

		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)})
	}
	return requests
}

func (r *SonarrReconciler) mapMediaLibraryPVCToSonarrs(ctx context.Context, object client.Object) []reconcile.Request {
	pvc, ok := object.(*corev1.PersistentVolumeClaim)
	if !ok {
		return nil
	}

	mediaLibraries := &feddemadevv1alpha1.MediaLibraryList{}
	if err := r.List(ctx, mediaLibraries, client.InNamespace(pvc.Namespace)); err != nil {
		lg.FromContext(ctx).Error(err, "unable to list MediaLibrary resources for PVC watch",
			"pvc", client.ObjectKeyFromObject(pvc))
		return nil
	}
	libraryNames := make(map[string]struct{})
	for i := range mediaLibraries.Items {
		library := &mediaLibraries.Items[i]
		if library.Status.EffectivePVC != nil && *library.Status.EffectivePVC == pvc.Name {
			libraryNames[library.Name] = struct{}{}
		}
	}
	if len(libraryNames) == 0 {
		return nil
	}

	sonarrs := &feddemadevv1alpha1.SonarrList{}
	if err := r.List(ctx, sonarrs, client.InNamespace(pvc.Namespace)); err != nil {
		lg.FromContext(ctx).Error(err, "unable to list Sonarr resources for PVC watch",
			"pvc", client.ObjectKeyFromObject(pvc))
		return nil
	}

	requests := make([]reconcile.Request, 0, len(sonarrs.Items))
	for i := range sonarrs.Items {
		sonarr := &sonarrs.Items[i]
		ref := sonarr.Spec.MediaLibraryRef
		refNamespace := ref.Namespace
		if refNamespace == "" {
			refNamespace = sonarr.Namespace
		}
		if ref.Kind != mediaLibraryKind || refNamespace != pvc.Namespace {
			continue
		}
		if _, ok := libraryNames[ref.Name]; ok {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)})
		}
	}
	return requests
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SonarrReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	log.V(1).Info("Starting reconcile iteration", "resource", req.NamespacedName)

	reconcileHandler := sonarrReconcile{}
	reconcileHandler.SonarrReconciler = *r
	reconcileHandler.ctx = ctx
	reconcileHandler.log = log
	reconcileHandler.object.Name = req.Name
	reconcileHandler.object.Namespace = req.Namespace

	err := reconcileHandler.reconcile()
	return ctrl.Result{}, err
}

func (r *sonarrReconcile) reconcile() error {
	// Load the object desired state based on resource, operator config and default values.
	if err := r.loadDesiredState(); err != nil {
		return client.IgnoreNotFound(err)
	}

	// Preconcile step to handle deletion of resources
	if s, err := r.preconcile(); err != nil || s {
		return err
	}

	if !r.object.Spec.HttpRouteSpec.Enabled {
		if err := r.reconcileHttpRoute(); err != nil {
			err = fmt.Errorf("unable to reconcile HTTPRoute: %w", err)
			return errors.Join(err, r.setAvailableCondition(metav1.ConditionFalse, "ReconciliationFailed", err.Error()))
		}
	}

	if err := r.loadMediaLibrary(); err != nil {
		if errors.Is(err, errCrossNamespaceMediaLibraryUnsupported) {
			return r.setAvailableCondition(metav1.ConditionFalse, "CrossNamespaceMediaLibraryUnsupported", err.Error())
		}
		return errors.Join(err, r.setAvailableCondition(metav1.ConditionFalse, "MediaLibraryUnavailable", err.Error()))
	}

	r.log.V(1).Info("Running Sonarr reconcilers")
	type reconcileStep struct {
		name string
		run  func() error
	}
	steps := []reconcileStep{
		{name: "headless Service", run: r.reconcileHeadlessService},
		{name: "Service", run: r.reconcileService},
	}
	if r.object.Spec.HttpRouteSpec.Enabled {
		steps = append(steps, reconcileStep{name: "HTTPRoute", run: r.reconcileHttpRoute})
	}
	steps = append(steps,
		reconcileStep{name: "StatefulSet", run: r.reconcileStatefulSet},
	)
	for _, step := range steps {
		if err := step.run(); err != nil {
			err = fmt.Errorf("unable to reconcile %s: %w", step.name, err)
			statusErr := r.setAvailableCondition(metav1.ConditionFalse, "ReconciliationFailed", err.Error())
			return errors.Join(err, statusErr)
		}
	}

	return r.setAvailableCondition(metav1.ConditionTrue, "ResourcesReconciled", "Sonarr resources have been reconciled")
}

// loadDesiredState loads the desired state of the Sonarr resource
func (r *sonarrReconcile) loadDesiredState() error {
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(&r.object), &r.object); err != nil {
		return fmt.Errorf("unable to fetch Sonarr %s: %w", client.ObjectKeyFromObject(&r.object), err)
	}

	r.log.V(1).Info("Desired state loaded")
	return nil
}

func (r *sonarrReconcile) loadMediaLibrary() error {
	ref := r.object.Spec.MediaLibraryRef
	if ref.Namespace == "" {
		ref.Namespace = r.object.Namespace
	}
	if ref.Namespace != r.object.Namespace {
		return fmt.Errorf("%w: Sonarr %s cannot use MediaLibrary %s",
			errCrossNamespaceMediaLibraryUnsupported,
			client.ObjectKeyFromObject(&r.object),
			client.ObjectKey{Name: ref.Name, Namespace: ref.Namespace})
	}
	mediaLibrary, err := getMediaLibraryFromRef(r.ctx, r.Client, ref)
	if err != nil {
		return fmt.Errorf("unable to get MediaLibrary from reference: %w", err)
	}
	r.mediaLibrary = mediaLibrary

	if !mediaLibrary.Status.Initialized {
		return errors.New("referenced MediaLibrary is not initialized")
	}

	mediaPVC, err := getMediaLibraryPVC(r.Client, r.ctx, *mediaLibrary)
	if err != nil {
		return fmt.Errorf("unable to get PVC for referenced MediaLibrary: %w", err)
	}
	r.mediaPVC = mediaPVC

	return nil
}

func (r *sonarrReconcile) setAvailableCondition(status metav1.ConditionStatus, reason, message string) error {
	for _, condition := range r.object.Status.Conditions {
		if condition.Type == typeAvailable &&
			condition.Status == status &&
			condition.Reason == reason &&
			condition.Message == message &&
			condition.ObservedGeneration == r.object.Generation {
			return nil
		}
	}

	condition := metav1.Condition{
		Type:               typeAvailable,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: r.object.Generation,
	}
	utils.MergeConditions(&r.object.Status.Conditions, condition)
	if err := r.Status().Update(r.ctx, &r.object); err != nil {
		return fmt.Errorf("unable to update Sonarr status condition: %w", err)
	}
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
			return true, fmt.Errorf("unable to update Sonarr finalizer: %w", err)
		}
		return true, nil
	}

	// Check if the resource is being deleted.
	if !r.object.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(&r.object, finalizerName) {
			r.log.Info("performing finalization before deletion of sonarr")

			// TODO: perform finalization

			// Remove finalizer when done
			if ok := controllerutil.RemoveFinalizer(&r.object, finalizerName); !ok {
				return true, errors.New("unable to remove finalizer")
			}
			if err := r.Update(r.ctx, &r.object); err != nil {
				return true, fmt.Errorf("unable to update Sonarr finalizer: %w", err)
			}
			return true, nil
		}
		return true, errors.New("deletion requested but finalizer not found")
	}
	return false, nil
}

func (r *sonarrReconcile) reconcileStatefulSet() error {
	if r.mediaPVC == nil {
		return errors.New("referenced MediaLibrary PVC was not loaded")
	}

	r.log.V(1).Info("Creating or patching StatefulSet")
	ss := &appsv1.StatefulSet{}
	ss.Name = r.object.Name
	ss.Namespace = r.object.Namespace
	labels := labelsForResource(r.object.Name)
	permissions := effectiveMediaPermissions(r.mediaLibrary.Spec.Permissions)
	podSecurityContext := &corev1.PodSecurityContext{
		RunAsNonRoot:       new(true),
		RunAsUser:          permissions.RunAsUser,
		RunAsGroup:         permissions.RunAsGroup,
		FSGroup:            permissions.FSGroup,
		SupplementalGroups: permissions.SupplementalGroups,
	}
	if configured := r.object.Spec.PodSpec.SecurityContext; configured != nil {
		podSecurityContext.SELinuxOptions = configured.SELinuxOptions
		podSecurityContext.SeccompProfile = configured.SeccompProfile
		podSecurityContext.AppArmorProfile = configured.AppArmorProfile
	}

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, ss, func() error {
		if err := ctrl.SetControllerReference(&r.object, ss, r.Scheme); err != nil {
			return fmt.Errorf("unable to set controller reference for StatefulSet: %w", err)
		}

		ss.SetLabels(mergeMap(ss.GetLabels(), labels))
		ss.Spec = appsv1.StatefulSetSpec{
			Replicas:             new(int32(1)),
			RevisionHistoryLimit: new(int32(0)),

			ServiceName: getHeadlessServiceName(r.object.Name),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Affinity:                  r.object.Spec.PodSpec.Affinity,
					NodeSelector:              r.object.Spec.PodSpec.NodeSelector,
					Tolerations:               r.object.Spec.PodSpec.Tolerations,
					TopologySpreadConstraints: r.object.Spec.PodSpec.TopologySpreadConstraints,
					NodeName:                  r.object.Spec.PodSpec.NodeName,
					SecurityContext:           podSecurityContext,
					ImagePullSecrets:          r.object.Spec.PodSpec.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "sonarr",
							Image:           r.object.Spec.PodSpec.Image,
							ImagePullPolicy: r.object.Spec.PodSpec.ImagePullPolicy,
							Ports: []corev1.ContainerPort{{
								ContainerPort: SonarrPort,
								Name:          sonarrHTTPPortName,
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
										Port:   intstr.FromInt32(SonarrPort),
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
										Port:   intstr.FromInt32(SonarrPort),
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
									Value: strconv.FormatInt(*permissions.RunAsUser, 10),
								},
								{
									Name:  "PGID",
									Value: strconv.FormatInt(*permissions.RunAsGroup, 10),
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
					Volumes: []corev1.Volume{
						{
							Name: "media",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: r.mediaPVC.Name,
								},
							},
						},
					},
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
		return fmt.Errorf("unable to create or patch StatefulSet %s: %w", client.ObjectKeyFromObject(ss), err)
	}

	logResourceOperation(r.log, "StatefulSet", ss, opResult)
	return nil
}

func (r *sonarrReconcile) reconcileService() error {
	r.log.V(1).Info("Creating or patching Service")
	svc := &corev1.Service{}
	svc.Name = r.object.Name
	svc.Namespace = r.object.Namespace
	labels := labelsForResource(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(&r.object, svc, r.Scheme); err != nil {
			return fmt.Errorf("unable to set controller owner reference for Service: %w", err)
		}

		svc.SetLabels(mergeMap(svc.GetLabels(), labels))
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       sonarrHTTPPortName,
			Port:       SonarrPort,
			TargetPort: intstr.FromInt32(SonarrPort),
			Protocol:   corev1.ProtocolTCP,
		}}
		svc.Spec.Selector = labels
		svc.Spec.Type = corev1.ServiceTypeClusterIP

		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to create or patch Service %s: %w", client.ObjectKeyFromObject(svc), err)
	}

	logResourceOperation(r.log, "Service", svc, opResult)
	return nil
}

func (r *sonarrReconcile) reconcileHeadlessService() error {
	r.log.V(1).Info("Creating or patching headless Service")
	svc := &corev1.Service{}
	svc.Name = getHeadlessServiceName(r.object.Name)
	svc.Namespace = r.object.Namespace
	labels := labelsForResource(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, svc, func() error {
		if err := ctrl.SetControllerReference(&r.object, svc, r.Scheme); err != nil {
			return fmt.Errorf("unable to set controller owner reference for headless Service: %w", err)
		}

		svc.SetLabels(mergeMap(svc.GetLabels(), labels))
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       sonarrHTTPPortName,
			Port:       SonarrPort,
			TargetPort: intstr.FromInt32(SonarrPort),
			Protocol:   corev1.ProtocolTCP,
		}}
		svc.Spec.Selector = labels
		svc.Spec.ClusterIP = corev1.ClusterIPNone
		svc.Spec.Type = corev1.ServiceTypeClusterIP

		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to create or patch headless Service %s: %w", client.ObjectKeyFromObject(svc), err)
	}

	logResourceOperation(r.log, "Service", svc, opResult)
	return nil
}

func (r *sonarrReconcile) reconcileHttpRoute() error {
	if !r.GatewayAPIAvailable {
		r.log.V(1).Info("Gateway API is unavailable; skipping HTTPRoute while continuing Sonarr reconciliation")
		return nil
	}

	route := &gatewayv1.HTTPRoute{}
	route.Name = r.object.Name
	route.Namespace = r.object.Namespace

	if !r.object.Spec.HttpRouteSpec.Enabled {
		key := client.ObjectKeyFromObject(route)
		if err := r.Get(r.ctx, key, route); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("unable to fetch HTTPRoute %s for deletion: %w", key, err)
		}
		if !metav1.IsControlledBy(route, &r.object) {
			return fmt.Errorf("cannot delete HTTPRoute %s: not controlled by Sonarr %s",
				key, client.ObjectKeyFromObject(&r.object))
		}
		if !route.DeletionTimestamp.IsZero() {
			return nil
		}
		if err := r.Delete(r.ctx, route, client.Preconditions{
			UID: &route.UID, ResourceVersion: &route.ResourceVersion,
		}); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("unable to delete HTTPRoute %s: %w", key, err)
		}
		r.log.Info("Resource deletion requested", "kind", "HTTPRoute", "resource", key)
		return nil
	}

	r.log.V(1).Info("Creating or patching HTTPRoute")
	labels := labelsForResource(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, route, func() error {
		if err := ctrl.SetControllerReference(&r.object, route, r.Scheme); err != nil {
			return fmt.Errorf("unable to set controller owner reference for HTTPRoute: %w", err)
		}

		route.SetLabels(mergeMap(route.GetLabels(), labels))
		route.Spec = gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					r.object.Spec.HttpRouteSpec.ParentRef,
				},
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(r.object.Spec.HttpRouteSpec.Hostname)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: gatewayv1.ObjectName(route.Name),
									Port: new(SonarrPort),
								},
							},
						},
					},
				},
			},
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to create or patch HTTPRoute %s: %w", client.ObjectKeyFromObject(route), err)
	}

	logResourceOperation(r.log, "HTTPRoute", route, opResult)
	return nil
}
