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
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/FlorisFeddema/operatarr/internal/utils"
	"github.com/FlorisFeddema/operatarr/jobrunner/medialibrary"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
)

// MediaLibraryReconciler reconciles a MediaLibrary object
type MediaLibraryReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	JobRunnerImage string
}

// A local reconcile object tied to a single reconcile iteration
type mediaLibraryReconcile struct {
	MediaLibraryReconciler

	ctx    context.Context
	log    logr.Logger
	object feddemadevv1alpha1.MediaLibrary
}

// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create;get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;patch

// SetupWithManager sets up the controller with the Manager.
func (r *MediaLibraryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	genChangedPredicate := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.MediaLibrary{}).
		Owns(&corev1.PersistentVolumeClaim{},
			builder.WithPredicates(genChangedPredicate),
		).
		Owns(&v1.Job{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MediaLibraryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	log.V(1).Info("Starting reconcile iteration", "resource", req.NamespacedName)

	reconcileHandler := mediaLibraryReconcile{}
	reconcileHandler.MediaLibraryReconciler = *r
	reconcileHandler.ctx = ctx
	reconcileHandler.log = log
	reconcileHandler.object.Name = req.Name
	reconcileHandler.object.Namespace = req.Namespace

	err := reconcileHandler.reconcile()
	return ctrl.Result{}, err
}

func (r *mediaLibraryReconcile) reconcile() error {
	// Load the object desired state based on resource, operator config and default values.
	if err := r.loadDesiredState(); err != nil {
		return client.IgnoreNotFound(err)
	}

	// Preconcile step to handle deletion of resources
	if s, err := r.preconcile(); err != nil || s {
		return err
	}

	r.log.V(1).Info("Running MediaLibrary reconcilers")
	if err := r.reconcileMainPvc(); err != nil {
		return fmt.Errorf("unable to reconcile MediaLibrary PVC: %w", err)
	}
	return nil
}

func (r *mediaLibraryReconcile) loadDesiredState() error {
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(&r.object), &r.object); err != nil {
		return fmt.Errorf("unable to fetch MediaLibrary %s: %w", client.ObjectKeyFromObject(&r.object), err)
	}
	r.log.V(1).Info("Desired state loaded")
	return nil
}

func (r *mediaLibraryReconcile) updateStatus() error {
	if err := r.Status().Update(r.ctx, &r.object); err != nil {
		return fmt.Errorf("unable to update MediaLibrary status: %w", err)
	}
	return nil
}

// preconcile handles finalizers and deletion logic
// Returns true if reconciliation should stop
func (r *mediaLibraryReconcile) preconcile() (bool, error) {
	// Check if there is a finalizer present, if not, add it
	if !controllerutil.ContainsFinalizer(&r.object, finalizerName) {
		if ok := controllerutil.AddFinalizer(&r.object, finalizerName); !ok {
			return true, errors.New("unable to add finalizer to mediaLibrary")
		}
		if err := r.Update(r.ctx, &r.object); err != nil {
			return true, fmt.Errorf("unable to update MediaLibrary finalizer: %w", err)
		}
		return true, nil
	}

	// Check if the resource is being deleted.
	if !r.object.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(&r.object, finalizerName) {
			r.log.Info("performing finalization before deletion of mediaLibrary")

			if err := r.finalizeManagedPVC(); err != nil {
				return true, err
			}

			// Remove finalizer when done
			if ok := controllerutil.RemoveFinalizer(&r.object, finalizerName); !ok {
				return true, errors.New("unable to remove finalizer")
			}
			if err := r.Update(r.ctx, &r.object); err != nil {
				return true, fmt.Errorf("unable to remove MediaLibrary finalizer: %w", err)
			}
			return true, nil
		}
		return true, errors.New("deletion requested but finalizer not found")
	}
	return false, nil
}

func (r *mediaLibraryReconcile) finalizeManagedPVC() error {
	if r.object.Spec.PVC.PVCName != nil ||
		r.object.Spec.PVCRetentionPolicy != feddemadevv1alpha1.PVCRetentionPolicyRetain {
		return nil
	}

	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Namespace: r.object.Namespace, Name: r.object.Name}
	if err := r.Get(r.ctx, key, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("unable to fetch managed PVC %s for retention: %w", key, err)
	}

	ownerReferences := pvc.GetOwnerReferences()
	retainedOwnerReferences := make([]metav1.OwnerReference, 0, len(ownerReferences))
	for _, ownerReference := range ownerReferences {
		if ownerReference.UID != r.object.UID {
			retainedOwnerReferences = append(retainedOwnerReferences, ownerReference)
		}
	}
	if len(retainedOwnerReferences) == len(ownerReferences) {
		return nil
	}

	before := pvc.DeepCopy()
	pvc.SetOwnerReferences(retainedOwnerReferences)
	if err := r.Patch(r.ctx, pvc, client.MergeFrom(before)); err != nil {
		return fmt.Errorf("unable to retain managed PVC %s: %w", key, err)
	}

	r.log.Info("Retained managed PVC", "resource", key)
	return nil
}

