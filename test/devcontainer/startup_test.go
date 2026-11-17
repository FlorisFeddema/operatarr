package devcontainer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupNodeCheck(t *testing.T) {
	tests := []struct {
		name        string
		hasConfig   bool
		kubectlExit string
		wantCall    bool
		wantOutput  string
	}{
		{
			name:        "ready cluster",
			hasConfig:   true,
			kubectlExit: "0",
			wantCall:    true,
			wantOutput:  "test-node Ready",
		},
		{
			name:        "unreachable cluster",
			hasConfig:   true,
			kubectlExit: "1",
			wantCall:    true,
			wantOutput:  "warning: unable to list kind cluster nodes",
		},
		{
			name:        "missing kubeconfig",
			kubectlExit: "0",
			wantOutput:  "warning: kind kubeconfig is missing or unreadable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			bin := filepath.Join(dir, "bin")
			kubeconfig := filepath.Join(home, ".kube", "operatarr-e2e")
			call := filepath.Join(dir, "kubectl-call")

			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			if tt.hasConfig {
				if err := os.MkdirAll(filepath.Dir(kubeconfig), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(kubeconfig, []byte("test"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			stubs := map[string]string{
				"getent":   "#!/bin/sh\nexit 0\n",
				"hostname": "#!/bin/sh\necho testhost\n",
				"sudo":     "#!/bin/sh\nexit 0\n",
				"kubectl": `#!/bin/sh
printf '%s\n%s\n' "$KUBECONFIG" "$*" > "$KUBECTL_CALL"
if [ "$KUBECTL_STATUS" -ne 0 ]; then
    echo "simulated API failure" >&2
    exit "$KUBECTL_STATUS"
fi
echo "test-node Ready"
`,
			}
			for name, content := range stubs {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.Command("sh", "../../.devcontainer/post-start.sh")
			cmd.Env = append(os.Environ(),
				"HOME="+home,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"KUBECONFIG=/another-cluster",
				"KUBECTL_CALL="+call,
				"KUBECTL_STATUS="+tt.kubectlExit,
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("startup failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), tt.wantOutput) {
				t.Fatalf("expected %q in startup output, got %q", tt.wantOutput, output)
			}
			if tt.kubectlExit != "0" && !strings.Contains(string(output), "simulated API failure") {
				t.Fatalf("kubectl error was not surfaced: %q", output)
			}

			recorded, err := os.ReadFile(call)
			if !tt.wantCall {
				if !os.IsNotExist(err) {
					t.Fatalf("kubectl unexpectedly called: %q, %v", recorded, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := kubeconfig + "\nget nodes --request-timeout=10s\n"
			if string(recorded) != want {
				t.Fatalf("unexpected kubectl invocation: got %q, want %q", recorded, want)
			}
		})
	}
}
