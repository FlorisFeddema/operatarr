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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/FlorisFeddema/operatarr/test/utils"
)

const (
	namespace          = "operatarr-system"
	namespaceFlag      = "--namespace"
	managerDeployment  = "operatarr-controller-manager"
	managerSelector    = "control-plane=controller-manager"
	leaderElectionName = "f9ce3012.feddema.dev"
	managerImage       = "example.com/operatarr:e2e"
	jobRunnerImage     = "example.com/operatarr-jobrunner:e2e"
	stabilityWindow    = 2 * time.Minute
)

var (
	targetContextVerified bool
	overlayApplyAttempted bool
)

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("verifying the current context is the selected kind cluster")
		Expect(verifyKindContext()).To(Succeed())
		targetContextVerified = true

		By("building local manager and jobrunner images and loading them into kind")
		Expect(prepareImages()).To(Succeed())

		By("installing CRDs and deploying the manager from the e2e overlay")
		overlayApplyAttempted = true
		_, err := utils.Run(exec.Command("kubectl", "apply", "-k", "test/e2e/overlay"))
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if !targetContextVerified {
			return
		}

		By("removing e2e-only metrics access")
		var cleanupErrors []error
		metricsCleanup := exec.Command("kubectl", "delete", "-f",
			"test/e2e/manifests/metrics-reader.yaml", "--ignore-not-found=true")
		if _, err := utils.Run(metricsCleanup); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}

		if overlayApplyAttempted {
			By("undeploying the manager and removing its CRDs and RBAC")
			overlayCleanup := exec.Command("kubectl", "delete", "-k",
				"test/e2e/overlay", "--ignore-not-found=true")
			if _, err := utils.Run(overlayCleanup); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		Expect(errors.Join(cleanupErrors...)).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			collectDiagnostics()
		}
	})

	Context("Operator", func() {
		It("starts and remains healthy after cache synchronization", func() {
			By("waiting for the manager Deployment to become available")
			cmd := exec.Command("kubectl", "wait",
				"--for=condition=Available",
				"--timeout=3m",
				"deployment/"+managerDeployment,
				namespaceFlag, namespace,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("waiting for the manager pod to become ready")
			cmd = exec.Command("kubectl", "wait",
				"--for=condition=Ready",
				"--timeout=3m",
				"pod", "-l", managerSelector,
				namespaceFlag, namespace,
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("checking the manager does not restart during cache synchronization")
			Consistently(func(g Gomega) {
				podName, err := controllerPodName()
				g.Expect(err).NotTo(HaveOccurred())
				restarts, err := utils.Run(exec.Command("kubectl", "get", "pod", podName,
					"-o", "jsonpath={.status.containerStatuses[0].restartCount}",
					namespaceFlag, namespace,
				))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(restarts))).To(Equal("0"))
			}, stabilityWindow, 5*time.Second).Should(Succeed())

			By("checking manager logs for RBAC errors")
			logs, err := utils.Run(exec.Command("kubectl", "logs",
				"deployment/"+managerDeployment,
				"--all-containers=true",
				namespaceFlag, namespace,
			))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			lowerLogs := strings.ToLower(string(logs))
			Expect(lowerLogs).NotTo(ContainSubstring("forbidden"))
			Expect(lowerLogs).NotTo(ContainSubstring("cannot list resource"))
		})

		It("serves metrics to an authorized service account", func() {
			By("creating a service account bound to the metrics-reader role")
			_, err := utils.Run(exec.Command("kubectl", "apply", "-f",
				"test/e2e/manifests/metrics-reader.yaml"))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_, cleanupErr := utils.Run(exec.Command("kubectl", "delete", "-f",
					"test/e2e/manifests/metrics-reader.yaml", "--ignore-not-found=true"))
				Expect(cleanupErr).NotTo(HaveOccurred())
			})

			By("requesting the HTTPS metrics endpoint from inside the cluster")
			output, err := utils.Run(exec.Command("kubectl", "run", "e2e-metrics-check",
				"--image=curlimages/curl:8.12.1",
				"--restart=Never",
				"--rm",
				"-i",
				namespaceFlag, namespace,
				"--overrides", `{"apiVersion":"v1","kind":"Pod","spec":{"serviceAccountName":"operatarr-e2e-metrics-reader"}}`,
				"--command", "--",
				"curl",
				"--insecure",
				"--fail",
				"--silent",
				"--show-error",
				"--write-out", "\nE2E_HTTP_STATUS:%{http_code}\n",
				"https://operatarr-controller-manager-metrics-service:8443/metrics",
			))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("# HELP"))
			Expect(string(output)).To(ContainSubstring("E2E_HTTP_STATUS:200"))
		})

		It("holds the leader-election lease", func() {
			By("checking the lease holder matches the manager pod")
			Eventually(func(g Gomega) {
				podName, err := controllerPodName()
				g.Expect(err).NotTo(HaveOccurred())

				holder, err := utils.Run(exec.Command("kubectl", "get", "lease", leaderElectionName,
					"-o", "jsonpath={.spec.holderIdentity}",
					namespaceFlag, namespace,
				))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(holder))).To(ContainSubstring(podName))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		It("establishes both custom resource definitions", func() {
			for _, name := range []string{
				"medialibraries.feddema.dev",
				"sonarrs.feddema.dev",
			} {
				cmd := exec.Command("kubectl", "wait",
					"--for=condition=Established",
					"--timeout=60s",
					"crd/"+name,
				)
				_, err := utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			}
		})
	})
})

