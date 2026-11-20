package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	lg "sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	sonarrKind             = "Sonarr"
	failureFinalizerUpdate = "finalizer-update"
	testSonarrName         = "sonarr"
	testLibraryName        = "library"
)

func errorTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		feddemadevv1alpha1.AddToScheme, corev1.AddToScheme, batchv1.AddToScheme, gatewayv1.Install,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func TestSonarrServiceOperationLogging(t *testing.T) {
	scheme := errorTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	r := &sonarrReconcile{
		SonarrReconciler: SonarrReconciler{Client: c, Scheme: scheme},
		ctx:              context.Background(), log: log,
		object: feddemadevv1alpha1.Sonarr{
			ObjectMeta: metav1.ObjectMeta{Name: testSonarrName, Namespace: defaultNamespace},
		},
	}
	for range 2 {
		if err := r.reconcileService(); err != nil {
			t.Fatal(err)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "Resource reconciled") || !strings.Contains(logs[0], "created") {
		t.Fatalf("expected only the actual creation to be logged, got %v", logs)
	}
	createErr := errors.New("create rejected")
	r.Client = interceptor.NewClient(c, interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return createErr
		},
	})
	r.object.Name = "another-sonarr"
	err := r.reconcileService()
	if !errors.Is(err, createErr) || !strings.Contains(err.Error(), "Service default/another-sonarr") {
		t.Fatalf("expected contextual creation failure, got %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("failed create must not emit a success/error log: %v", logs)
	}
}

func TestSonarrHTTPRouteErrorContext(t *testing.T) {
	scheme := errorTestScheme(t)
	createErr := errors.New("route create rejected")
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return createErr
		},
	}).Build()
	r := &sonarrReconcile{
		SonarrReconciler: SonarrReconciler{Client: c, Scheme: scheme, GatewayAPIAvailable: true},
		ctx:              context.Background(), log: logr.Discard(),
		object: feddemadevv1alpha1.Sonarr{
			ObjectMeta: metav1.ObjectMeta{Name: testSonarrName, Namespace: defaultNamespace},
			Spec: feddemadevv1alpha1.SonarrSpec{
				HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{Enabled: true},
			},
		},
	}
	err := r.reconcileHttpRoute()
	if !errors.Is(err, createErr) || !strings.Contains(err.Error(), "HTTPRoute default/sonarr") || strings.Contains(err.Error(), "Service") {
		t.Fatalf("expected HTTPRoute error rather than misleading Service context, got %v", err)
	}
}

func testSonarrWithOwnedRoute() (*feddemadevv1alpha1.Sonarr, *gatewayv1.HTTPRoute) {
	sonarr := &feddemadevv1alpha1.Sonarr{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSonarrName, Namespace: defaultNamespace, UID: "sonarr-uid",
			Finalizers: []string{finalizerName},
		},
		Spec: feddemadevv1alpha1.SonarrSpec{
			MediaLibraryRef: corev1.ObjectReference{Kind: mediaLibraryKind, Name: "missing-library"},
			HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{
				Hostname: "sonarr.example.com", ParentRef: gatewayv1.ParentReference{Name: "gateway"},
			},
		},
	}
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: sonarr.Name, Namespace: sonarr.Namespace, UID: "route-uid",
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(sonarr, feddemadevv1alpha1.GroupVersion.WithKind(sonarrKind)),
			},
		},
	}
	return sonarr, route
}

func TestSonarrDisabledHTTPRouteDeletion(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr, route := testSonarrWithOwnedRoute()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(route).Build()
	var logs []string
	r := &sonarrReconcile{
		SonarrReconciler: SonarrReconciler{Client: c, Scheme: scheme, GatewayAPIAvailable: true},
		ctx:              context.Background(),
		log:              funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{}),
		object:           *sonarr,
	}
	for range 2 {
		if err := r.reconcileHttpRoute(); err != nil {
			t.Fatalf("disabled route reconciliation failed: %v", err)
		}
	}
	if err := c.Get(r.ctx, client.ObjectKeyFromObject(route), &gatewayv1.HTTPRoute{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expected the owned route to be deleted, got %v", err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "Resource deletion requested") {
		t.Fatalf("expected exactly one deletion log, got %v", logs)
	}

	r.object.Spec.HttpRouteSpec.Enabled = true
	if err := r.reconcileHttpRoute(); err != nil {
		t.Fatalf("re-enabling the route failed: %v", err)
	}
	recreated := &gatewayv1.HTTPRoute{}
	if err := c.Get(r.ctx, client.ObjectKeyFromObject(route), recreated); err != nil {
		t.Fatalf("expected route to be recreated: %v", err)
	}
	if !metav1.IsControlledBy(recreated, sonarr) {
		t.Fatal("recreated route must be controlled by Sonarr")
	}
}

