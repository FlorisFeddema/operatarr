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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	lg "sigs.k8s.io/controller-runtime/pkg/log"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
)

// LibraryReconciler reconciles a Library object
type LibraryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=feddema.dev,resources=libraries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feddema.dev,resources=libraries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=feddema.dev,resources=libraries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Library object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *LibraryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := lg.FromContext(ctx)
	library := &feddemadevv1alpha1.Library{}

	if err := r.Get(ctx, req.NamespacedName, library); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("library resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch library")
		return ctrl.Result{}, err
	}

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LibraryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.Library{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func (r *LibraryReconciler) pvcForLibrary(library *feddemadevv1alpha1.Library) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      library.Name,
			Namespace: library.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      library.Spec.AccessModes,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: library.Spec.Size}},
			StorageClassName: library.Spec.StorageClassName,
		},
	}

	if err := ctrl.SetControllerReference(library, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}
