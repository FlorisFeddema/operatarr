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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/FlorisFeddema/operatarr/test/utils"
)

const (
	namespace          = "operatarr-system"
	namespaceFlag      = "--namespace"
	kubectlGet         = "get"
	kubectlDelete      = "delete"
	podResource        = "pods"
	ignoreNotFoundFlag = "--ignore-not-found=true"
	managerDeployment  = "operatarr-controller-manager"
	managerContainer   = "manager"
	managerSelector    = "control-plane=controller-manager"
	leaderElectionName = "f9ce3012.feddema.dev"
	managerImage       = "example.com/operatarr:e2e"
	previousManagerEnv = "E2E_PREVIOUS_MANAGER_IMAGE"
	jobRunnerImage     = "example.com/operatarr-jobrunner:e2e"
	sonarrMockImage    = "example.com/sonarr:e2e"
)

var (
	targetContextVerified bool
	overlayApplyAttempted bool
	e2eStartedAt          time.Time
)

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		e2eStartedAt = time.Now()
		By("verifying the current context is the selected kind cluster")
		Expect(verifyKindContext()).To(Succeed())
		targetContextVerified = true

		By("building local manager and jobrunner images and loading them into kind")
		Expect(prepareImages()).To(Succeed())

		By("installing CRDs and deploying the manager from the e2e overlay")
		overlayApplyAttempted = true
		deployStartedAt := time.Now()
		_, err := utils.Run(utils.KindKubectl("apply", "-k", "test/e2e/overlay"))
		reportE2ETiming("overlay-deploy", deployStartedAt)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if !targetContextVerified {
			return
		}

		var cleanupErrors []error
		operatorCleanupBlocked := false
		if overlayApplyAttempted {
			By("checking manager logs for RBAC errors after all specs")
			if err := timeE2EOperation("cleanup-rbac-log-scan", verifyNoManagerRBACErrors); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if mediaNamespaceCreated {
			By("removing MediaLibrary resources before uninstalling test storage")
			if err := timeE2EOperation("cleanup-medialibrary-total", cleanupMediaResources); err != nil {
				operatorCleanupBlocked = true
				cleanupErrors = append(cleanupErrors, err)
			} else {
				mediaNamespaceCreated = false
				mediaNamespace = ""
			}
		}

		By("removing e2e-only metrics access")
		metricsCleanup := utils.KindKubectl(kubectlDelete, "-f",
			"test/e2e/manifests/metrics-reader.yaml", ignoreNotFoundFlag)
		if err := timeE2EOperation("cleanup-metrics-access", func() error {
			_, err := utils.Run(metricsCleanup)
			return err
		}); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}

		if overlayApplyAttempted && operatorCleanupBlocked {
			cleanupErrors = append(cleanupErrors,
				errors.New("leaving the operator installed because MediaLibrary cleanup failed"))
		} else if overlayApplyAttempted {
			By("undeploying the manager and removing its CRDs and RBAC")
			overlayCleanup := utils.KindKubectl(kubectlDelete, "-k",
				"test/e2e/overlay", ignoreNotFoundFlag)
			if err := timeE2EOperation("cleanup-operator-overlay", func() error {
				_, err := utils.Run(overlayCleanup)
				return err
			}); err != nil {
				operatorCleanupBlocked = true
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if gatewayAPICRDInstalled && !operatorCleanupBlocked {
			By("removing the test-only Gateway API CRD")
			if err := timeE2EOperation("cleanup-gateway-api-crd", func() error {
				_, err := utils.Run(utils.KindKubectl(kubectlDelete, "crd",
					"httproutes.gateway.networking.k8s.io", ignoreNotFoundFlag, "--wait=true", "--timeout=2m"))
				return err
			}); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			} else {
				gatewayAPICRDInstalled = false
			}
		}
		reportE2ETiming("suite-including-teardown", e2eStartedAt)
		Expect(errors.Join(cleanupErrors...)).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			collectDiagnostics()
		}
	})

	Context("Operator", Label("operator"), func() {
		It("starts and remains healthy after cache synchronization", func() {
			By("waiting for the manager Deployment to become available")
			cmd := utils.KindKubectl("wait",
				"--for=condition=Available",
				"--timeout=3m",
				"deployment/"+managerDeployment,
				namespaceFlag, namespace,
			)
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("waiting for the manager pod to become ready")
			cmd = utils.KindKubectl("wait",
				"--for=condition=Ready",
				"--timeout=3m",
				"pod", "-l", managerSelector,
				namespaceFlag, namespace,
			)
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("waiting for both controllers to start workers after cache synchronization")
			Eventually(func(g Gomega) {
				logs, logErr := managerLogs()
				g.Expect(logErr).NotTo(HaveOccurred())
				g.Expect(controllerStartedWorkers(logs, "medialibrary")).To(BeTrue())
				g.Expect(controllerStartedWorkers(logs, "sonarr")).To(BeTrue())
			}, 3*time.Minute, time.Second).Should(Succeed())

			By("checking the manager does not restart after cache synchronization")
			Consistently(func(g Gomega) {
				podName, err := controllerPodName()
				g.Expect(err).NotTo(HaveOccurred())
				restarts, err := utils.Run(utils.KindKubectl(kubectlGet, "pod", podName,
					"-o", "jsonpath={.status.containerStatuses[0].restartCount}",
					namespaceFlag, namespace,
				))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(strings.TrimSpace(string(restarts))).To(Equal("0"))
			}, 10*time.Second, 2*time.Second).Should(Succeed())

			By("checking manager logs for RBAC errors")
			ExpectWithOffset(1, verifyNoManagerRBACErrors()).To(Succeed())
		})

		It("serves metrics to an authorized service account", func() {
			By("creating a service account bound to the metrics-reader role")
			_, err := utils.Run(utils.KindKubectl("apply", "-f",
				"test/e2e/manifests/metrics-reader.yaml"))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_, cleanupErr := utils.Run(utils.KindKubectl(kubectlDelete, "-f",
					"test/e2e/manifests/metrics-reader.yaml", ignoreNotFoundFlag))
				Expect(cleanupErr).NotTo(HaveOccurred())
			})

			By("requesting the HTTPS metrics endpoint from inside the cluster")
			DeferCleanup(func() {
				_, cleanupErr := utils.Run(utils.KindKubectl(kubectlDelete, "pod", "e2e-metrics-check",
					namespaceFlag, namespace, ignoreNotFoundFlag))
				Expect(cleanupErr).NotTo(HaveOccurred())
			})
			_, err = utils.Run(utils.KindKubectl("run", "e2e-metrics-check",
				"--image=curlimages/curl:8.12.1",
				"--restart=Never",
				"--attach=false",
				namespaceFlag, namespace,
				"--overrides", `{"apiVersion":"v1","kind":"Pod","spec":{"serviceAccountName":"operatarr-e2e-metrics-reader"}}`,
				"--command", "--",
				"sh", "-ec", `token=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
printf 'header = "Authorization: Bearer %s"\n' "$token" |
  curl --config - --insecure --fail --silent --show-error \
    --write-out '\nE2E_HTTP_STATUS:%{http_code}\n' \
    https://operatarr-controller-manager-metrics-service:8443/metrics`,
			))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			_, err = utils.Run(utils.KindKubectl("wait", "pod/e2e-metrics-check",
				"--for=jsonpath={.status.phase}=Succeeded", "--timeout=2m",
				namespaceFlag, namespace))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			output, err := utils.Run(utils.KindKubectl("logs", "e2e-metrics-check",
				namespaceFlag, namespace))
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			Expect(string(output)).To(ContainSubstring("# HELP"))
			Expect(string(output)).To(ContainSubstring("E2E_HTTP_STATUS:200"))
		})

		It("holds the leader-election lease", func() {
			By("checking the lease holder matches the manager pod")
			Eventually(func(g Gomega) {
				podName, err := controllerPodName()
				g.Expect(err).NotTo(HaveOccurred())

				holder, err := utils.Run(utils.KindKubectl(kubectlGet, "lease", leaderElectionName,
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
				cmd := utils.KindKubectl("wait",
					"--for=condition=Established",
					"--timeout=60s",
					"crd/"+name,
				)
				_, err := utils.Run(cmd)
				ExpectWithOffset(1, err).NotTo(HaveOccurred())
			}
		})
	})

	mediaLibrarySpecs()
	sonarrSpecs()
})