func (r *mediaLibraryReconcile) reconcileMainPvc() error {
	// If a pre-existing PVC is specified, skip creation
	if r.object.Spec.PVC.PVCName != nil {
		r.log.V(1).Info("Using pre-existing PVC", "pvcName", *r.object.Spec.PVC.PVCName)

		// Check if the PVC exists
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(r.ctx, client.ObjectKey{Namespace: r.object.Namespace, Name: *r.object.Spec.PVC.PVCName}, pvc)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("unable to fetch pre-existing PVC %q: %w", *r.object.Spec.PVC.PVCName, err)
			}

			cond := metav1.Condition{
				Type:               typeAvailable,
				Status:             metav1.ConditionFalse,
				Reason:             "ExistingPVCNotFound",
				Message:            "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' was not found",
				ObservedGeneration: r.object.Generation,
			}
			utils.MergeConditions(&r.object.Status.Conditions, cond)
			r.object.Status.Initialized = false
			r.object.Status.EffectivePVC = nil
			return errors.Join(fmt.Errorf("unable to fetch pre-existing PVC %q: %w", *r.object.Spec.PVC.PVCName, err), r.updateStatus())
		}

		// Check if pvc has ReadWriteMany access mode
		if !slices.Contains(pvc.Spec.AccessModes, corev1.ReadWriteMany) {
			err := errors.New("pre-existing PVC does not have ReadWriteMany access mode")
			cond := metav1.Condition{
				Type:               typeAvailable,
				Status:             metav1.ConditionFalse,
				Reason:             "ExistingPVCInvalidAccessMode",
				Message:            "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' does not have ReadWriteMany access mode",
				ObservedGeneration: r.object.Generation,
			}
			utils.MergeConditions(&r.object.Status.Conditions, cond)
			r.object.Status.Initialized = false
			r.object.Status.EffectivePVC = nil
			return errors.Join(err, r.updateStatus())
		}

		cond := metav1.Condition{
			Type:               typeAvailable,
			Status:             metav1.ConditionTrue,
			Reason:             "ExistingPVCFound",
			Message:            "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' was found",
			ObservedGeneration: r.object.Generation,
		}
		utils.MergeConditions(&r.object.Status.Conditions, cond)
		r.object.Status.EffectivePVC = r.object.Spec.PVC.PVCName
		r.object.Status.Initialized = true
		return r.updateStatus()
	}

	if r.object.Spec.PVC.Size == nil {
		return errors.New("size is required for a managed PVC")
	}

	r.log.V(1).Info("Creating or patching PVC", "pvcName", r.object.Name)
	pvc := &corev1.PersistentVolumeClaim{}
	pvc.Name = r.object.Name
	pvc.Namespace = r.object.Namespace
	labels := labelsForResource(r.object.Name)

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, pvc, func() error {
		if err := ctrl.SetControllerReference(&r.object, pvc, r.Scheme); err != nil {
			return fmt.Errorf("unable to set controller owner reference for PVC: %w", err)
		}

		r.log.V(1).Info("Checking storage class", "pvcStorageClass", pvc.Spec.StorageClassName, "libraryStorageClass", r.object.Spec.PVC.StorageClassName)
		storageClass := cmp.Or(pvc.Spec.StorageClassName, r.object.Spec.PVC.StorageClassName)
		pvc.SetLabels(mergeMap(pvc.GetLabels(), labels))
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		if pvc.Spec.Resources.Requests == nil {
			pvc.Spec.Resources.Requests = corev1.ResourceList{}
		}
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = *r.object.Spec.PVC.Size
		pvc.Spec.StorageClassName = storageClass
		pvc.Annotations = mergeMap(pvc.GetAnnotations(), r.object.Spec.PVC.Annotations)

		return nil
	})
	if err != nil {
		return fmt.Errorf("unable to create or patch PVC %s: %w", client.ObjectKeyFromObject(pvc), err)
	}

	if opResult != controllerutil.OperationResultNone {
		logResourceOperation(r.log, "PersistentVolumeClaim", pvc, opResult)
	}
	if opResult != controllerutil.OperationResultNone ||
		r.object.Status.EffectivePVC == nil || *r.object.Status.EffectivePVC != pvc.Name {
		cond := metav1.Condition{
			Type:               typeAvailable,
			Status:             metav1.ConditionFalse,
			Reason:             "PVCCreated",
			Message:            fmt.Sprintf("The PVC '%s' was successfully created", pvc.Name),
			ObservedGeneration: r.object.Generation,
		}
		utils.MergeConditions(&r.object.Status.Conditions, cond)
		r.object.Status.Initialized = false
		r.object.Status.EffectivePVC = &pvc.Name
		if err := r.updateStatus(); err != nil {
			return err
		}
	}

	if !r.object.Status.Initialized {
		return initializeJob(r)
	}

	return nil
}

