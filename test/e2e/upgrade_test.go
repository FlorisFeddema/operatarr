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
	"os"
	"strings"
	"time"

	"github.com/FlorisFeddema/operatarr/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	upgradeLibraryName  = "upgrade-library"
	previousManagerRBAC = "test/e2e/manifests/previous-manager-pv-reader.yaml"
)

func upgradeSpecs() {
	Context("Manager upgrade", Ordered, Label("upgrade"), func() {
		It("keeps existing resources working after upgrading from the previous release",
			Label("restart"), Label("slow"), func() {
				previousImage := strings.TrimSpace(os.Getenv(previousManagerEnv))
				if previousImage == "" {
					Skip("set E2E_PREVIOUS_MANAGER_IMAGE to run the manager upgrade test")
				}
				Expect(previousImage).NotTo(Equal(managerImage))

				DeferCleanup(func() {
					_, err := utils.Run(utils.KindKubectl(kubectlDelete, "-f", previousManagerRBAC, ignoreNotFoundFlag))
					Expect(err).NotTo(HaveOccurred())
				})
				DeferCleanup(func() {
					Expect(setManagerImage(managerImage)).To(Succeed())
				})

				By("granting the previous manager temporary PersistentVolume read access")
				_, err := utils.Run(utils.KindKubectl("apply", "-f", previousManagerRBAC))
				Expect(err).NotTo(HaveOccurred())

				By("starting the previous release manager")
				Expect(setManagerImage(previousImage)).To(Succeed())

				By("creating a MediaLibrary with the previous release")
				Expect(createTestObject(newManagedTestLibrary(upgradeLibraryName))).To(Succeed())
				Eventually(func(g Gomega) {
					var pvc corev1.PersistentVolumeClaim
					g.Expect(getTestObject("pvc", upgradeLibraryName, &pvc)).To(Succeed())
				}, 2*time.Minute, 2*time.Second).Should(Succeed())
				Expect(waitForPVCBound(upgradeLibraryName)).To(Succeed())
				library := expectLibraryStatus(
					upgradeLibraryName, metav1.ConditionTrue, "PVCInitialized", true, new(upgradeLibraryName),
				)
				initialLibraryUID := library.UID

				var pvc corev1.PersistentVolumeClaim
				Expect(getTestObject("pvc", upgradeLibraryName, &pvc)).To(Succeed())
				initialPVCUID := pvc.UID

				jobName := upgradeLibraryName + "-pvc-init"
				_, err = utils.Run(utils.KindKubectl("wait", "--for=condition=Complete", twoMinuteTimeoutFlag,
					"job/"+jobName, namespaceFlag, mediaNamespace))
				Expect(err).NotTo(HaveOccurred())
				jobUID, err := getJobUID(jobName)
				Expect(err).NotTo(HaveOccurred())
				Expect(jobUID).NotTo(BeEmpty())

				By("upgrading to the current manager image")
				Expect(setManagerImage(managerImage)).To(Succeed())

				By("changing the existing resource and checking the upgraded manager reconciles it")
				_, err = utils.Run(utils.KindKubectl("patch", "medialibrary", upgradeLibraryName, "--type=merge",
					"-p", `{"spec":{"pvc":{"size":"256Mi"}}}`, namespaceFlag, mediaNamespace))
				Expect(err).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					var currentPVC corev1.PersistentVolumeClaim
					g.Expect(getTestObject("pvc", upgradeLibraryName, &currentPVC)).To(Succeed())
					g.Expect(currentPVC.UID).To(Equal(initialPVCUID))
					g.Expect(currentPVC.Spec.Resources.Requests.Storage().String()).To(Equal("256Mi"))
				}, 2*time.Minute, 2*time.Second).Should(Succeed())

				library = expectLibraryStatus(
					upgradeLibraryName, metav1.ConditionTrue, "PVCInitialized", true, new(upgradeLibraryName),
				)
				Expect(library.UID).To(Equal(initialLibraryUID))
				finalJobUID, err := getJobUID(jobName)
				Expect(err).NotTo(HaveOccurred())
				Expect(finalJobUID).To(Equal(jobUID))

				By("checking the upgraded manager is running the current image")
				podName, err := controllerPodName()
				Expect(err).NotTo(HaveOccurred())
				image, err := utils.Run(utils.KindKubectl(kubectlGet, podResource, podName,
					"-o", "jsonpath={.spec.containers[?(@.name=='manager')].image}", namespaceFlag, namespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(strings.TrimSpace(string(image))).To(Equal(managerImage))
			})
	})
}

func setManagerImage(image string) error {
	if _, err := utils.Run(utils.KindKubectl("set", "image",
		"deployment/"+managerDeployment, managerContainer+"="+image, namespaceFlag, namespace)); err != nil {
		return fmt.Errorf("unable to set manager image to %q: %w", image, err)
	}
	if _, err := utils.Run(utils.KindKubectl("rollout", "status",
		"deployment/"+managerDeployment, "--timeout=3m", namespaceFlag, namespace)); err != nil {
		return fmt.Errorf("unable to wait for manager image %q: %w", image, err)
	}
	return nil
}

func getJobUID(name string) (string, error) {
	output, err := utils.Run(utils.KindKubectl(kubectlGet, "job", name,
		"-o", "jsonpath={.metadata.uid}", namespaceFlag, mediaNamespace))
	if err != nil {
		return "", fmt.Errorf("unable to get MediaLibrary initialization Job %q: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}