func TestSonarrDisabledHTTPRouteDoesNotDeleteUnownedRoute(t *testing.T) {
	for _, owner := range []string{"none", "another-sonarr"} {
		t.Run(owner, func(t *testing.T) {
			scheme := errorTestScheme(t)
			sonarr, route := testSonarrWithOwnedRoute()
			if owner == "none" {
				route.OwnerReferences = nil
			} else {
				route.OwnerReferences[0].UID = "another-sonarr-uid"
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(route).Build()
			r := &sonarrReconcile{
				SonarrReconciler: SonarrReconciler{Client: c, Scheme: scheme, GatewayAPIAvailable: true},
				ctx:              context.Background(), log: logr.Discard(), object: *sonarr,
			}
			if err := r.reconcileHttpRoute(); err == nil || !strings.Contains(err.Error(), "not controlled by Sonarr") {
				t.Fatalf("expected ownership error, got %v", err)
			}
			if err := c.Get(r.ctx, client.ObjectKeyFromObject(route), &gatewayv1.HTTPRoute{}); err != nil {
				t.Fatalf("unowned route must remain: %v", err)
			}
		})
	}
}

func TestSonarrDisabledHTTPRouteErrorsAndRetry(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr, route := testSonarrWithOwnedRoute()
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(route).Build()
	getErr := errors.New("route read rejected")
	r := &sonarrReconcile{
		SonarrReconciler: SonarrReconciler{Scheme: scheme, GatewayAPIAvailable: true},
		ctx:              context.Background(), log: logr.Discard(), object: *sonarr,
	}
	r.Client = interceptor.NewClient(base, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return getErr
		},
	})
	if err := r.reconcileHttpRoute(); !errors.Is(err, getErr) || !strings.Contains(err.Error(), "HTTPRoute default/sonarr") {
		t.Fatalf("expected contextual read error, got %v", err)
	}

	r.Client = interceptor.NewClient(base, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, object client.Object, options ...client.DeleteOption) error {
			current := &gatewayv1.HTTPRoute{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(object), current); err != nil {
				return err
			}
			current.Annotations = map[string]string{"changed": "true"}
			if err := c.Update(ctx, current); err != nil {
				return err
			}
			return c.Delete(ctx, object, options...)
		},
	})
	if err := r.reconcileHttpRoute(); !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "HTTPRoute default/sonarr") {
		t.Fatalf("expected contextual delete conflict, got %v", err)
	}
	if err := base.Get(r.ctx, client.ObjectKeyFromObject(route), &gatewayv1.HTTPRoute{}); err != nil {
		t.Fatalf("changed route must remain after conflict: %v", err)
	}
	r.Client = base
	if err := r.reconcileHttpRoute(); err != nil {
		t.Fatalf("deletion should succeed on retry: %v", err)
	}
}

func TestSonarrDisabledHTTPRouteDeletedWhenMediaLibraryMissing(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr, route := testSonarrWithOwnedRoute()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sonarr, route).
		WithStatusSubresource(sonarr).Build()
	reconciler := &SonarrReconciler{Client: c, Scheme: scheme, GatewayAPIAvailable: true}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected the missing MediaLibrary error, got %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(route), &gatewayv1.HTTPRoute{}); !apierrors.IsNotFound(err) {
		t.Fatalf("route must be removed even when the MediaLibrary is missing, got %v", err)
	}
}

func TestSonarrDisabledHTTPRouteDeletionAndStatusFailure(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr, route := testSonarrWithOwnedRoute()
	deleteErr := errors.New("route deletion rejected")
	statusErr := errors.New("status update rejected")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sonarr, route).
		WithStatusSubresource(sonarr).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return deleteErr
		},
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return statusErr
		},
	}).Build()
	reconciler := &SonarrReconciler{Client: c, Scheme: scheme, GatewayAPIAvailable: true}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)})
	if !errors.Is(err, deleteErr) || !errors.Is(err, statusErr) || !strings.Contains(err.Error(), "HTTPRoute") {
		t.Fatalf("expected the deletion and status causes, got %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(route), &gatewayv1.HTTPRoute{}); err != nil {
		t.Fatalf("route must remain after failed deletion: %v", err)
	}
}