func verifyKindContext() error {
	output, err := utils.Run(exec.Command("kubectl", "config", "current-context"))
	if err != nil {
		return fmt.Errorf("unable to read current Kubernetes context: %w", err)
	}
	expected := "kind-" + utils.KindClusterName()
	current := strings.TrimSpace(string(output))
	if current != expected {
		return fmt.Errorf("refusing to run e2e tests in context %q; expected %q", current, expected)
	}
	return nil
}

func prepareImages() (result error) {
	preparationStartedAt := time.Now()
	defer reportE2ETiming("image-preparation-total", preparationStartedAt)

	previousManagerImage := strings.TrimSpace(os.Getenv(previousManagerEnv))
	if previousManagerImage != "" && previousManagerImage == managerImage {
		return fmt.Errorf("%s must differ from the current e2e image %q", previousManagerEnv, managerImage)
	}

	platform, err := utils.KindNodePlatform()
	if err != nil {
		return fmt.Errorf("unable to select e2e image platform: %w", err)
	}

	imageDir, err := os.MkdirTemp("", "operatarr-e2e-images-")
	if err != nil {
		return fmt.Errorf("unable to create temporary image directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(imageDir); err != nil {
			result = errors.Join(result, fmt.Errorf("unable to remove temporary image directory: %w", err))
		}
	}()

	args := []string{
		"e2e-images",
		"E2E_PLATFORM=" + platform,
		"E2E_IMAGE_DIR=" + imageDir,
	}
	if previousManagerImage != "" {
		args = append(args, "E2E_PREVIOUS_MANAGER_IMAGE="+previousManagerImage)
	}
	cmd := exec.Command("make", args...)
	buildStartedAt := time.Now()
	if _, err := utils.Run(cmd); err != nil {
		reportE2ETiming("image-build-and-save", buildStartedAt)
		return fmt.Errorf("unable to build e2e images: %w", err)
	}
	reportE2ETiming("image-build-and-save", buildStartedAt)

	archives := []string{"manager.tar", "jobrunner.tar", "sonarr.tar"}
	if previousManagerImage != "" {
		archives = append(archives, "previous-manager.tar")
	}
	loadStartedAt := time.Now()
	defer reportE2ETiming("kind-image-load", loadStartedAt)
	for _, archive := range archives {
		if err := utils.LoadImageArchiveToKindCluster(filepath.Join(imageDir, archive)); err != nil {
			return fmt.Errorf("unable to load %s into kind: %w", archive, err)
		}
	}
	return nil
}

