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
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	feddemav1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/FlorisFeddema/operatarr/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	e2eRunLabel           = "e2e.feddema.dev/run"
	e2eDebugLabel         = "e2e.feddema.dev/debug"
	mediaLibraryFinalizer = "operatarr.feddema.dev/finalizer"
	mediaLibraryKind      = "MediaLibrary"
	managedLibraryName    = "managed-library"
	failedLibraryName     = "failed-init-library"
	resizeLibraryName     = "resize-library"
	retainedLibraryName   = "retained-library"
	kubectlAll            = "--all"
	twoMinuteTimeoutFlag  = "--timeout=2m"
	operatarrAPIVersion   = "feddema.dev/v1alpha1"
)

func quantityPointer(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}

//go:embed manifests/local-path.yaml.tmpl
var localPathTemplate string

var (
	mediaNamespace        string
	mediaNamespaceCreated bool
	localPathHelperImage  string
)

func mediaLibrarySpecs() {
	Context(mediaLibraryKind, func() {
		BeforeAll(func() {
			By("checking the single-node cluster and its local-path provisioner images")
			provisionerImage, helperImage, err := localPathImages()
			Expect(err).NotTo(HaveOccurred())
			localPathHelperImage = helperImage

			By("creating an isolated namespace and test-only RWX provisioner")
			mediaNamespace, err = newMediaRunName()
			Expect(err).NotTo(HaveOccurred())
			_, err = testStorageRoot()
			Expect(err).NotTo(HaveOccurred())

			testNamespace := &corev1.Namespace{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
				ObjectMeta: metav1.ObjectMeta{Name: mediaNamespace, Labels: map[string]string{e2eRunLabel: mediaNamespace}},
			}
			Expect(createTestObject(testNamespace)).To(Succeed())
			mediaNamespaceCreated = true

			provisionerJSON, err := json.Marshal(provisionerImage)
			Expect(err).NotTo(HaveOccurred())
			helperJSON, err := json.Marshal(helperImage)
			Expect(err).NotTo(HaveOccurred())
			manifest := strings.NewReplacer(
				"E2E_RUN_NAME", mediaNamespace,
				"E2E_PROVISIONER_IMAGE", string(provisionerJSON),
				"E2E_HELPER_IMAGE", string(helperJSON),
			).Replace(localPathTemplate)
			_, err = runKubectlWithInput([]byte(manifest), "create", "-f", "-")
			Expect(err).NotTo(HaveOccurred())

			_, err = utils.Run(utils.KindKubectl("wait", "--for=condition=Available", "--timeout=3m",
				"deployment/"+mediaNamespace, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
		})

		It("uses an existing ReadWriteMany PVC and leaves it after deletion", func() {
			const libraryName = "existing-library"
			pvcName := "existing-rwx"

			By("provisioning a ReadWriteMany PVC")
			Expect(createTestObject(newTestPVC(pvcName, corev1.ReadWriteMany))).To(Succeed())
			Expect(waitForPVCBound(pvcName)).To(Succeed())

			By("creating a MediaLibrary that references the PVC")
			library := newTestLibrary(libraryName, pvcName)
			library.Spec.PVCRetentionPolicy = feddemav1alpha1.PVCRetentionPolicyRetain
			Expect(createTestObject(library)).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionTrue, "ExistingPVCFound", true, &pvcName)

			By("checking no operator-managed PVC was created")
			output, err := utils.Run(utils.KindKubectl(kubectlGet, "pvc", libraryName, ignoreNotFoundFlag,
				"-o", "name", namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(string(output))).To(BeEmpty())

			By("deleting the MediaLibrary and checking the referenced PVC remains")
			_, err = utils.Run(utils.KindKubectl(kubectlDelete, "medialibrary", libraryName, twoMinuteTimeoutFlag,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			var pvc corev1.PersistentVolumeClaim
			Expect(getTestObject("pvc", pvcName, &pvc)).To(Succeed())
			Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteMany))
		})

		It("rejects an existing PVC without ReadWriteMany access", func() {
			const libraryName = "read-write-once-library"
			pvcName := "existing-rwo"

			By("creating a bound ReadWriteOnce PVC")
			Expect(createTestObject(newTestPVC(pvcName, corev1.ReadWriteOnce))).To(Succeed())
			Expect(waitForPVCBound(pvcName)).To(Succeed())

			By("checking the MediaLibrary reports the unsupported access mode")
			Expect(createTestObject(newTestLibrary(libraryName, pvcName))).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionFalse, "ExistingPVCInvalidAccessMode", false, nil)
		})

		It("recovers when a missing referenced PVC is created later", func() {
			const libraryName = "late-pvc-library"
			pvcName := "late-rwx"

			By("checking the missing dependency is reported")
			Expect(createTestObject(newTestLibrary(libraryName, pvcName))).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionFalse, "ExistingPVCNotFound", false, nil)

			By("creating the PVC and waiting for reconciliation to recover")
			Expect(createTestObject(newTestPVC(pvcName, corev1.ReadWriteMany))).To(Succeed())
			Expect(waitForPVCBound(pvcName)).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionTrue, "ExistingPVCFound", true, &pvcName)
		})

		It("initializes a managed ReadWriteMany PVC", func() {
			const libraryName = managedLibraryName
			pvcName := libraryName

			By("creating a MediaLibrary with managed RWX storage")
			Expect(createTestObject(newManagedTestLibrary(libraryName))).To(Succeed())
			Expect(waitForPVCBound(pvcName)).To(Succeed())
			library := expectLibraryStatus(libraryName, metav1.ConditionTrue, "PVCInitialized", true, &pvcName)

			By("checking ownership and the initialization Job")
			var pvc corev1.PersistentVolumeClaim
			Expect(getTestObject("pvc", pvcName, &pvc)).To(Succeed())
			Expect(pvc.Spec.AccessModes).To(ConsistOf(corev1.ReadWriteMany))
			Expect(pvc.Spec.VolumeName).NotTo(BeEmpty())
			expectMediaOwner(&pvc.ObjectMeta, library)

			var job batchv1.Job
			jobName := libraryName + "-pvc-init"
			Expect(getTestObject("job", jobName, &job)).To(Succeed())
			expectMediaOwner(&job.ObjectMeta, library)
			Expect(job.Spec.Template.Spec.NodeSelector).NotTo(HaveKey("kubernetes.io/arch"))
			_, err := utils.Run(utils.KindKubectl("wait", "--for=condition=Complete", twoMinuteTimeoutFlag,
				"job/"+jobName, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())

			By("checking the initialized media directories through a mounted pod")
			checkMediaFolders(pvcName)

			By("checking two pods can write to the same claim concurrently")
			checkConcurrentRWX(pvcName)
		})

		It("reports a failed initialization Job", func() {
			const libraryName = failedLibraryName

			By("creating a managed claim with a file where a media directory must be")
			Expect(createTestObject(newTestPVC(libraryName, corev1.ReadWriteMany))).To(Succeed())
			Expect(waitForPVCBound(libraryName)).To(Succeed())
			err := runMediaVolumePod(libraryName, libraryName+"-prepare", "printf blocked > /library/movies", false)
			Expect(err).NotTo(HaveOccurred())

			By("checking that the failing Job leaves the MediaLibrary uninitialized")
			Expect(createTestObject(newManagedTestLibrary(libraryName))).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionFalse, "PVCInitializationFailed", false, new(libraryName))
			jobName := libraryName + "-pvc-init"
			_, err = utils.Run(utils.KindKubectl("wait", "--for=condition=Failed", twoMinuteTimeoutFlag,
				"job/"+jobName, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			output, err := utils.Run(utils.KindKubectl("logs", "job/"+jobName, "--all-containers=true",
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("is not a directory"))
		})

		It("increases a managed PVC request when the requested size grows", func() {
			const libraryName = resizeLibraryName
			pvcName := libraryName

			By("creating a managed MediaLibrary with an expandable StorageClass")
			Expect(createTestObject(newManagedTestLibrary(libraryName))).To(Succeed())
			Expect(waitForPVCBound(pvcName)).To(Succeed())
			expectLibraryStatus(libraryName, metav1.ConditionTrue, "PVCInitialized", true, &pvcName)

			By("increasing the requested MediaLibrary storage size")
			_, err := utils.Run(utils.KindKubectl("patch", "medialibrary", libraryName, "--type=merge",
				"-p", `{"spec":{"pvc":{"size":"256Mi"}}}`, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())

			By("checking the managed PVC request was expanded")
			expectedSize := resource.MustParse("256Mi")
			Eventually(func(g Gomega) {
				var pvc corev1.PersistentVolumeClaim
				g.Expect(getTestObject("pvc", pvcName, &pvc)).To(Succeed())
				requestedSize := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
				g.Expect(requestedSize.Cmp(expectedSize)).To(BeZero())
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		})

		It("rejects changes to an existing storage class", func() {
			var before corev1.PersistentVolumeClaim
			Expect(getTestObject("pvc", managedLibraryName, &before)).To(Succeed())

			By("attempting to change the MediaLibrary storage class")
			_, err := utils.Run(utils.KindKubectl("patch", "medialibrary", managedLibraryName, "--type=merge",
				"-p", `{"spec":{"pvc":{"storageClassName":"another-class"}}}`, namespaceFlag, mediaNamespace))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Value is immutable"))

			var library feddemav1alpha1.MediaLibrary
			Expect(getTestObject("medialibrary", managedLibraryName, &library)).To(Succeed())
			Expect(library.Spec.PVC.StorageClassName).To(Equal(&mediaNamespace))
			var after corev1.PersistentVolumeClaim
			Expect(getTestObject("pvc", managedLibraryName, &after)).To(Succeed())
			Expect(after.Spec).To(Equal(before.Spec))
		})

		It("deletes managed PVCs by default and retains them when requested", func() {
			const deleteLibraryName = "delete-policy-library"
			deletePVCName := deleteLibraryName

			By("creating a managed MediaLibrary with the default Delete policy")
			Expect(createTestObject(newManagedTestLibrary(deleteLibraryName))).To(Succeed())
			Expect(waitForPVCBound(deleteLibraryName)).To(Succeed())
			deletingLibrary := expectLibraryStatus(
				deleteLibraryName, metav1.ConditionTrue, "PVCInitialized", true, &deletePVCName,
			)
			Expect(deletingLibrary.Spec.PVCRetentionPolicy).To(Equal(feddemav1alpha1.PVCRetentionPolicyDelete))

			By("cleaning the local-path test volume before deletion")
			Expect(runMediaVolumePod(deleteLibraryName, deleteLibraryName+"-prepare-delete",
				"rm -r /library/movies /library/tv /library/music", false)).To(Succeed())
			_, err := utils.Run(utils.KindKubectl(kubectlDelete, podResource,
				deleteLibraryName+"-prepare-delete", twoMinuteTimeoutFlag,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())

			By("deleting the MediaLibrary and waiting for its PVC and initialization Job to be deleted")
			_, err = utils.Run(utils.KindKubectl(kubectlDelete, "medialibrary", deleteLibraryName,
				twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			for _, resource := range []string{"pvc/" + deleteLibraryName, "job/" + deleteLibraryName + "-pvc-init"} {
				_, err = utils.Run(utils.KindKubectl("wait", "--for=delete", resource,
					twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace))
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a managed MediaLibrary with the Retain policy")
			retainedLibrary := newManagedTestLibrary(retainedLibraryName)
			retainedLibrary.Spec.PVCRetentionPolicy = feddemav1alpha1.PVCRetentionPolicyRetain
			Expect(createTestObject(retainedLibrary)).To(Succeed())
			Expect(waitForPVCBound(retainedLibraryName)).To(Succeed())
			retainedPVCName := retainedLibraryName
			expectLibraryStatus(
				retainedLibraryName, metav1.ConditionTrue, "PVCInitialized", true, &retainedPVCName,
			)

			By("deleting the MediaLibrary while preserving its PVC and data")
			_, err = utils.Run(utils.KindKubectl(kubectlDelete, "medialibrary", retainedLibraryName,
				twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				var pvc corev1.PersistentVolumeClaim
				g.Expect(getTestObject("pvc", retainedLibraryName, &pvc)).To(Succeed())
				g.Expect(pvc.OwnerReferences).To(BeEmpty())
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
			_, err = utils.Run(utils.KindKubectl("wait", "--for=delete",
				"job/"+retainedLibraryName+"-pvc-init", twoMinuteTimeoutFlag,
				namespaceFlag, mediaNamespace))
			Expect(err).NotTo(HaveOccurred())
			checkMediaFolders(retainedLibraryName)
		})

		It("keeps initialized resources unchanged across reconciliations", func() {
			for attempt := 1; attempt <= 2; attempt++ {
				var pvc corev1.PersistentVolumeClaim
				var job batchv1.Job
				Expect(getTestObject("pvc", managedLibraryName, &pvc)).To(Succeed())
				Expect(getTestObject("job", managedLibraryName+"-pvc-init", &job)).To(Succeed())
				before, err := mediaReconcileCount(managedLibraryName)
				Expect(err).NotTo(HaveOccurred())

				output, err := utils.Run(utils.KindKubectl("annotate", "medialibrary", managedLibraryName,
					fmt.Sprintf("e2e.feddema.dev/reconcile=%d", attempt), "--overwrite", "-o", "json",
					namespaceFlag, mediaNamespace))
				Expect(err).NotTo(HaveOccurred())
				var annotated feddemav1alpha1.MediaLibrary
				Expect(json.Unmarshal(output, &annotated)).To(Succeed())

				Eventually(func(g Gomega) {
					count, countErr := mediaReconcileCount(managedLibraryName)
					g.Expect(countErr).NotTo(HaveOccurred())
					g.Expect(count).To(BeNumerically(">", before))
				}, 30*time.Second, time.Second).Should(Succeed())
				Consistently(func(g Gomega) {
					var currentLibrary feddemav1alpha1.MediaLibrary
					var currentPVC corev1.PersistentVolumeClaim
					var currentJob batchv1.Job
					g.Expect(getTestObject("medialibrary", managedLibraryName, &currentLibrary)).To(Succeed())
					g.Expect(getTestObject("pvc", managedLibraryName, &currentPVC)).To(Succeed())
					g.Expect(getTestObject("job", managedLibraryName+"-pvc-init", &currentJob)).To(Succeed())
					g.Expect(currentLibrary.ResourceVersion).To(Equal(annotated.ResourceVersion))
					g.Expect(currentPVC.ResourceVersion).To(Equal(pvc.ResourceVersion))
					g.Expect(currentJob.ResourceVersion).To(Equal(job.ResourceVersion))
				}, 10*time.Second, 2*time.Second).Should(Succeed())
			}
		})
	})
}

func localPathImages() (string, string, error) {
	output, err := utils.Run(utils.KindKubectl(kubectlGet, "nodes", "-o", "json"))
	if err != nil {
		return "", "", fmt.Errorf("unable to inspect kind nodes: %w", err)
	}
	var nodes corev1.NodeList
	if err := json.Unmarshal(output, &nodes); err != nil {
		return "", "", fmt.Errorf("unable to decode kind nodes: %w", err)
	}
	if len(nodes.Items) != 1 {
		return "", "", fmt.Errorf("test RWX storage requires one kind node, found %d", len(nodes.Items))
	}

	var deployment appsv1.Deployment
	if err := getNamespacedObject("local-path-storage", "deployment", "local-path-provisioner", &deployment); err != nil {
		return "", "", fmt.Errorf("unable to inspect kind's local-path provisioner: %w", err)
	}
	var provisionerImage string
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "local-path-provisioner" {
			provisionerImage = container.Image
		}
	}
	if provisionerImage == "" {
		return "", "", fmt.Errorf("kind's local-path deployment has no provisioner image")
	}

	var config corev1.ConfigMap
	if err := getNamespacedObject("local-path-storage", "configmap", "local-path-config", &config); err != nil {
		return "", "", fmt.Errorf("unable to inspect kind's local-path configuration: %w", err)
	}
	var helper corev1.Pod
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(config.Data["helperPod.yaml"]), 4096)
	if err := decoder.Decode(&helper); err != nil {
		return "", "", fmt.Errorf("unable to decode kind's local-path helper pod: %w", err)
	}
	if len(helper.Spec.Containers) != 1 || helper.Spec.Containers[0].Image == "" {
		return "", "", fmt.Errorf("kind's local-path helper must specify one container image")
	}
	return provisionerImage, helper.Spec.Containers[0].Image, nil
}

func testStorageRoot() (string, error) {
	const prefix = "operatarr-e2e-"
	if !strings.HasPrefix(mediaNamespace, prefix) {
		return "", fmt.Errorf("invalid test storage namespace %q", mediaNamespace)
	}
	suffix := strings.TrimPrefix(mediaNamespace, prefix)
	if len(suffix) != 12 {
		return "", fmt.Errorf("invalid test storage namespace %q", mediaNamespace)
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return "", fmt.Errorf("invalid test storage namespace %q: %w", mediaNamespace, err)
	}
	return "/tmp/" + mediaNamespace, nil
}

func TestStorageRootValidation(t *testing.T) {
	previous := mediaNamespace
	t.Cleanup(func() { mediaNamespace = previous })
	for _, name := range []string{"", "operatarr-e2e-../tmp", "operatarr-e2e-not-hex-data", "other-123456789abc"} {
		mediaNamespace = name
		if _, err := testStorageRoot(); err == nil {
			t.Fatalf("expected invalid storage namespace %q to be rejected", name)
		}
	}
	mediaNamespace = "operatarr-e2e-123456789abc"
	root, err := testStorageRoot()
	if err != nil || root != "/tmp/operatarr-e2e-123456789abc" {
		t.Fatalf("expected isolated test storage root, got %q, %v", root, err)
	}
}

func mediaReconcileCount(name string) (int, error) {
	output, err := utils.Run(utils.KindKubectl("logs", "deployment/"+managerDeployment, "--all-containers=true",
		namespaceFlag, namespace))
	if err != nil {
		return 0, fmt.Errorf("unable to read manager logs: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "\tStarting reconcile iteration\t") {
			continue
		}
		start := strings.IndexByte(line, '{')
		if start < 0 {
			return 0, fmt.Errorf("reconcile log has no structured fields: %s", line)
		}
		var fields struct {
			ControllerKind string `json:"controllerKind"`
			Namespace      string `json:"namespace"`
			Name           string `json:"name"`
		}
		if err := json.Unmarshal([]byte(line[start:]), &fields); err != nil {
			return 0, fmt.Errorf("unable to decode reconcile log: %w", err)
		}
		if fields.ControllerKind == mediaLibraryKind && fields.Namespace == mediaNamespace && fields.Name == name {
			count++
		}
	}
	return count, nil
}

func newMediaRunName() (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("unable to generate MediaLibrary test namespace: %w", err)
	}
	return fmt.Sprintf("operatarr-e2e-%x", suffix), nil
}

func runKubectlWithInput(input []byte, args ...string) ([]byte, error) {
	cmd := utils.KindKubectl(args...)
	cmd.Stdin = bytes.NewReader(input)
	return utils.Run(cmd)
}

func createTestObject(object any) error {
	data, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("unable to encode test object: %w", err)
	}
	if _, err := runKubectlWithInput(data, "create", "-f", "-"); err != nil {
		return fmt.Errorf("unable to create test object: %w", err)
	}
	return nil
}

func getTestObject(kind, name string, object any) error {
	return getNamespacedObject(mediaNamespace, kind, name, object)
}

func getNamespacedObject(objectNamespace, kind, name string, object any) error {
	output, err := utils.Run(utils.KindKubectl(kubectlGet, kind, name, "-o", "json", namespaceFlag, objectNamespace))
	if err != nil {
		return fmt.Errorf("unable to get %s %s: %w", kind, name, err)
	}
	if err := json.Unmarshal(output, object); err != nil {
		return fmt.Errorf("unable to decode %s %s: %w", kind, name, err)
	}
	return nil
}

func newTestPVC(name string, mode corev1.PersistentVolumeAccessMode) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mediaNamespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{mode},
			StorageClassName: &mediaNamespace,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("128Mi")},
			},
		},
	}
}

func newTestLibrary(name, pvcName string) *feddemav1alpha1.MediaLibrary {
	return &feddemav1alpha1.MediaLibrary{
		TypeMeta:   metav1.TypeMeta{APIVersion: operatarrAPIVersion, Kind: mediaLibraryKind},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mediaNamespace},
		Spec:       feddemav1alpha1.MediaLibrarySpec{PVC: feddemav1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
	}
}

func newManagedTestLibrary(name string) *feddemav1alpha1.MediaLibrary {
	return &feddemav1alpha1.MediaLibrary{
		TypeMeta:   metav1.TypeMeta{APIVersion: "feddema.dev/v1alpha1", Kind: mediaLibraryKind},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mediaNamespace},
		Spec: feddemav1alpha1.MediaLibrarySpec{
			PVC: feddemav1alpha1.MediaLibraryPVC{
				Size:             quantityPointer("128Mi"),
				StorageClassName: &mediaNamespace,
			},
		},
	}
}

func waitForPVCBound(name string) error {
	_, err := utils.Run(utils.KindKubectl("wait", "--for=jsonpath={.status.phase}=Bound", twoMinuteTimeoutFlag,
		"pvc/"+name, namespaceFlag, mediaNamespace))
	if err != nil {
		return fmt.Errorf("unable to wait for PVC %s to bind: %w", name, err)
	}
	return nil
}

func expectLibraryStatus(
	name string, status metav1.ConditionStatus, reason string, initialized bool, pvcName *string,
) *feddemav1alpha1.MediaLibrary {
	GinkgoHelper()
	var observed feddemav1alpha1.MediaLibrary
	Eventually(func(g Gomega) {
		var library feddemav1alpha1.MediaLibrary
		g.Expect(getTestObject("medialibrary", name, &library)).To(Succeed())
		condition := meta.FindStatusCondition(library.Status.Conditions, "Available")
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.Status).To(Equal(status))
		g.Expect(condition.Reason).To(Equal(reason))
		g.Expect(library.Status.Initialized).To(Equal(initialized))
		g.Expect(library.Status.EffectivePVC).To(Equal(pvcName))
		g.Expect(library.Finalizers).To(ContainElement(mediaLibraryFinalizer))
		observed = library
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
	return &observed
}

func expectMediaOwner(object *metav1.ObjectMeta, library *feddemav1alpha1.MediaLibrary) {
	GinkgoHelper()
	owner := metav1.GetControllerOf(object)
	Expect(owner).NotTo(BeNil())
	Expect(owner.Kind).To(Equal(mediaLibraryKind))
	Expect(owner.UID).To(Equal(library.UID))
}

func checkMediaFolders(pvcName string) {
	GinkgoHelper()
	command := "test -d /library/movies && test -d /library/tv && test -d /library/music"
	Expect(runMediaVolumePod(pvcName, pvcName+"-check", command, true)).To(Succeed())
}

func checkConcurrentRWX(pvcName string) {
	GinkgoHelper()
	writerName := pvcName + "-writer"
	readerName := pvcName + "-reader"
	writer := newMediaVolumePod(pvcName, writerName, "printf 'writer\\n' > /library/.e2e-writer; sleep 120", false)
	Expect(createTestObject(writer)).To(Succeed())
	_, err := utils.Run(utils.KindKubectl("wait", "--for=condition=Ready", twoMinuteTimeoutFlag,
		"pod/"+writerName, namespaceFlag, mediaNamespace))
	Expect(err).NotTo(HaveOccurred())

	reader := newMediaVolumePod(pvcName, readerName,
		"grep -qx writer /library/.e2e-writer; printf 'reader\\n' > /library/.e2e-reader; sleep 120", false)
	Expect(createTestObject(reader)).To(Succeed())
	_, err = utils.Run(utils.KindKubectl("wait", "--for=condition=Ready", twoMinuteTimeoutFlag,
		"pod/"+readerName, namespaceFlag, mediaNamespace))
	Expect(err).NotTo(HaveOccurred())
	_, err = utils.Run(utils.KindKubectl("exec", writerName, namespaceFlag, mediaNamespace,
		"--", "grep", "-qx", "reader", "/library/.e2e-reader"))
	Expect(err).NotTo(HaveOccurred())
	_, err = utils.Run(utils.KindKubectl(kubectlDelete, podResource, writerName, readerName,
		twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace))
	Expect(err).NotTo(HaveOccurred())
}

func newMediaVolumePod(pvcName, podName, command string, readOnly bool) *corev1.Pod {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: podName, Namespace: mediaNamespace, Labels: map[string]string{e2eDebugLabel: "true"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:         corev1.RestartPolicyNever,
			ActiveDeadlineSeconds: new(int64(180)),
			Containers: []corev1.Container{{
				Name:    "check",
				Image:   "curlimages/curl:8.12.1",
				Command: []string{"sh", "-ec", command},
				VolumeMounts: []corev1.VolumeMount{{
					Name: "library", MountPath: "/library", ReadOnly: readOnly,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "library",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
				},
			}},
		},
	}
	if !readOnly {
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: new(true), RunAsUser: new(int64(5000)), RunAsGroup: new(int64(5000)), FSGroup: new(int64(5000)),
		}
	}
	return pod
}

func runMediaVolumePod(pvcName, podName, command string, readOnly bool) error {
	if err := createTestObject(newMediaVolumePod(pvcName, podName, command, readOnly)); err != nil {
		return err
	}
	return waitForTestPod(podName)
}

func waitForTestPod(name string) error {
	var phase corev1.PodPhase
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 2*time.Minute, true,
		func(context.Context) (bool, error) {
			var pod corev1.Pod
			if err := getTestObject("pod", name, &pod); err != nil {
				return false, err
			}
			phase = pod.Status.Phase
			if phase == corev1.PodFailed {
				return false, fmt.Errorf("pod %s failed: %s", name, pod.Status.Message)
			}
			return phase == corev1.PodSucceeded, nil
		})
	if err != nil {
		return fmt.Errorf("unable to wait for pod %s (phase %s): %w", name, phase, err)
	}
	return nil
}

