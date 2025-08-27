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

	"github.com/FlorisFeddema/operatarr/internal/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"

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
	library *feddemadevv1alpha1.MediaLibrary
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
	} else {
		log.Info("MediaLibrary reconciliation completed successfully")
	}
	return ctrl.Result{}, err
}

func (r *mediaLibraryReconcile) reconcile() error {
	// Load the object desired state based on resource, operator config and default values.
	if err := r.LoadAndValidateDesiredState(); err != nil {
		return err
	}

	reconcilers := []func() error{
		r.reconcilePvc,
	}

	errChan := utils.RunConcurrently(reconcilers...)
	errList := utils.ChannelToSlice(errChan)

	if err := errors.Join(errList...); err != nil {
		return err
	}

	return nil
}

func (r *mediaLibraryReconcile) LoadAndValidateDesiredState() error {
	mediaLibrary := &feddemadevv1alpha1.MediaLibrary{}
	mediaLibrary.Name = r.library.Name
	mediaLibrary.Namespace = r.library.Namespace
	if err := r.Get(r.ctx, client.ObjectKeyFromObject(r.library), r.library); client.IgnoreNotFound(err) != nil {
		r.log.Error(err, "unable to fetch mediaLibrary")
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MediaLibraryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.MediaLibrary{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func (r *mediaLibraryReconcile) reconcilePvc() error {
	desired := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.library.Name,
			Namespace:   r.library.Namespace,
			Annotations: r.library.Spec.Annotations,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      r.library.Spec.AccessModes,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: r.library.Spec.Size}},
			StorageClassName: r.library.Spec.StorageClassName,
		},
	}

	if err := ctrl.SetControllerReference(r.library, desired, r.Scheme); err != nil {
		return errors.Join(err, errors.New("unable to set controller reference for PVC"))
	}

	pvc := desired.DeepCopy()
	statusType, err := controllerutil.CreateOrPatch(r.ctx, r.Client, pvc, func() error {
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = r.library.Spec.Size
		setMergedLabelsAndAnnotations(pvc, desired)
		return nil
	})
	if err != nil {
		return errors.Join(err, errors.New(fmt.Sprintf("unable to create or patch PVC with status %s", statusType)))
	}
	return nil
}
