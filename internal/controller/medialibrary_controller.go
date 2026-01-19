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
	"github.com/go-logr/logr"
	v1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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

// SetupWithManager sets up the controller with the Manager.
func (r *MediaLibraryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	genChangedPredicate := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.MediaLibrary{}).
		Owns(&corev1.PersistentVolumeClaim{},
			builder.WithPredicates(genChangedPredicate),
		).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MediaLibraryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	log.Info("Starting reconcile iteration for MediaLibrary", "req", req)

	reconcileHandler := mediaLibraryReconcile{}
	reconcileHandler.MediaLibraryReconciler = *r
	reconcileHandler.ctx = ctx
	reconcileHandler.log = log
	reconcileHandler.object.Name = req.Name
	reconcileHandler.object.Namespace = req.Namespace

	err := reconcileHandler.reconcile()
	if err != nil {
		log.Error(err, "Failed to reconcile MediaLibrary")
	} else {
		log.Info("Successfully reconciled MediaLibrary")
	}
	return ctrl.Result{}, err
}

func (r *mediaLibraryReconcile) reconcile() error {
	// Load the object desired state based on resource, operator config and default values.
	if err := r.loadDesiredState(); err != nil {
		return err
	}

	// Preconcile step to handle deletion of resources
	if err := r.preconcile(); err != nil {
		return err
	}

	r.log.Info("Running MediaLibrary reconcilers")
	reconcilers := []func() error{
		r.reconcileMainPvc,
	}
	err := utils.RunConcurrently(reconcilers...)
	if err != nil {
		return err
	}
	return nil
}

func (r *mediaLibraryReconcile) loadDesiredState() error {
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(&r.object), &r.object); err != nil {
		if client.IgnoreNotFound(err) != nil {
			r.log.Error(err, "unable to fetch MediaLibrary")
			r.log.Error(err, "unable to fetch MediaLibrary")
			return err
		}
		// Resource not found, could have been deleted before the reconcile request.
		return fmt.Errorf("mediaLibrary resource not found. Ignoring since object must be deleted")
	}
	r.log.Info("Desired state loaded")
	return nil
}

func (r *mediaLibraryReconcile) preconcile() error {
	// Check if there is a finalizer present, if not, add it
	if !controllerutil.ContainsFinalizer(&r.object, finalizerName) {
		r.log.Info("adding finalizer to MediaLibrary")
		if ok := controllerutil.AddFinalizer(&r.object, finalizerName); !ok {
			return errors.New("unable to add finalizer to MediaLibrary")
		}
		if err := r.Update(r.ctx, &r.object); err != nil {
			r.log.Error(err, "failed to update sonarr with finalizer")
			return err
		}
		//TODO: maybe update object status to reflect finalizer addition or stop reconcile with status requeue
		//TODO: this might cause failures at the moment
		return nil
	}

	//Check if the resource is being deleted
	if !r.object.GetDeletionTimestamp().IsZero()  {
		if controllerutil.ContainsFinalizer(&r.object, finalizerName) {
			r.log.Info("performing finalization for sonarr before deletion")
			meta.SetStatusCondition(&r.object.Status.Conditions, metav1.Condition{Type: typeDegraded, Status: metav1.ConditionUnknown, Reason: "Finalizing", Message: fmt.Sprintf("Finalizing %s before deletion", sonarr.Name)})

			if err := r.Status().Update(r.ctx, &r.object); err != nil {
				r.log.Error(err, "failed to update sonarr status")
				return err
			}

			// TODO: perform finalization

			if err := r.Get(r.ctx, r.object, sonarr); err != nil {
				r.log.Error(err, "failed to re-fetch sonarr")
				return err
			}
		return nil
	}


	return nil
}

func (r *mediaLibraryReconcile) reconcileMainPvc() error {
	// If a pre-existing PVC is specified, skip creation
	if r.object.Spec.PVC.PVCName != nil {
		r.log.Info("Using pre-existing PVC", "pvcName", *r.object.Spec.PVC.PVCName)

		// Check if the PVC exists
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(r.ctx, client.ObjectKey{Namespace: r.object.Namespace, Name: *r.object.Spec.PVC.PVCName}, pvc)

		if err != nil {
			r.log.Error(err, "unable to fetch pre-existing PVC", "pvcName", *r.object.Spec.PVC.PVCName)

			cond := metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionFalse,
				Reason:  "ExistingPVCNotFound",
				Message: "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' was not found",
			}
			utils.MergeConditions(&r.object.Status.Conditions, cond)
			r.object.Status.Initialized = true
			updateErr := r.Status().Update(r.ctx, &r.object)
			if updateErr != nil {
				r.log.Error(updateErr, "unable to update MediaLibrary status")
				return errors.Join(err, updateErr)
			}
			return errors.Join(err, errors.New("unable to fetch pre-existing PVC"))
		}

		// Check if pvc has ReadWriteMany access mode
		if !slices.Contains(pvc.Spec.AccessModes, corev1.ReadWriteMany) {
			err := errors.New("pre-existing PVC does not have ReadWriteMany access mode")
			cond := metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionFalse,
				Reason:  "ExistingPVCInvalidAccessMode",
				Message: "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' does not have ReadWriteMany access mode",
			}
			utils.MergeConditions(&r.object.Status.Conditions, cond)
			updateErr := r.Status().Update(r.ctx, &r.object)
			if updateErr != nil {
				r.log.Error(updateErr, "unable to update MediaLibrary status")
				return errors.Join(err, updateErr)
			}
			return err
		}

		cond := metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionTrue,
			Reason:  "ExistingPVCFound",
			Message: "The specified pre-existing PVC '" + *r.object.Spec.PVC.PVCName + "' was found",
		}
		utils.MergeConditions(&r.object.Status.Conditions, cond)
		r.object.Status.EffectivePVC = r.object.Spec.PVC.PVCName
		updateErr := r.Status().Update(r.ctx, &r.object)
		if updateErr != nil {
			r.log.Error(updateErr, "unable to update MediaLibrary status")
			return updateErr
		}

		return nil
	}

	r.log.Info("Creating or patching PVC", "pvcName", r.object.Name)
	pvc := &corev1.PersistentVolumeClaim{}
	pvc.Name = r.object.Name
	pvc.Namespace = r.object.Namespace

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, pvc, func() error {
		if err := ctrl.SetControllerReference(&r.object, pvc, r.Scheme); err != nil {
			return errors.Join(err, errors.New("unable to set controller owner reference for PVC"))
		}

		r.log.Info("Checking storage class", "pvcStorageClass", pvc.Spec.StorageClassName, "libraryStorageClass", r.object.Spec.PVC.StorageClassName)
		storageClass := cmp.Or(pvc.Spec.StorageClassName, r.object.Spec.PVC.StorageClassName)
		pvc.Spec = corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{"storage": r.object.Spec.PVC.Size}},
			StorageClassName: storageClass,
		}
		pvc.Annotations = mergeMap(pvc.Annotations, r.object.Spec.PVC.Annotations)

		return nil
	})
	if err != nil {
		return errors.Join(err, fmt.Errorf("unable to create or patch PVC with status %s", opResult))
	}

	if opResult != controllerutil.OperationResultNone {
		r.log.Info("PVC reconciled with result", "result", opResult)
		cond := metav1.Condition{
			Type:    "Available",
			Status:  metav1.ConditionFalse,
			Reason:  "PVCCreated",
			Message: fmt.Sprintf("The PVC '%s' was successfully created", pvc.Name),
		}
		utils.MergeConditions(&r.object.Status.Conditions, cond)
		r.log.Info("Condition set", "condition", &r.object.Status.Conditions)
		r.object.Status.EffectivePVC = &pvc.Name
		err = r.Status().Update(r.ctx, &r.object)
		if err != nil {
			r.log.Error(err, "unable to update MediaLibrary status")
			return err
		}
		return nil
	}

	if !r.object.Status.Initialized {
		return initializeJob(r)
	}

	return nil
}

