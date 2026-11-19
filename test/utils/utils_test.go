package utils

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlatformForNodes(t *testing.T) {
	const invalidArchitecture = "invalid architecture"

	tests := []struct {
		name      string
		nodes     string
		platform  string
		wantError string
	}{
		{
			name: "arm64 node",
			nodes: `{"items":[` +
				`{"metadata":{"name":"node"},"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"arm64"}}}]}`,
			platform: "linux/arm64",
		},
		{
			name: "multiple amd64 nodes",
			nodes: `{"items":[{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"amd64"}}},` +
				`{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"amd64"}}}]}`,
			platform: "linux/amd64",
		},
		{
			name: "mixed architectures",
			nodes: `{"items":[{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"arm64"}}},` +
				`{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"amd64"}}}]}`,
			wantError: "different platforms",
		},
		{
			name:      "non-linux node",
			nodes:     `{"items":[{"status":{"nodeInfo":{"operatingSystem":"windows","architecture":"amd64"}}}]}`,
			wantError: "unsupported operating system",
		},
		{
			name:      "missing architecture",
			nodes:     `{"items":[{"status":{"nodeInfo":{"operatingSystem":"linux"}}}]}`,
			wantError: invalidArchitecture,
		},
		{
			name:      "invalid architecture",
			nodes:     `{"items":[{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"arm64;invalid"}}}]}`,
			wantError: invalidArchitecture,
		},
		{
			name:      "empty cluster",
			nodes:     `{"items":[]}`,
			wantError: "no nodes",
		},
		{
			name:      "invalid response",
			nodes:     `{`,
			wantError: "unable to decode kind nodes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, err := platformForNodes([]byte(tt.nodes))
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if platform != tt.platform {
				t.Fatalf("expected platform %q, got %q", tt.platform, platform)
			}
		})
	}
}

func TestKindKubectlUsesSelectedContext(t *testing.T) {
	for _, tt := range []struct {
		cluster string
		context string
	}{
		{cluster: "", context: "kind-operatarr-e2e"},
		{cluster: "custom-e2e", context: "kind-custom-e2e"},
	} {
		t.Run(tt.context, func(t *testing.T) {
			t.Setenv("KIND_CLUSTER", tt.cluster)
			got := KindKubectl("version").Args
			want := []string{"kubectl", "--context", tt.context, "version"}
			if !slices.Equal(got, want) {
				t.Fatalf("expected command %q, got %q", want, got)
			}
		})
	}
}

func TestKindNodePlatformReadsSelectedCluster(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  "--context kind-custom-e2e get nodes -o json")
    printf '%s' '{"items":[{"status":{"nodeInfo":{"operatingSystem":"linux","architecture":"amd64"}}}]}'
    ;;
  *) printf 'unexpected kubectl arguments\n' >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("KIND_CLUSTER", "custom-e2e")

	platform, err := KindNodePlatform()
	if err != nil {
		t.Fatal(err)
	}
	if platform != "linux/amd64" {
		t.Fatalf("expected the kind node platform, got %q", platform)
	}
}

func TestRunPreservesCommandFailure(t *testing.T) {
	output, err := Run(exec.Command("sh", "-c", "printf 'failure output'; exit 7"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("expected preserved exit error, got %v", err)
	}
	if string(output) != "failure output" || !strings.Contains(err.Error(), "failure output") {
		t.Fatalf("expected command output in result and error, got %q, %v", output, err)
	}
}

func TestRunDoesNotChangeProcessWorkingDirectory(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Run(exec.Command("pwd")); err != nil {
		t.Fatal(err)
	}

	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("Run changed process working directory from %q to %q", before, after)
	}
}
