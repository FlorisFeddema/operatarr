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
	"time"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	testSonarrImage = "sonarr:test"
	testGatewayName = "test-gateway"
)

var _ = Describe("Sonarr HTTP route validation", func() {
	DescribeTable("validates the hostname against whether routing is enabled",
		func(enabled bool, hostname string, valid bool) {
			ctx := context.Background()
			sonarr := &feddemadevv1alpha1.Sonarr{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sonarr-validation-",
					Namespace:    defaultNamespace,
				},
				Spec: feddemadevv1alpha1.SonarrSpec{
					PodSpec: feddemadevv1alpha1.SonarrPodTemplateSpec{
						Image: testSonarrImage,
						ConfigVolumeSpec: &feddemadevv1alpha1.ConfigVolumeSpec{
							Size: resource.MustParse("1Gi"),
						},
					},
					HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{
						Enabled:   enabled,
						Hostname:  hostname,
						ParentRef: gatewayv1.ParentReference{Name: testGatewayName},
					},
					MediaLibraryRef: corev1.ObjectReference{
						Kind: mediaLibraryKind,
						Name: "test-library",
					},
				},
			}

			err := k8sClient.Create(ctx, sonarr)
			if err == nil {
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed())
				})
			}
			if valid {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("hostname must be set if httpRouteSpec.enabled is true")))
			}
		},
		Entry("accepts an enabled route with a hostname", true, "sonarr.example.com", true),
		Entry("rejects an enabled route without a hostname", true, "", false),
		Entry("accepts a disabled route without a hostname", false, "", true),
		Entry("accepts a disabled route with a hostname", false, "sonarr.example.com", true),
	)
})

var _ = Describe("Sonarr MediaLibrary watch mapping", func() {
	It("enqueues Sonarr resources that reference the changed MediaLibrary", func() {
		ctx := context.Background()
		mediaLibrary := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: "watched-library", Namespace: defaultNamespace},
		}

		createSonarr := func(name, libraryName string) *feddemadevv1alpha1.Sonarr {
			sonarr := &feddemadevv1alpha1.Sonarr{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: defaultNamespace},
				Spec: feddemadevv1alpha1.SonarrSpec{
					PodSpec: feddemadevv1alpha1.SonarrPodTemplateSpec{
						Image: testSonarrImage,
						ConfigVolumeSpec: &feddemadevv1alpha1.ConfigVolumeSpec{
							Size: resource.MustParse("1Gi"),
						},
					},
					HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{
						Enabled:   false,
						ParentRef: gatewayv1.ParentReference{Name: testGatewayName},
					},
					MediaLibraryRef: corev1.ObjectReference{
						Kind:      mediaLibraryKind,
						Name:      libraryName,
						Namespace: defaultNamespace,
					},
				},
			}
			Expect(k8sClient.Create(ctx, sonarr)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed())
			})
			return sonarr
		}

		referencedSonarr := createSonarr("sonarr-references-watched-library", mediaLibrary.Name)
		createSonarr("sonarr-references-another-library", "another-library")

		reconciler := &SonarrReconciler{Client: k8sClient}
		requests := reconciler.mapMediaLibraryToSonarrs(ctx, mediaLibrary)
		Expect(requests).To(ConsistOf(reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(referencedSonarr),
		}))
	})
})