func TestSonarrHTTPRouteSkippedWithoutGatewayAPI(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr, _ := testSonarrWithOwnedRoute()
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			t.Fatal("must not access HTTPRoutes when Gateway API is unavailable")
			return nil
		},
	}).Build()
	r := &sonarrReconcile{
		SonarrReconciler: SonarrReconciler{Client: c, Scheme: scheme},
		ctx:              context.Background(), log: logr.Discard(), object: *sonarr,
	}
	for _, enabled := range []bool{false, true} {
		r.object.Spec.HttpRouteSpec.Enabled = enabled
		if err := r.reconcileHttpRoute(); err != nil {
			t.Fatalf("unavailable Gateway API should be skipped: %v", err)
		}
	}
}

func TestSonarrStepAndStatusErrors(t *testing.T) {
	scheme := errorTestScheme(t)
	sonarr := &feddemadevv1alpha1.Sonarr{
		ObjectMeta: metav1.ObjectMeta{Name: testSonarrName, Namespace: defaultNamespace, Finalizers: []string{finalizerName}},
		Spec: feddemadevv1alpha1.SonarrSpec{
			MediaLibraryRef: corev1.ObjectReference{Kind: mediaLibraryKind, Name: testLibraryName},
		},
	}
	library := &feddemadevv1alpha1.MediaLibrary{
		ObjectMeta: metav1.ObjectMeta{Name: testLibraryName, Namespace: defaultNamespace},
		Status:     feddemadevv1alpha1.MediaLibraryStatus{Initialized: true},
	}
	stepErr := errors.New("service create rejected")
	statusErr := errors.New("status update rejected")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sonarr, library).
		WithStatusSubresource(sonarr, library).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return stepErr
		},
		SubResourceUpdate: func(_ context.Context, _ client.Client, _ string, object client.Object, _ ...client.SubResourceUpdateOption) error {
			updated, ok := object.(*feddemadevv1alpha1.Sonarr)
			if !ok || len(updated.Status.Conditions) != 1 || updated.Status.Conditions[0].Reason != "ReconciliationFailed" {
				t.Fatalf("expected failure condition, got %#v", object)
			}
			return statusErr
		},
	}).Build()
	_, err := (&SonarrReconciler{Client: c, Scheme: scheme}).Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKeyFromObject(sonarr),
	})
	if !errors.Is(err, stepErr) || !errors.Is(err, statusErr) || !strings.Contains(err.Error(), "headless Service") {
		t.Fatalf("expected step context and both causes, got %v", err)
	}
}

func TestWatchMappingLogsUnreturnableErrors(t *testing.T) {
	scheme := errorTestScheme(t)
	listErr := errors.New("list rejected")
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return listErr
		},
	}).Build()
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	ctx := lg.IntoContext(context.Background(), log)
	library := &feddemadevv1alpha1.MediaLibrary{ObjectMeta: metav1.ObjectMeta{Name: testLibraryName, Namespace: defaultNamespace}}
	requests := (&SonarrReconciler{Client: c}).mapMediaLibraryToSonarrs(ctx, library)
	if len(requests) != 0 || len(logs) != 1 || !strings.Contains(logs[0], "list rejected") ||
		!strings.Contains(logs[0], `"mediaLibrary"`) {
		t.Fatalf("expected a single contextual watch error log, got %v, %v", requests, logs)
	}
}

