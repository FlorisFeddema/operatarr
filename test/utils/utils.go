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

package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	corev1 "k8s.io/api/core/v1"
)

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) ([]byte, error) {
	dir, err := GetProjectDir()
	if err != nil {
		return nil, fmt.Errorf("unable to get project directory: %w", err)
	}
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %s\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed: %w; output: %s", command, err, string(output))
	}

	return output, nil
}

// KindClusterName returns the selected kind cluster name.
func KindClusterName() string {
	cluster := os.Getenv("KIND_CLUSTER")
	if cluster == "" {
		return "operatarr-e2e"
	}
	return cluster
}

// KindKubectl targets the selected kind context explicitly.
func KindKubectl(args ...string) *exec.Cmd {
	context := "kind-" + KindClusterName()
	return exec.Command("kubectl", append([]string{"--context", context}, args...)...)
}

// KindNodePlatform returns the common Linux platform of the kind cluster's nodes.
func KindNodePlatform() (string, error) {
	output, err := Run(KindKubectl("get", "nodes", "-o", "json"))
	if err != nil {
		return "", fmt.Errorf("unable to inspect kind nodes: %w", err)
	}
	return platformForNodes(output)
}

func platformForNodes(output []byte) (string, error) {
	var nodes corev1.NodeList
	if err := json.Unmarshal(output, &nodes); err != nil {
		return "", fmt.Errorf("unable to decode kind nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return "", errors.New("kind cluster has no nodes")
	}

	var platform string
	for _, node := range nodes.Items {
		info := node.Status.NodeInfo
		if info.OperatingSystem != "linux" {
			return "", fmt.Errorf("kind node %q has unsupported operating system %q", node.Name, info.OperatingSystem)
		}
		if info.Architecture == "" || strings.Trim(info.Architecture, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			return "", fmt.Errorf("kind node %q has invalid architecture %q", node.Name, info.Architecture)
		}
		nodePlatform := "linux/" + info.Architecture
		if platform != "" && platform != nodePlatform {
			return "", fmt.Errorf("kind nodes have different platforms: %s and %s", platform, nodePlatform)
		}
		platform = nodePlatform
	}
	return platform, nil
}

// LoadImageArchiveToKindCluster loads a saved image archive into the selected kind cluster.
func LoadImageArchiveToKindCluster(archive string) error {
	kindOptions := []string{"load", "image-archive", archive, "--name", KindClusterName()}
	cmd := exec.Command("kind", kindOptions...)
	_, err := Run(cmd)
	return err
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("unable to get current directory: %w", err)
	}
	for {
		_, err := os.Stat(filepath.Join(wd, "go.mod"))
		if err == nil {
			return wd, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("unable to inspect project marker in %q: %w", wd, err)
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", fmt.Errorf("unable to find project root from %q", wd)
		}
		wd = parent
	}
}
