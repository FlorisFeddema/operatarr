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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
)

const (
	sonarrFinalizer = "sonarr.operatarr.feddema.dev/finalizer"

	typeAvailableSonarr = "Available"
	typeDegradedSonarr  = "Degraded"
)

// SonarrReconciler reconciles a Sonarr object
type SonarrReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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
	log := log.FromContext(ctx)
	sonarr := &feddemadevv1alpha1.Sonarr{}

	// fetch the Sonarr instance, and if it doesn't exist, return and stop reconciliation
	if err := r.Get(ctx, req.NamespacedName, sonarr); err != nil {
		if errors.IsNotFound(err) {
			log.Info("sonarr instance not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}

		log.Error(err, "failed to get sonarr")
		return ctrl.Result{}, err
	}

	if sonarr.Status.Conditions == nil || len(sonarr.Status.Conditions) == 0 {
		meta.SetStatusCondition(&sonarr.Status.Conditions, metav1.Condition{Type: typeAvailableSonarr, Status: metav1.ConditionUnknown, Reason: "Reconciliation", Message: "Starting reconciliation"})

		if err := r.Status().Update(ctx, sonarr); err != nil {
			log.Error(err, "unable to update Sonarr status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, sonarr); err != nil {
			log.Error(err, "failed to re-fetch sonarr")
			return ctrl.Result{}, err
		}
	}

	if !controllerutil.ContainsFinalizer(sonarr, sonarrFinalizer) {
		log.Info("adding finalizer to sonarr")
		if ok := controllerutil.AddFinalizer(sonarr, sonarrFinalizer); !ok {
			log.Info("failed to add finalizer to sonarr")
			return ctrl.Result{Requeue: true}, nil
		}

		if err := r.Update(ctx, sonarr); err != nil {
			log.Error(err, "failed to update sonarr with finalizer")
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, req.NamespacedName, sonarr); err != nil {
			log.Error(err, "failed to re-fetch sonarr")
			return ctrl.Result{}, err
		}
	}

	// check if the object is being deleted, and if so, do nothing and stop reconciliation
	if sonarr.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, nil
	}

	//TODO: Implement the logic to reconcile the state of the Sonarr object

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SonarrReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&feddemadevv1alpha1.Sonarr{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}