var _ = Describe("Sonarr dependency reconciliation", func() {
	ctx := context.Background()

	newSonarr := func(name, mediaLibraryName string, routeEnabled bool) *feddemadevv1alpha1.Sonarr {
		return &feddemadevv1alpha1.Sonarr{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: defaultNamespace},
			Spec: feddemadevv1alpha1.SonarrSpec{
				PodSpec: feddemadevv1alpha1.SonarrPodTemplateSpec{
					Image: testSonarrImage,
					ConfigVolumeSpec: &feddemadevv1alpha1.ConfigVolumeSpec{
						Size: resource.MustParse("1Gi"),
					},
				},
				HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{
					Enabled:   routeEnabled,
					Hostname:  "sonarr.example.com",
					ParentRef: gatewayv1.ParentReference{Name: testGatewayName},
				},
				MediaLibraryRef: corev1.ObjectReference{
					Kind: mediaLibraryKind,
					Name: mediaLibraryName,
				},
			},
		}
	}

	It("records an unavailable condition and retries when the MediaLibrary is missing", func() {
		sonarr := newSonarr("sonarr-missing-library", "missing-library", false)
		Expect(k8sClient.Create(ctx, sonarr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed()) })

		reconciler := &SonarrReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)}
		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).To(MatchError(ContainSubstring("unable to get MediaLibrary from reference")))

		updated := &feddemadevv1alpha1.Sonarr{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sonarr), updated)).To(Succeed())
		condition := getAvailableCondition(updated.Status.Conditions)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("MediaLibraryUnavailable"))
		Expect(condition.Message).To(ContainSubstring("not found"))
	})

	It("persists finalizer removal before deleting Sonarr", func() {
		sonarr := newSonarr("sonarr-finalizer-removal", "missing-library", false)
		Expect(k8sClient.Create(ctx, sonarr)).To(Succeed())

		reconciler := &SonarrReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)}
		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		updated := &feddemadevv1alpha1.Sonarr{}
		Expect(k8sClient.Get(ctx, request.NamespacedName, updated)).To(Succeed())
		Expect(updated.Finalizers).To(ContainElement(finalizerName))
		Expect(k8sClient.Delete(ctx, updated)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, request.NamespacedName, &feddemadevv1alpha1.Sonarr{}))
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(BeTrue())
	})

	It("preserves allocated Service fields without patching unchanged Services", func() {
		sonarr := newSonarr("sonarr-service-idempotency", "unused-library", false)
		Expect(k8sClient.Create(ctx, sonarr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed()) })

		r := &sonarrReconcile{
			SonarrReconciler: SonarrReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()},
			ctx:              ctx,
			log:              logr.Discard(),
			object:           *sonarr,
		}
		for _, step := range []struct {
			name string
			run  func() error
		}{
			{name: sonarr.Name, run: r.reconcileService},
			{name: getHeadlessServiceName(sonarr.Name), run: r.reconcileHeadlessService},
		} {
			Expect(step.run()).To(Succeed())
			before := &corev1.Service{}
			key := client.ObjectKey{Name: step.name, Namespace: sonarr.Namespace}
			Expect(k8sClient.Get(ctx, key, before)).To(Succeed())
			Expect(before.Spec.ClusterIP).NotTo(BeEmpty())

			Expect(step.run()).To(Succeed())
			after := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
			Expect(after.Spec.ClusterIPs).To(Equal(before.Spec.ClusterIPs))
			Expect(after.Spec.ClusterIP).To(Equal(before.Spec.ClusterIP))
		}
	})

	It("continues reconciling Sonarr resources when Gateway API is unavailable", func() {
		const libraryName = "sonarr-gateway-library"
		pvcName := "sonarr-gateway-pvc"
		library := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: libraryName, Namespace: defaultNamespace},
			Spec: feddemadevv1alpha1.MediaLibrarySpec{
				CrossNamespaceAccess: true,
				PVC:                  feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName},
			},
			Status: feddemadevv1alpha1.MediaLibraryStatus{
				EffectivePVC: &pvcName,
				Initialized:  true,
			},
		}
		Expect(k8sClient.Create(ctx, library)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(library), library)).To(Succeed())
		library.Status = feddemadevv1alpha1.MediaLibraryStatus{
			EffectivePVC: &pvcName,
			Initialized:  true,
		}
		Expect(k8sClient.Status().Update(ctx, library)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(library), library)).To(Succeed())
		Expect(library.Status.Initialized).To(BeTrue())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, library)).To(Succeed()) })

		sonarr := newSonarr("sonarr-without-gateway-api", libraryName, true)
		Expect(k8sClient.Create(ctx, sonarr)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed()) })

		reconciler := &SonarrReconciler{
			Client:              k8sClient,
			Scheme:              k8sClient.Scheme(),
			GatewayAPIAvailable: false,
		}
		request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(sonarr)}
		_, err := reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())

		service := &corev1.Service{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sonarr), service)).To(Succeed())
		statefulSet := &appsv1.StatefulSet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sonarr), statefulSet)).To(Succeed())

		updated := &feddemadevv1alpha1.Sonarr{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sonarr), updated)).To(Succeed())
		condition := getAvailableCondition(updated.Status.Conditions)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		resourceVersion := updated.ResourceVersion

		_, err = reconciler.Reconcile(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sonarr), updated)).To(Succeed())
		Expect(updated.ResourceVersion).To(Equal(resourceVersion))
	})
})