func reportE2ETiming(stage string, startedAt time.Time) {
	fmt.Fprintf(GinkgoWriter, "E2E_TIMING stage=%s duration_ms=%d\n",
		stage, time.Since(startedAt).Milliseconds())
}

func timeE2EOperation(stage string, operation func() error) error {
	startedAt := time.Now()
	defer reportE2ETiming(stage, startedAt)
	return operation()
}

func controllerPodName() (string, error) {
	cmd := utils.KindKubectl(kubectlGet, podResource,
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

func managerLogs() ([]byte, error) {
	logs, err := utils.Run(utils.KindKubectl("logs",
		"deployment/"+managerDeployment,
		"--all-containers=true",
		namespaceFlag, namespace,
	))
	if err != nil {
		return nil, fmt.Errorf("unable to read manager logs: %w", err)
	}
	return logs, nil
}

func controllerStartedWorkers(logs []byte, controller string) bool {
	for _, line := range strings.Split(strings.ToLower(string(logs)), "\n") {
		if strings.Contains(line, "starting workers") && strings.Contains(line, controller) {
			return true
		}
	}
	return false
}

func verifyNoManagerRBACErrors() error {
	logs, err := managerLogs()
	if err != nil {
		return err
	}
	lowerLogs := strings.ToLower(string(logs))
	if strings.Contains(lowerLogs, "forbidden") || strings.Contains(lowerLogs, "cannot list resource") {
		return fmt.Errorf("manager logs contain an RBAC error")
	}
	return nil
}

func collectDiagnostics() {
	fmt.Fprintln(GinkgoWriter, "E2E failure diagnostics:")
	commands := [][]string{
		{kubectlGet, podResource, "-o", "wide", namespaceFlag, namespace},
		{"describe", podResource, "-l", managerSelector, namespaceFlag, namespace},
		{"logs", "deployment/" + managerDeployment, "--all-containers=true", namespaceFlag, namespace},
		{kubectlGet, "events", "--sort-by=.metadata.creationTimestamp", "-o", "json", namespaceFlag, namespace},
	}
	if mediaNamespace != "" {
		commands = append(commands,
			[]string{kubectlGet, "medialibraries,sonarrs,pvc,jobs,pods,services,statefulsets", "-o", "wide",
				namespaceFlag, mediaNamespace},
			[]string{"describe", podResource, namespaceFlag, mediaNamespace},
			[]string{"logs", "deployment/" + mediaNamespace, namespaceFlag, mediaNamespace},
			[]string{kubectlGet, "events", "--sort-by=.metadata.creationTimestamp", "-o", "json", namespaceFlag, mediaNamespace},
		)
	}
	for _, args := range commands {
		cmd := utils.KindKubectl(args...)
		fmt.Fprintf(GinkgoWriter, "$ %s\n", strings.Join(cmd.Args, " "))
		output, err := utils.Run(cmd)
		if len(output) > 0 {
			fmt.Fprintln(GinkgoWriter, string(output))
		}
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "diagnostic command failed: %v\n", err)
		}
	}
}