func initializeJob(r *mediaLibraryReconcile) error {
	r.log.Info("Initializing media object")

	// create a kubernetes job to initialize the pvc
	job := newPvcInitJob(&r.object, r.JobRunnerImage)
	if err := ctrl.SetControllerReference(&r.object, job, r.Scheme); err != nil {
		return errors.Join(err, errors.New("unable to set controller owner reference for PVC initialization job"))
	}

	// check if an existing job is running
	err := r.Get(r.ctx, client.ObjectKeyFromObject(job), job)
	if err == nil {
		// job exists, check if it's completed
		for _, c := range job.Status.Conditions {
			if c.Type == v1.JobComplete && c.Status == corev1.ConditionTrue {
				r.log.Info("PVC initialization job completed successfully")

				// if the job completed successfully, set initialized to true
				cond := metav1.Condition{
					Type:    "Available",
					Status:  metav1.ConditionTrue,
					Reason:  "PVCInitialized",
					Message: "The PVC is initialized",
				}
				utils.MergeConditions(&r.object.Status.Conditions, cond)
				r.object.Status.Initialized = true
				err = r.Status().Update(r.ctx, &r.object)
				if err != nil {
					r.log.Error(err, "unable to update MediaLibrary status")
					return err
				}
				return nil
			}

			if c.Type == v1.JobFailed && c.Status == corev1.ConditionTrue {
				r.log.Error(errors.New(c.Message), "PVC initialization job failed")

				cond := metav1.Condition{
					Type:    "Available",
					Status:  metav1.ConditionFalse,
					Reason:  "PVCInitializationFailed",
					Message: "The PVC initialization job failed: " + c.Message,
				}
				utils.MergeConditions(&r.object.Status.Conditions, cond)
				err = r.Status().Update(r.ctx, &r.object)
				if err != nil {
					r.log.Error(err, "unable to update MediaLibrary status")
					return err
				}
				return nil
			}
		}

		r.log.Info("PVC initialization job is still running")
		return nil
	}

	err = r.Create(r.ctx, job)
	if err != nil {
		r.log.Error(err, "unable to create PVC initialization job")
		return errors.Join(err, errors.New("unable to create PVC initialization job"))
	}

	return nil
}

func newPvcInitJob(f *feddemadevv1alpha1.MediaLibrary, image string) *v1.Job {
	return &v1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      f.Name + "-pvc-init",
			Namespace: f.Namespace,
		},
		Spec: v1.JobSpec{
			Parallelism:             utils.PtrToInt32(1),
			Completions:             utils.PtrToInt32(1),
			ActiveDeadlineSeconds:   utils.PtrToInt64(180),
			BackoffLimit:            utils.PtrToInt32(1),
			TTLSecondsAfterFinished: utils.PtrToInt32(3600),
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
									MountPath: "/object",
								},
							},
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: utils.PtrToBool(false),
								Privileged:               utils.PtrToBool(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								ReadOnlyRootFilesystem: utils.PtrToBool(true),
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
					NodeSelector:  map[string]string{"kubernetes.io/arch": "arm64"}, // TODO: remove once multi-arch support is added
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:       utils.PtrToBool(true),
						RunAsUser:          f.Spec.Permissions.RunAsUser,
						RunAsGroup:         f.Spec.Permissions.RunAsGroup,
						FSGroup:            f.Spec.Permissions.FSGroup,
						SupplementalGroups: f.Spec.Permissions.SupplementalGroups,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					ImagePullSecrets: nil, //TODO: add later if needed
				},
			},
		},
	}

}
