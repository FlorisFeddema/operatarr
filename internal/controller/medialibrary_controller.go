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
	"fmt"
	controllerruntime "github.com/FlorisFeddema/operatarr/internal/controller/controller-runtime"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
)

const (
	mediaLibraryFinalizer = "medialibrary.operatarr.feddema.dev/finalizer"

	typeAvailableMediaLibrary = "Available"
	typeDegradedMediaLibrary  = "Degraded"
)

// MediaLibraryReconciler reconciles a MediaLibrary object
type MediaLibraryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=medialibraries/finalizers,verbs=update

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
	mediaLibrary := &feddemadevv1alpha1.MediaLibrary{}

	if err := r.Get(ctx, req.NamespacedName, mediaLibrary); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("mediaLibrary resource not found. Ignoring since object must be deleted")
			return controllerruntime.Success()
		}
		log.Error(err, "unable to fetch mediaLibrary")
		return controllerruntime.Fail(err)
	}

	if len(mediaLibrary.Status.Conditions) == 0 {
		meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeAvailableMediaLibrary, Status: metav1.ConditionUnknown, Message: "Starting reconciliation", Reason: "Reconciliation"})

		if err := r.Status().Update(ctx, mediaLibrary); err != nil {
			log.Error(err, "unable to update mediaLibrary status")
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Success()
	}

	if !controllerutil.ContainsFinalizer(mediaLibrary, mediaLibraryFinalizer) {
		log.Info("adding finalizer to the mediaLibrary")
		if ok := controllerutil.AddFinalizer(mediaLibrary, mediaLibraryFinalizer); !ok {
			log.Info("unable to add finalizer to the mediaLibrary")
			return controllerruntime.Fail(errors.New("unable to add finalizer to the mediaLibrary"))
		}
		if err := r.Update(ctx, mediaLibrary); err != nil {
			log.Error(err, "unable to update mediaLibrary with finalizer")
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Success()
	}

	if mediaLibrary.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(mediaLibrary, mediaLibraryFinalizer) {
			log.Info("performing finalization for the mediaLibrary before deletion")
			meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeDegradedMediaLibrary, Status: metav1.ConditionUnknown, Message: fmt.Sprintf("Finalizing %s before deletion", mediaLibrary.Name), Reason: "Finalization"})

			if err := r.Status().Update(ctx, mediaLibrary); err != nil {
				log.Error(err, "unable to update mediaLibrary status")
				return controllerruntime.Fail(err)
			}

			// TODO: perform finalization here

			if err := r.Get(ctx, req.NamespacedName, mediaLibrary); err != nil {
				log.Error(err, "unable to re-fetch mediaLibrary")
				return controllerruntime.Fail(err)
			}

			meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeDegradedMediaLibrary, Status: metav1.ConditionTrue, Message: fmt.Sprintf("Finalization of %s completed", mediaLibrary.Name), Reason: "Finalization"})
			if err := r.Status().Update(ctx, mediaLibrary); err != nil {
				log.Error(err, "unable to update mediaLibrary status")
				return controllerruntime.Fail(err)
			}

			log.Info("removing finalizer from the mediaLibrary")
			if ok := controllerutil.RemoveFinalizer(mediaLibrary, mediaLibraryFinalizer); !ok {
				log.Info("unable to remove finalizer from the mediaLibrary")
				return controllerruntime.Success()
			}

			if err := r.Update(ctx, mediaLibrary); err != nil {
				log.Error(err, "unable to remove finalizer from mediaLibrary")
				return controllerruntime.Fail(err)
			}
		}
		return controllerruntime.Success()
	}

	err := r.ensurePvc(ctx, log, mediaLibrary)
	if err != nil {
		meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeDegradedMediaLibrary, Status: metav1.ConditionTrue, Message: "pvc creation failed", Reason: "PvcCreationFailed"})
		if err := r.Status().Update(ctx, mediaLibrary); err != nil {
			log.Error(err, "unable to update mediaLibrary status")
			return controllerruntime.Fail(err)
		}
		return controllerruntime.Fail(err)
	}

	if err := r.Get(ctx, req.NamespacedName, mediaLibrary); err != nil {
		log.Error(err, "unable to re-fetch mediaLibrary")
		return controllerruntime.Fail(err)
	}

	meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeAvailableMediaLibrary, Status: metav1.ConditionTrue, Message: "Ready", Reason: "Ready"})
	meta.SetStatusCondition(&mediaLibrary.Status.Conditions, metav1.Condition{Type: typeDegradedMediaLibrary, Status: metav1.ConditionFalse, Message: "Healthy", Reason: "Healthy"})
	if err := r.Status().Update(ctx, mediaLibrary); err != nil {
		log.Error(err, "unable to update mediaLibrary status")
		return controllerruntime.Fail(err)
	}

	log.Info("media library is ready")

	return controllerruntime.Success()
}

// SetupWithManager sets up the controller with the Manager.
func (r *MediaLibraryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.MediaLibrary{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func (r *MediaLibraryReconciler) ensurePvc(ctx context.Context, log logr.Logger, library *feddemadevv1alpha1.MediaLibrary) error {
	desired := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        library.Name,
			Namespace:   library.Namespace,
			Annotations: library.Spec.Annotations,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      library.Spec.AccessModes,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: library.Spec.Size}},
			StorageClassName: library.Spec.StorageClassName,
		},
	}

	if err := ctrl.SetControllerReference(library, desired, r.Scheme); err != nil {
		return errors.WithMessage(err, "unable to set owner reference on PVC")
	}

	pvc := desired.DeepCopy()
	statusType, err := controllerutil.CreateOrPatch(ctx, r.Client, pvc, func() error {
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = library.Spec.Size
		setMergedLabelsAndAnnotations(pvc, desired)
		return nil
	})
	if err != nil {
		return errors.WithMessage(err, fmt.Sprintf("unable to create or patch PVC with status %s", statusType))
	}
	return nil
}
