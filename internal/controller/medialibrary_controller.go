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
	"os"
	"slices"

	"github.com/FlorisFeddema/operatarr/internal/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
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
}

// A local reconcile object tied to a single reconcile iteration
type mediaLibraryReconcile struct {
	MediaLibraryReconciler

	ctx     context.Context
	log     logr.Logger
	library feddemadevv1alpha1.MediaLibrary
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
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MediaLibrary object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *MediaLibraryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	log.Info("Starting reconcile iteration for MediaLibrary", "req", req)

	reconcileHandler := mediaLibraryReconcile{}
	reconcileHandler.MediaLibraryReconciler = *r
	reconcileHandler.ctx = ctx
	reconcileHandler.log = log
	reconcileHandler.library.Name = req.Name
	reconcileHandler.library.Namespace = req.Namespace

	err := reconcileHandler.reconcile()
	if err != nil {
		log.Error(err, "MediaLibrary reconciliation failed")
		os.Exit(1) // TODO: remove exit once it is stable
	} else {
		log.Info("MediaLibrary reconciliation completed successfully")
	}
	return ctrl.Result{}, err
}

func (r *mediaLibraryReconcile) reconcile() error {
	//Check if the resource is being deleted
	if err := r.Get(r.ctx, client.ObjectKey{Namespace: r.library.Namespace, Name: r.library.Name}, &r.library); err != nil {
		if client.IgnoreNotFound(err) != nil {
			r.log.Error(err, "unable to fetch MediaLibrary")
			return err
		}
		// Resource not found, could have been deleted after reconcile request.
		r.log.Info("MediaLibrary resource not found. Ignoring since object must be deleted.")
		return nil
	}
	if !r.library.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is being deleted
		r.log.Info("MediaLibrary resource is being deleted")
		return nil
	}

	// Load the object desired state based on resource, operator config and default values.
	if err := r.LoadDesiredState(); err != nil {
		return err
	}

	r.log.Info("Running reconcilers")
	reconcilers := []func() error{
		r.reconcileMainPvc,
	}
	err := utils.RunConcurrently(reconcilers...)
	if err != nil {
		return err
	}
	return nil
}

func (r *mediaLibraryReconcile) LoadDesiredState() error {
	mediaLibrary := &feddemadevv1alpha1.MediaLibrary{}
	mediaLibrary.Name = r.library.Name
	mediaLibrary.Namespace = r.library.Namespace
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(&r.library), &r.library); client.IgnoreNotFound(err) != nil {
		r.log.Error(err, "unable to fetch mediaLibrary")
		return err
	}
	r.log.Info("Desired state loaded")
	return nil
}

func (r *mediaLibraryReconcile) reconcileMainPvc() error {
	// If a pre-existing PVC is specified, skip creation
	if r.library.Spec.PVC.PVCName != nil {
		r.log.Info("Using pre-existing PVC", "pvcName", *r.library.Spec.PVC.PVCName)

		// Check if the PVC exists
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(r.ctx, client.ObjectKey{Namespace: r.library.Namespace, Name: *r.library.Spec.PVC.PVCName}, pvc)

		if err != nil {
			r.log.Error(err, "unable to fetch pre-existing PVC", "pvcName", *r.library.Spec.PVC.PVCName)

			cond := metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionFalse,
				Reason:  "ExistingPVCNotFound",
				Message: "The specified pre-existing PVC '" + *r.library.Spec.PVC.PVCName + "' was not found",
			}
			utils.MergeConditions(&r.library.Status.Conditions, cond)
			updateErr := r.Status().Update(r.ctx, &r.library)
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
				Reason:  "ExistingPVCInvalid",
				Message: "The specified pre-existing PVC '" + *r.library.Spec.PVC.PVCName + "' does not have ReadWriteMany access mode",
			}
			utils.MergeConditions(&r.library.Status.Conditions, cond)
			updateErr := r.Status().Update(r.ctx, &r.library)
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
			Message: "The specified pre-existing PVC '" + *r.library.Spec.PVC.PVCName + "' was found",
		}
		utils.MergeConditions(&r.library.Status.Conditions, cond)
		r.library.Status.EffectivePVC = r.library.Spec.PVC.PVCName
		updateErr := r.Status().Update(r.ctx, &r.library)
		if updateErr != nil {
			r.log.Error(updateErr, "unable to update MediaLibrary status")
			return updateErr
		}
		return nil
	}

	r.log.Info("Creating or patching PVC", "pvcName", r.library.Name)
	pvc := &corev1.PersistentVolumeClaim{}
	pvc.Name = r.library.Name
	pvc.Namespace = r.library.Namespace

	opResult, err := controllerutil.CreateOrPatch(r.ctx, r.Client, pvc, func() error {
		if err := ctrl.SetControllerReference(&r.library, pvc, r.Scheme); err != nil {
			return errors.Join(err, errors.New("unable to set controller owner reference for PVC"))
		}

		r.log.Info("Checking storage class", "pvcStorageClass", pvc.Spec.StorageClassName, "libraryStorageClass", r.library.Spec.PVC.StorageClassName)
		storageClass := cmp.Or(pvc.Spec.StorageClassName, r.library.Spec.PVC.StorageClassName)
		pvc.Spec = corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{"storage": r.library.Spec.PVC.Size}},
			StorageClassName: storageClass,
		}
		pvc.Annotations = mergeMap(pvc.Annotations, r.library.Spec.PVC.Annotations)

		return nil
	})
	if err != nil {
		return errors.Join(err, errors.New(fmt.Sprintf("unable to create or patch PVC with status %s", opResult)))
	}

	cond := metav1.Condition{
		Type:    "Available",
		Status:  metav1.ConditionTrue,
		Reason:  "PVCCreated",
		Message: fmt.Sprintf("The PVC '%s' was successfully created", pvc.Name),
	}
	utils.MergeConditions(&r.library.Status.Conditions, cond)
	r.log.Info("Condition set", "condition", &r.library.Status.Conditions)
	r.library.Status.EffectivePVC = &pvc.Name
	err = r.Status().Update(r.ctx, &r.library)
	if err != nil {
		r.log.Error(err, "unable to update MediaLibrary status")
		return err
	}
	return nil
}