func cleanupMediaResources() error {
	if _, err := utils.Run(utils.KindKubectl(kubectlDelete, podResource, "-l", e2eDebugLabel+"=true",
		ignoreNotFoundFlag, twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace)); err != nil {
		return fmt.Errorf("unable to remove MediaLibrary test pods: %w", err)
	}
	var protectedPods []string
	for _, name := range []string{managedLibraryName, failedLibraryName, resizeLibraryName, retainedLibraryName} {
		podName, err := protectTestVolume(name)
		if err != nil {
			return err
		}
		if podName != "" {
			protectedPods = append(protectedPods, podName)
		}
	}

	commands := [][]string{
		{kubectlDelete, "sonarrs", kubectlAll, ignoreNotFoundFlag, twoMinuteTimeoutFlag,
			namespaceFlag, mediaNamespace},
		{kubectlDelete, "medialibraries", kubectlAll, ignoreNotFoundFlag, twoMinuteTimeoutFlag,
			namespaceFlag, mediaNamespace},
		{kubectlDelete, "jobs", kubectlAll, ignoreNotFoundFlag, twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace},
		{kubectlDelete, podResource, "-l", "batch.kubernetes.io/job-name", ignoreNotFoundFlag, twoMinuteTimeoutFlag,
			namespaceFlag, mediaNamespace},
	}
	for _, args := range commands {
		if _, err := utils.Run(utils.KindKubectl(args...)); err != nil {
			return fmt.Errorf("unable to clean up MediaLibrary test resources: %w", err)
		}
	}
	for _, podName := range protectedPods {
		if err := cleanTestVolume(podName); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{kubectlDelete, podResource, "-l", e2eDebugLabel + "=true", ignoreNotFoundFlag, twoMinuteTimeoutFlag,
			namespaceFlag, mediaNamespace},
		{kubectlDelete, "pvc", kubectlAll, ignoreNotFoundFlag, twoMinuteTimeoutFlag, namespaceFlag, mediaNamespace},
	} {
		if _, err := utils.Run(utils.KindKubectl(args...)); err != nil {
			return fmt.Errorf("unable to clean up MediaLibrary test volumes: %w", err)
		}
	}
	if err := waitForTestVolumesDeleted(); err != nil {
		return err
	}
	if err := removeTestStorageRoot(); err != nil {
		return err
	}

	_, err := utils.Run(utils.KindKubectl(kubectlDelete, "clusterrole,clusterrolebinding,storageclass",
		"-l", e2eRunLabel+"="+mediaNamespace, ignoreNotFoundFlag, twoMinuteTimeoutFlag))
	if err != nil {
		return fmt.Errorf("unable to remove test storage cluster resources: %w", err)
	}
	_, err = utils.Run(utils.KindKubectl(kubectlDelete, "namespace", mediaNamespace,
		ignoreNotFoundFlag, twoMinuteTimeoutFlag))
	if err != nil {
		return fmt.Errorf("unable to remove MediaLibrary test namespace: %w", err)
	}
	return nil
}