func initializeJob(r *mediaLibraryReconcile) error {
	r.log.V(1).Info("Initializing media object")
	if r.object.Status.EffectivePVC == nil || *r.object.Status.EffectivePVC == "" {
		return errors.New("MediaLibrary has no effective PVC for initialization")
	}

	// create a kubernetes job to initialize the pvc
	job := newPvcInitJob(&r.object, r.JobRunnerImage)
	if err := ctrl.SetControllerReference(&r.object, job, r.Scheme); err != nil {
		return fmt.Errorf("unable to set controller owner reference for PVC initialization job: %w", err)
	}

	// check if an existing job is running
	err := r.Get(r.ctx, client.ObjectKeyFromObject(job), job)
	if err == nil {
		// job exists, check if it's completed
		for _, c := range job.Status.Conditions {
			if c.Type == v1.JobComplete && c.Status == corev1.ConditionTrue {
				// if the job completed successfully, set initialized to true
				cond := metav1.Condition{
					Type:               typeAvailable,
					Status:             metav1.ConditionTrue,
					Reason:             "PVCInitialized",
					Message:            "The PVC is initialized",
					ObservedGeneration: r.object.Generation,
				}
				utils.MergeConditions(&r.object.Status.Conditions, cond)
				r.object.Status.Initialized = true
				if err := r.updateStatus(); err != nil {
					return err
				}
				r.log.Info("PVC initialization job completed", "job", client.ObjectKeyFromObject(job))
				return nil
			}

			if c.Type == v1.JobFailed && c.Status == corev1.ConditionTrue {
				jobErr := fmt.Errorf("PVC initialization job %s failed: %s", client.ObjectKeyFromObject(job), c.Message)
				cond := metav1.Condition{
					Type:               typeAvailable,
					Status:             metav1.ConditionFalse,
					Reason:             "PVCInitializationFailed",
					Message:            "The PVC initialization job failed: " + c.Message,
					ObservedGeneration: r.object.Generation,
				}
				previous := meta.FindStatusCondition(r.object.Status.Conditions, typeAvailable)
				changed := previous == nil || previous.Status != cond.Status || previous.Reason != cond.Reason ||
					previous.Message != cond.Message || previous.ObservedGeneration != cond.ObservedGeneration
				utils.MergeConditions(&r.object.Status.Conditions, cond)
				r.object.Status.Initialized = false
				if err := r.updateStatus(); err != nil {
					return errors.Join(jobErr, err)
				}
				if changed {
					r.log.Error(jobErr, "PVC initialization job failed", "job", client.ObjectKeyFromObject(job), "reason", c.Reason)
				}
				return nil
			}
		}

		r.log.V(1).Info("PVC initialization job is still running", "job", client.ObjectKeyFromObject(job))
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("unable to fetch PVC initialization job: %w", err)
	}

	err = r.Create(r.ctx, job)
	if err != nil {
		return fmt.Errorf("unable to create PVC initialization job %s: %w", client.ObjectKeyFromObject(job), err)
	}

	logResourceOperation(r.log, "Job", job, controllerutil.OperationResultCreated)
	return nil
}

func newPvcInitJob(f *feddemadevv1alpha1.MediaLibrary, image string) *v1.Job {
	permissions := effectiveMediaPermissions(f.Spec.Permissions)

	return &v1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.Name + "-pvc-init",
			Namespace: f.Namespace,
		},
		Spec: v1.JobSpec{
			Parallelism:             new(int32(1)),
			Completions:             new(int32(1)),
			ActiveDeadlineSeconds:   new(int64(180)),
			BackoffLimit:            new(int32(1)),
			TTLSecondsAfterFinished: new(int32(3600)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      f.Name + "-pvc-init",
					Namespace: f.Namespace,
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "object",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: *f.Status.EffectivePVC,
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "initializer",
							Image: image,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    utils.MustParseResource("20m"),
									corev1.ResourceMemory: utils.MustParseResource("32Mi"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "object",
									MountPath: medialibrary.DefaultLibraryRootPath,
								},
							},
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: new(false),
								Privileged:               new(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								ReadOnlyRootFilesystem: new(true),
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:       new(true),
						RunAsUser:          permissions.RunAsUser,
						RunAsGroup:         permissions.RunAsGroup,
						FSGroup:            permissions.FSGroup,
						SupplementalGroups: permissions.SupplementalGroups,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					ImagePullSecrets: nil, // TODO: add later if needed.
				},
			},
		},
	}

}
