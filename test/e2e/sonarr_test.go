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

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	feddemav1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/FlorisFeddema/operatarr/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	sonarrTestLibraryName = "sonarr-e2e-library"
	sonarrTestPVCName     = "sonarr-e2e-media"
	sonarrTestPort        = int32(8989)
	statefulSetKind       = "statefulset"
	serviceKind           = "service"
)

var gatewayAPICRDInstalled bool

func sonarrSpecs() {
	Context("Sonarr", Ordered, func() {
		It("reports a missing MediaLibrary and recovers when it appears", func() {
			sonarr := newTestSonarr("sonarr-missing-library", true)
			Expect(createTestObject(sonarr)).To(Succeed())
			expectSonarrCondition(sonarr.Name, metav1.ConditionFalse, "MediaLibraryUnavailable")

			By("creating a bound RWX PVC and the referenced MediaLibrary")
			Expect(createTestObject(newTestPVC(sonarrTestPVCName, corev1.ReadWriteMany))).To(Succeed())
			Expect(waitForPVCBound(sonarrTestPVCName)).To(Succeed())
			library := newTestLibrary(sonarrTestLibraryName, sonarrTestPVCName)
			library.Spec.CrossNamespaceAccess = true
			Expect(createTestObject(library)).To(Succeed())
			pvcName := sonarrTestPVCName
			expectLibraryStatus(sonarrTestLibraryName, metav1.ConditionTrue, "ExistingPVCFound", true, &pvcName)

			By("waiting for the MediaLibrary watch to reconcile Sonarr")
			expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")
		})

		It("creates owned resources when Gateway API is unavailable", func() {
			By("verifying the cluster has no HTTPRoute CRD")
			output, err := utils.Run(utils.KindKubectl(kubectlGet, "crd",
				"httproutes.gateway.networking.k8s.io", ignoreNotFoundFlag, "-o", "name"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(BeEmpty())

			sonarr := newTestSonarr("sonarr-without-gateway", true)
			Expect(createTestObject(sonarr)).To(Succeed())
			sonarr = expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")

			assertSonarrOwnedResources(sonarr)
			output, err = utils.Run(utils.KindKubectl(kubectlGet, "persistentvolume", sonarr.Name+"-media",
				ignoreNotFoundFlag, "-o", "name"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(BeEmpty())
			output, err = utils.Run(utils.KindKubectl(kubectlGet, "crd",
				"httproutes.gateway.networking.k8s.io", ignoreNotFoundFlag, "-o", "name"))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(BeEmpty())

			disabled := newTestSonarr("sonarr-disabled-without-gateway", false)
			Expect(createTestObject(disabled)).To(Succeed())
			expectSonarrCondition(disabled.Name, metav1.ConditionTrue, "ResourcesReconciled")
		})

		It("creates, deletes, and recreates HTTPRoutes as routing is changed", func() {
			By("installing the pinned Gateway API HTTPRoute CRD")
			Expect(installGatewayHTTPRouteCRD()).To(Succeed())

			By("restarting the manager to detect Gateway API")
			_, err := utils.Run(utils.KindKubectl("rollout", "restart",
				"deployment/"+managerDeployment, namespaceFlag, namespace))
			Expect(err).NotTo(HaveOccurred())
			_, err = utils.Run(utils.KindKubectl("rollout", "status",
				"deployment/"+managerDeployment, "--timeout=3m", namespaceFlag, namespace))
			Expect(err).NotTo(HaveOccurred())

			sonarr := newTestSonarr("sonarr-with-gateway", true)
			Expect(createTestObject(sonarr)).To(Succeed())
			sonarr = expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")

			var route gatewayv1.HTTPRoute
			Eventually(func(g Gomega) {
				g.Expect(getTestObject("httproute", sonarr.Name, &route)).To(Succeed())
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
			Expect(route.Spec.Hostnames).To(ConsistOf(gatewayv1.Hostname("sonarr.example.test")))
			Expect(route.Spec.ParentRefs).To(HaveLen(1))
			Expect(string(route.Spec.ParentRefs[0].Name)).To(Equal("e2e-gateway"))
			expectSonarrOwner(&route.ObjectMeta, sonarr)
			originalRouteUID := route.UID

			By("disabling the route while its MediaLibrary is missing")
			_, err = utils.Run(utils.KindKubectl("patch", "sonarr", sonarr.Name, "--type=merge",
				"-p", `{"spec":{"sonarrHttpRouteSpec":{"enabled":false},"mediaLibraryRef":{"name":"missing-library"}}}`,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			expectSonarrCondition(sonarr.Name, metav1.ConditionFalse, "MediaLibraryUnavailable")
			Eventually(func(g Gomega) {
				output, getErr := utils.Run(utils.KindKubectl(kubectlGet, "httproute", sonarr.Name,
					ignoreNotFoundFlag, "-o", "name", namespaceFlag, mediaNamespace))
				g.Expect(getErr).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(output))).To(BeEmpty())
			}, 2*time.Minute, 2*time.Second).Should(Succeed())

			By("restoring the MediaLibrary reference and re-enabling the route")
			_, err = utils.Run(utils.KindKubectl("patch", "sonarr", sonarr.Name, "--type=merge",
				"-p", `{"spec":{"sonarrHttpRouteSpec":{"enabled":true},"mediaLibraryRef":{"name":"`+
					sonarrTestLibraryName+`"}}}`, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			sonarr = expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")
			Eventually(func(g Gomega) {
				g.Expect(getTestObject("httproute", sonarr.Name, &route)).To(Succeed())
				g.Expect(route.Spec.Hostnames).To(ConsistOf(gatewayv1.Hostname("sonarr.example.test")))
				g.Expect(route.UID).NotTo(Equal(originalRouteUID))
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
			expectSonarrOwner(&route.ObjectMeta, sonarr)

			disabled := newTestSonarr("sonarr-disabled-route", false)
			Expect(createTestObject(disabled)).To(Succeed())
			expectSonarrCondition(disabled.Name, metav1.ConditionTrue, "ResourcesReconciled")
			output, err := utils.Run(utils.KindKubectl(kubectlGet, "httproute", disabled.Name,
				ignoreNotFoundFlag, "-o", "name", namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(BeEmpty())
		})

		It("rejects an enabled HTTPRoute without a hostname", func() {
			sonarr := newTestSonarr("sonarr-invalid-route", true)
			sonarr.Spec.HttpRouteSpec.Hostname = ""
			err := createTestObject(sonarr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("hostname must be set if httpRouteSpec.enabled is true"))
		})

		It("updates the StatefulSet when the Sonarr spec changes", func() {
			sonarr := newTestSonarr("sonarr-spec-update", false)
			Expect(createTestObject(sonarr)).To(Succeed())
			expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")

			patch := `{
				"spec": {
					"sonarrPodTemplateSpec": {
						"image": "example.com/sonarr:v2",
						"resources": {"requests": {"cpu": "100m", "memory": "128Mi"}}
					}
				}
			}`
			_, err := utils.Run(utils.KindKubectl("patch", "sonarr", sonarr.Name, "--type=merge",
				"-p", patch,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				var statefulSet appsv1.StatefulSet
				g.Expect(getTestObject("statefulset", sonarr.Name, &statefulSet)).To(Succeed())
				g.Expect(statefulSet.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := statefulSet.Spec.Template.Spec.Containers[0]
				g.Expect(container.Image).To(Equal("example.com/sonarr:v2"))
				g.Expect(container.Resources.Requests.Cpu().String()).To(Equal("100m"))
				g.Expect(container.Resources.Requests.Memory().String()).To(Equal("128Mi"))
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("restores a modified or deleted Service", func() {
			sonarr := newTestSonarr("sonarr-service-drift", false)
			Expect(createTestObject(sonarr)).To(Succeed())
			expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")

			By("changing the managed Service port")
			_, err := utils.Run(utils.KindKubectl("patch", "service", sonarr.Name, "--type=merge",
				"-p", `{"spec":{"ports":[{"name":"http","port":9999,"targetPort":9999}]}}`,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			expectSonarrServicePort(sonarr.Name, sonarrTestPort)

			By("deleting the managed Service")
			var original corev1.Service
			Expect(getTestObject("service", sonarr.Name, &original)).To(Succeed())
			_, err = utils.Run(utils.KindKubectl(kubectlDelete, "service", sonarr.Name, "--wait=false",
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				var service corev1.Service
				g.Expect(getTestObject("service", sonarr.Name, &service)).To(Succeed())
				g.Expect(service.UID).NotTo(Equal(original.UID))
				g.Expect(service.DeletionTimestamp).To(BeNil())
				g.Expect(service.Spec.Ports).To(HaveLen(1))
				g.Expect(service.Spec.Ports[0].Port).To(Equal(sonarrTestPort))
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("removes its finalizer and owned resources on deletion", func() {
			sonarr := newTestSonarr("sonarr-delete", false)
			Expect(createTestObject(sonarr)).To(Succeed())
			expectSonarrCondition(sonarr.Name, metav1.ConditionTrue, "ResourcesReconciled")

			var current feddemav1alpha1.Sonarr
			Expect(getTestObject("sonarr", sonarr.Name, &current)).To(Succeed())
			Expect(current.Finalizers).To(ContainElement(mediaLibraryFinalizer))
			assertSonarrOwnedResources(&current)

			By("deleting the Sonarr and waiting for Kubernetes garbage collection")
			Expect(deleteTestSonarr(sonarr.Name)).To(Succeed())
			Eventually(func(g Gomega) {
				for _, object := range []struct{ kind, name string }{
					{statefulSetKind, sonarr.Name},
					{serviceKind, sonarr.Name},
					{serviceKind, sonarr.Name + "-headless"},
				} {
					output, err := utils.Run(utils.KindKubectl(kubectlGet, object.kind, object.name,
						ignoreNotFoundFlag, "-o", "name", namespaceFlag, mediaNamespace))
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(strings.TrimSpace(string(output))).To(BeEmpty(),
						"%s %s should be garbage-collected", object.kind, object.name)
				}
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})
	})
}

func newTestSonarr(name string, routeEnabled bool) *feddemav1alpha1.Sonarr {
	return &feddemav1alpha1.Sonarr{
		TypeMeta:   metav1.TypeMeta{APIVersion: operatarrAPIVersion, Kind: "Sonarr"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mediaNamespace},
		Spec: feddemav1alpha1.SonarrSpec{
			PodSpec: feddemav1alpha1.SonarrPodTemplateSpec{
				Image:           "example.com/sonarr:e2e",
				ImagePullPolicy: corev1.PullNever,
				ConfigVolumeSpec: &feddemav1alpha1.ConfigVolumeSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Size:        resource.MustParse("128Mi"),
				},
			},
			HttpRouteSpec: feddemav1alpha1.SonarrHttpRouteSpec{
				Enabled:   routeEnabled,
				Hostname:  "sonarr.example.test",
				ParentRef: gatewayv1.ParentReference{Name: gatewayv1.ObjectName("e2e-gateway")},
			},
			MediaLibraryRef: corev1.ObjectReference{Kind: mediaLibraryKind, Name: sonarrTestLibraryName},
		},
	}
}

func expectSonarrCondition(name string, status metav1.ConditionStatus, reason string) *feddemav1alpha1.Sonarr {
	GinkgoHelper()
	var observed feddemav1alpha1.Sonarr
	Eventually(func(g Gomega) {
		var sonarr feddemav1alpha1.Sonarr
		g.Expect(getTestObject("sonarr", name, &sonarr)).To(Succeed())
		condition := meta.FindStatusCondition(sonarr.Status.Conditions, "Available")
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(status))
		g.Expect(condition.Reason).To(Equal(reason))
		g.Expect(condition.ObservedGeneration).To(Equal(sonarr.Generation))
		observed = sonarr
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
	return &observed
}

func assertSonarrOwnedResources(sonarr *feddemav1alpha1.Sonarr) {
	GinkgoHelper()
	for _, object := range []struct {
		kind string
		name string
	}{
		{statefulSetKind, sonarr.Name},
		{serviceKind, sonarr.Name},
		{serviceKind, sonarr.Name + "-headless"},
	} {
		var metadata metav1.ObjectMeta
		switch object.kind {
		case statefulSetKind:
			var statefulSet appsv1.StatefulSet
			Expect(getTestObject(object.kind, object.name, &statefulSet)).To(Succeed())
			metadata = statefulSet.ObjectMeta
			Expect(statefulSet.Spec.VolumeClaimTemplates).To(HaveLen(1))
			Expect(statefulSet.Spec.VolumeClaimTemplates[0].Name).To(Equal("config"))
		case serviceKind:
			var service corev1.Service
			Expect(getTestObject(object.kind, object.name, &service)).To(Succeed())
			metadata = service.ObjectMeta
			Expect(service.Spec.Ports).To(HaveLen(1))
			Expect(service.Spec.Ports[0].Port).To(Equal(sonarrTestPort))
		}
		expectSonarrOwner(&metadata, sonarr)
	}
}

func expectSonarrOwner(metadata *metav1.ObjectMeta, sonarr *feddemav1alpha1.Sonarr) {
	GinkgoHelper()
	owner := metav1.GetControllerOf(metadata)
	Expect(owner).NotTo(BeNil())
	Expect(owner.Kind).To(Equal("Sonarr"))
	Expect(owner.UID).To(Equal(sonarr.UID))
}

func expectSonarrServicePort(name string, port int32) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var service corev1.Service
		g.Expect(getTestObject("service", name, &service)).To(Succeed())
		g.Expect(service.Spec.Ports).To(HaveLen(1))
		g.Expect(service.Spec.Ports[0].Port).To(Equal(port))
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
}

func deleteTestSonarr(name string) error {
	if _, err := utils.Run(utils.KindKubectl(kubectlDelete, "sonarr", name, ignoreNotFoundFlag,
		"--timeout=2m", namespaceFlag, mediaNamespace)); err != nil {
		return fmt.Errorf("unable to delete Sonarr %s: %w", name, err)
	}
	return nil
}

func installGatewayHTTPRouteCRD() error {
	moduleDir, err := utils.Run(exec.Command("go", "list", "-m", "-f={{.Dir}}", "sigs.k8s.io/gateway-api"))
	if err != nil {
		return fmt.Errorf("unable to locate pinned Gateway API module: %w", err)
	}
	crdPath := filepath.Join(strings.TrimSpace(string(moduleDir)), "config", "crd", "standard",
		"gateway.networking.k8s.io_httproutes.yaml")
	if _, err := utils.Run(utils.KindKubectl("create", "-f", crdPath)); err != nil {
		return fmt.Errorf("unable to install Gateway API HTTPRoute CRD: %w", err)
	}
	gatewayAPICRDInstalled = true
	if _, err := utils.Run(utils.KindKubectl("wait", "--for=condition=Established", "--timeout=2m",
		"crd/httproutes.gateway.networking.k8s.io")); err != nil {
		return fmt.Errorf("unable to wait for Gateway API HTTPRoute CRD: %w", err)
	}
	return nil
}