func protectTestVolume(name string) (string, error) {
	output, err := utils.Run(utils.KindKubectl(kubectlGet, "pvc", name, ignoreNotFoundFlag, "-o", "json",
		namespaceFlag, mediaNamespace))
	if err != nil {
		return "", fmt.Errorf("unable to inspect test PVC %s: %w", name, err)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return "", nil
	}
	var pvc corev1.PersistentVolumeClaim
	if err := json.Unmarshal(output, &pvc); err != nil {
		return "", fmt.Errorf("unable to decode test PVC %s: %w", name, err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != mediaNamespace {
		return "", fmt.Errorf("refusing to clean PVC %s with an unexpected storage class", name)
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		return "", nil
	}
	pod := newMediaVolumePod(name, name+"-cleanup", "sleep 600", false)
	pod.Spec.ActiveDeadlineSeconds = new(int64(600))
	if err := createTestObject(pod); err != nil {
		return "", fmt.Errorf("unable to protect test PVC %s during cleanup: %w", name, err)
	}
	if _, err := utils.Run(utils.KindKubectl("wait", "--for=condition=Ready", twoMinuteTimeoutFlag,
		"pod/"+pod.Name, namespaceFlag, mediaNamespace)); err != nil {
		return "", fmt.Errorf("unable to mount test PVC %s during cleanup: %w", name, err)
	}
	return pod.Name, nil
}

func cleanTestVolume(podName string) error {
	command := `rm -f /library/.e2e-writer /library/.e2e-reader
if [ -f /library/movies ]; then rm /library/movies; elif [ -d /library/movies ]; then rmdir /library/movies; fi
if [ -d /library/tv ]; then rmdir /library/tv; fi
if [ -d /library/music ]; then rmdir /library/music; fi`
	if _, err := utils.Run(utils.KindKubectl("exec", podName, namespaceFlag, mediaNamespace,
		"--", "sh", "-ec", command)); err != nil {
		return fmt.Errorf("unable to clean test volume using pod %s: %w", podName, err)
	}
	return nil
}

func removeTestStorageRoot() error {
	root, err := testStorageRoot()
	if err != nil {
		return err
	}
	target := "/node-tmp/" + strings.TrimPrefix(root, "/tmp/")
	command := fmt.Sprintf("if [ -d %[1]s ]; then rm -d %[1]s; fi", target)
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "storage-root-cleanup", Namespace: mediaNamespace, Labels: map[string]string{e2eDebugLabel: "true"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:         corev1.RestartPolicyNever,
			ActiveDeadlineSeconds: new(int64(90)),
			Containers: []corev1.Container{{
				Name: "cleanup", Image: localPathHelperImage, Command: []string{"sh", "-ec", command},
				VolumeMounts: []corev1.VolumeMount{{Name: "node-tmp", MountPath: "/node-tmp"}},
			}},
			Volumes: []corev1.Volume{{
				Name: "node-tmp",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: "/tmp", Type: new(corev1.HostPathDirectory)},
				},
			}},
		},
	}
	if err := createTestObject(pod); err != nil {
		return fmt.Errorf("unable to create test storage root cleanup pod: %w", err)
	}
	if err := waitForTestPod(pod.Name); err != nil {
		return fmt.Errorf("unable to remove test storage root %s: %w", root, err)
	}
	return nil
}

func waitForTestVolumesDeleted() error {
	var remaining []string
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 2*time.Minute, true,
		func(context.Context) (bool, error) {
			output, err := utils.Run(utils.KindKubectl(kubectlGet, "persistentvolumes", "-o", "json"))
			if err != nil {
				return false, err
			}
			var volumes corev1.PersistentVolumeList
			if err := json.Unmarshal(output, &volumes); err != nil {
				return false, fmt.Errorf("unable to decode PersistentVolumes: %w", err)
			}
			remaining = nil
			for _, volume := range volumes.Items {
				if volume.Spec.StorageClassName == mediaNamespace {
					remaining = append(remaining, volume.Name)
				}
			}
			return len(remaining) == 0, nil
		})
	if err != nil {
		return fmt.Errorf("unable to remove test PersistentVolumes %v: %w", remaining, err)
	}
	return nil
}