func TestReconcilePrimaryErrors(t *testing.T) {
	for _, kind := range []string{sonarrKind, mediaLibraryKind} {
		for _, failure := range []string{"not-found", "forbidden", failureFinalizerUpdate} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				scheme := errorTestScheme(t)
				var object client.Object
				if kind == sonarrKind {
					object = &feddemadevv1alpha1.Sonarr{}
				} else {
					object = &feddemadevv1alpha1.MediaLibrary{}
				}
				object.SetName("test")
				object.SetNamespace(defaultNamespace)
				injected := errors.New("API request rejected")
				funcs := interceptor.Funcs{}
				if failure == "forbidden" {
					funcs.Get = func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return injected
					}
				}
				if failure == failureFinalizerUpdate {
					funcs.Update = func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
						return injected
					}
				}
				builder := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(funcs)
				if failure == failureFinalizerUpdate {
					builder.WithObjects(object)
				}
				c := builder.Build()
				var logs []string
				log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
				ctx := lg.IntoContext(context.Background(), log)
				req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(object)}
				var result ctrl.Result
				var err error
				if kind == sonarrKind {
					result, err = (&SonarrReconciler{Client: c, Scheme: scheme}).Reconcile(ctx, req)
				} else {
					result, err = (&MediaLibraryReconciler{Client: c, Scheme: scheme}).Reconcile(ctx, req)
				}
				if result != (ctrl.Result{}) {
					t.Fatalf("expected empty result, got %v", result)
				}
				if failure == "not-found" {
					if err != nil {
						t.Fatalf("deleted primary should not retry: %v", err)
					}
				} else if !errors.Is(err, injected) || !strings.Contains(err.Error(), kind) {
					t.Fatalf("expected contextual preserved cause, got %v", err)
				}
				if len(logs) != 0 {
					t.Fatalf("returned errors and routine reconciles must not emit info/error logs: %v", logs)
				}
			})
		}
	}
}

func TestMediaLibraryFinalizerUpdateFailure(t *testing.T) {
	scheme := errorTestScheme(t)
	deletedAt := metav1.Now()
	library := &feddemadevv1alpha1.MediaLibrary{
		ObjectMeta: metav1.ObjectMeta{
			Name: testLibraryName, Namespace: defaultNamespace,
			Finalizers: []string{finalizerName}, DeletionTimestamp: &deletedAt,
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(library).Build()
	updateErr := errors.New("finalizer update rejected")
	failingClient := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return updateErr
		},
	})
	reconciler := &MediaLibraryReconciler{Client: failingClient, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(library)}

	_, err := reconciler.Reconcile(context.Background(), req)
	if !errors.Is(err, updateErr) || !strings.Contains(err.Error(), "MediaLibrary finalizer") {
		t.Fatalf("expected contextual finalizer update error, got %v", err)
	}
	current := &feddemadevv1alpha1.MediaLibrary{}
	if err := base.Get(context.Background(), req.NamespacedName, current); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(current, finalizerName) {
		t.Fatal("failed update must leave the persisted finalizer intact")
	}

	reconciler.Client = base
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("expected finalization to succeed on retry: %v", err)
	}
	if err := base.Get(context.Background(), req.NamespacedName, current); !apierrors.IsNotFound(err) {
		t.Fatalf("expected the finalized MediaLibrary to be deleted, got %v", err)
	}
}

func TestSonarrFinalizerUpdateFailure(t *testing.T) {
	scheme := errorTestScheme(t)
	deletedAt := metav1.Now()
	sonarr := &feddemadevv1alpha1.Sonarr{
		ObjectMeta: metav1.ObjectMeta{
			Name: testSonarrName, Namespace: defaultNamespace,
			Finalizers: []string{finalizerName}, DeletionTimestamp: &deletedAt,
		},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sonarr).Build()
	updateErr := errors.New("finalizer update rejected")
	failingClient := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			return updateErr
		},
	})
	reconciler := &SonarrReconciler{Client: failingClient, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)}

	_, err := reconciler.Reconcile(context.Background(), req)
	if !errors.Is(err, updateErr) || !strings.Contains(err.Error(), "Sonarr finalizer") {
		t.Fatalf("expected contextual finalizer update error, got %v", err)
	}
	current := &feddemadevv1alpha1.Sonarr{}
	if err := base.Get(context.Background(), req.NamespacedName, current); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(current, finalizerName) {
		t.Fatal("failed update must leave the persisted finalizer intact")
	}
}