func verifyKindContext() error {
	cluster := os.Getenv("KIND_CLUSTER")
	if cluster == "" {
		cluster = "operatarr-e2e"
	}

	output, err := utils.Run(exec.Command("kubectl", "config", "current-context"))
	if err != nil {
		return fmt.Errorf("unable to read current Kubernetes context: %w", err)
	}
	expected := "kind-" + cluster
	current := strings.TrimSpace(string(output))
	if current != expected {
		return fmt.Errorf("refusing to run e2e tests in context %q; expected %q", current, expected)
	}
	return nil
}

func prepareImages() (result error) {
	imageDir, err := os.MkdirTemp("", "operatarr-e2e-images-")
	if err != nil {
		return fmt.Errorf("unable to create temporary image directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(imageDir); err != nil {
			result = errors.Join(result, fmt.Errorf("unable to remove temporary image directory: %w", err))
		}
	}()

	platform := "linux/" + runtime.GOARCH
	cmd := exec.Command("make", "e2e-images",
		"E2E_PLATFORM="+platform,
		"E2E_IMAGE_DIR="+imageDir,
	)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("unable to build e2e images: %w", err)
	}

	for _, archive := range []string{"manager.tar", "jobrunner.tar"} {
		if err := utils.LoadImageArchiveToKindCluster(filepath.Join(imageDir, archive)); err != nil {
			return fmt.Errorf("unable to load %s into kind: %w", archive, err)
		}
	}
	return nil
}

func controllerPodName() (string, error) {
	cmd := exec.Command("kubectl", "get", "pods",
		"-l", managerSelector,
		"-o", "go-template={{ range .items }}"+
			"{{ if not .metadata.deletionTimestamp }}{{ .metadata.name }}"+
			"{{ \"\\n\" }}{{ end }}{{ end }}",
		namespaceFlag, namespace,
	)
	output, err := utils.Run(cmd)
	if err != nil {
		return "", err
	}
	podNames := utils.GetNonEmptyLines(string(output))
	if len(podNames) != 1 {
		return "", fmt.Errorf("expected exactly one manager pod, found %d", len(podNames))
	}
	return podNames[0], nil
}

func collectDiagnostics() {
	fmt.Fprintln(GinkgoWriter, "E2E failure diagnostics:")
	commands := [][]string{
		{"get", "pods", "-o", "wide", namespaceFlag, namespace},
		{"describe", "pods", "-l", managerSelector, namespaceFlag, namespace},
		{"logs", "deployment/" + managerDeployment, "--all-containers=true", namespaceFlag, namespace},
		{"get", "events", "--sort-by=.metadata.creationTimestamp", namespaceFlag, namespace},
	}
	for _, args := range commands {
		fmt.Fprintf(GinkgoWriter, "$ kubectl %s\n", strings.Join(args, " "))
		output, err := utils.Run(exec.Command("kubectl", args...))
		if len(output) > 0 {
			fmt.Fprintln(GinkgoWriter, string(output))
		}
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "diagnostic command failed: %v\n", err)
		}
	}
}