func TestManagedPVCInitializationAndIdempotency(t *testing.T) {
	scheme := errorTestScheme(t)
	library := &feddemadevv1alpha1.MediaLibrary{
		ObjectMeta: metav1.ObjectMeta{Name: testLibraryName, Namespace: defaultNamespace, Finalizers: []string{finalizerName}},
		Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi")}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(library).WithStatusSubresource(library).Build()
	reconciler := &MediaLibraryReconciler{Client: c, Scheme: scheme, JobRunnerImage: testJobRunnerImage}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(library)}
	ctx := context.Background()

	if _, err := reconciler.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	job := &batchv1.Job{}
	jobKey := client.ObjectKey{Name: testLibraryName + "-pvc-init", Namespace: defaultNamespace}
	if err := c.Get(ctx, jobKey, job); err != nil {
		t.Fatalf("expected the initialization Job after PVC creation: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, req.NamespacedName, pvc); err != nil {
		t.Fatal(err)
	}
	volumeMode := corev1.PersistentVolumeFilesystem
	pvc.Spec.VolumeName = "bound-volume"
	pvc.Spec.VolumeMode = &volumeMode
	if err := c.Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	pvcVersion, jobVersion := pvc.ResourceVersion, job.ResourceVersion

	if _, err := reconciler.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, req.NamespacedName, pvc); err != nil {
		t.Fatal(err)
	}
	if pvc.Spec.VolumeName != "bound-volume" || pvc.Spec.VolumeMode == nil || *pvc.Spec.VolumeMode != volumeMode {
		t.Fatalf("reconciliation removed API-assigned PVC fields: %#v", pvc.Spec)
	}
	if err := c.Get(ctx, jobKey, job); err != nil {
		t.Fatal(err)
	}
	if pvc.ResourceVersion != pvcVersion || job.ResourceVersion != jobVersion {
		t.Fatalf("unchanged PVC and Job should not be updated: PVC %s -> %s, Job %s -> %s",
			pvcVersion, pvc.ResourceVersion, jobVersion, job.ResourceVersion)
	}
}

func TestManagedPVCStatusFailureCanRetry(t *testing.T) {
	scheme := errorTestScheme(t)
	library := &feddemadevv1alpha1.MediaLibrary{
		ObjectMeta: metav1.ObjectMeta{Name: testLibraryName, Namespace: defaultNamespace, Finalizers: []string{finalizerName}},
		Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi")}},
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(library).WithStatusSubresource(library).Build()
	statusErr := apierrors.NewConflict(schema.GroupResource{Group: "feddema.dev", Resource: "medialibraries"}, testLibraryName,
		errors.New("status update rejected"))
	failingClient := interceptor.NewClient(base, interceptor.Funcs{
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return statusErr
		},
	})
	reconciler := &MediaLibraryReconciler{Client: failingClient, Scheme: scheme, JobRunnerImage: testJobRunnerImage}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(library)}
	ctx := context.Background()

	_, err := reconciler.Reconcile(ctx, req)
	if !errors.Is(err, statusErr) || !apierrors.IsConflict(err) {
		t.Fatalf("expected the status update failure, got %v", err)
	}
	jobKey := client.ObjectKey{Name: testLibraryName + "-pvc-init", Namespace: defaultNamespace}
	if err := base.Get(ctx, jobKey, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("must not create a Job before the PVC status is persisted, got %v", err)
	}

	reconciler.Client = base
	if _, err := reconciler.Reconcile(ctx, req); err != nil {
		t.Fatalf("expected reconciliation to recover after the status failure: %v", err)
	}
	current := &feddemadevv1alpha1.MediaLibrary{}
	if err := base.Get(ctx, req.NamespacedName, current); err != nil {
		t.Fatal(err)
	}
	if current.Status.EffectivePVC == nil || *current.Status.EffectivePVC != testLibraryName {
		t.Fatalf("expected effective PVC to be persisted on retry, got %#v", current.Status.EffectivePVC)
	}
	if err := base.Get(ctx, jobKey, &batchv1.Job{}); err != nil {
		t.Fatalf("expected initialization Job on retry: %v", err)
	}
}

func TestDependencyAndStatusErrorsPreserveBothCauses(t *testing.T) {
	for _, kind := range []string{sonarrKind, mediaLibraryKind} {
		t.Run(kind, func(t *testing.T) {
			scheme := errorTestScheme(t)
			pvcName := "missing-pvc"
			var object client.Object
			if kind == sonarrKind {
				object = &feddemadevv1alpha1.Sonarr{
					Spec: feddemadevv1alpha1.SonarrSpec{MediaLibraryRef: corev1.ObjectReference{Kind: mediaLibraryKind, Name: "missing-library"}},
				}
			} else {
				object = &feddemadevv1alpha1.MediaLibrary{
					Spec: feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
				}
			}
			object.SetName("test")
			object.SetNamespace(defaultNamespace)
			object.SetFinalizers([]string{finalizerName})
			statusErr := errors.New("status update rejected")
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object).
				WithStatusSubresource(object).WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
					return statusErr
				},
			}).Build()
			var logs []string
			log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
			ctx := lg.IntoContext(context.Background(), log)
			req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(object)}
			var err error
			if kind == sonarrKind {
				_, err = (&SonarrReconciler{Client: c, Scheme: scheme}).Reconcile(ctx, req)
			} else {
				_, err = (&MediaLibraryReconciler{Client: c, Scheme: scheme}).Reconcile(ctx, req)
			}
			if !apierrors.IsNotFound(err) || !errors.Is(err, statusErr) || !strings.Contains(err.Error(), "status") {
				t.Fatalf("expected dependency and status causes, got %v", err)
			}
			if len(logs) != 0 {
				t.Fatalf("framework owns logging returned errors: %v", logs)
			}
		})
	}
}

func TestFailedInitializationJobLogging(t *testing.T) {
	scheme := errorTestScheme(t)
	pvcName := testLibraryName
	object := &feddemadevv1alpha1.MediaLibrary{
		ObjectMeta: metav1.ObjectMeta{Name: testLibraryName, Namespace: defaultNamespace, Generation: 1},
		Status:     feddemadevv1alpha1.MediaLibraryStatus{EffectivePVC: &pvcName},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "library-pvc-init", Namespace: defaultNamespace},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "initializer failed",
		}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(object, job).WithStatusSubresource(object, job).Build()
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	r := &mediaLibraryReconcile{
		MediaLibraryReconciler: MediaLibraryReconciler{Client: c, Scheme: scheme},
		ctx:                    context.Background(), log: log,
	}
	for range 2 {
		if err := c.Get(r.ctx, client.ObjectKeyFromObject(object), &r.object); err != nil {
			t.Fatal(err)
		}
		if err := initializeJob(r); err != nil {
			t.Fatalf("failed job remains status-driven: %v", err)
		}
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "PVC initialization job failed") ||
		!strings.Contains(logs[0], `"job"`) || !strings.Contains(logs[0], "BackoffLimitExceeded") {
		t.Fatalf("expected one structured failed-job log, got %v", logs)
	}
	if r.object.Status.Initialized {
		t.Fatal("failed job must not mark library initialized")
	}
	statusErr := errors.New("status rejected")
	r.Client = interceptor.NewClient(c, interceptor.Funcs{
		SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
			return statusErr
		},
	})
	err := initializeJob(r)
	if !errors.Is(err, statusErr) || !strings.Contains(err.Error(), "initializer failed") {
		t.Fatalf("expected job failure and status error, got %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("returned status errors must not also be logged: %v", logs)
	}
}

func TestLogResourceOperation(t *testing.T) {
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	object := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: testSonarrName, Namespace: defaultNamespace}}
	logResourceOperation(log, "Service", object, controllerutil.OperationResultNone)
	logResourceOperation(log, "Service", object, controllerutil.OperationResultCreated)
	logResourceOperation(log, "Service", object, controllerutil.OperationResultUpdated)
	if len(logs) != 2 {
		t.Fatalf("only real changes should be logged, got %v", logs)
	}
	for _, output := range logs {
		for _, field := range []string{`"kind"`, `"resource"`, `"operation"`} {
			if !strings.Contains(output, field) {
				t.Fatalf("missing structured field %s: %s", field, output)
			}
		}
	}
}

func TestMissingEffectivePVCIsAnError(t *testing.T) {
	for _, name := range []*string{nil, new("")} {
		object := feddemadevv1alpha1.MediaLibrary{Status: feddemadevv1alpha1.MediaLibraryStatus{EffectivePVC: name}}
		_, err := getMediaLibraryPVC(nil, context.Background(), object)
		if err == nil || !strings.Contains(err.Error(), "effective PVC") {
			t.Fatalf("expected explicit missing PVC error, got %v", err)
		}
		if err := initializeJob(&mediaLibraryReconcile{object: object, log: logr.Discard()}); err == nil {
			t.Fatal("expected initialization to reject missing effective PVC")
		}
	}
}
