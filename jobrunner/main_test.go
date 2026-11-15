package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
)

func TestJobrunnerFailureExit(t *testing.T) {
	const helperEnv = "OPERATARR_TEST_JOBRUNNER_MAIN"
	if os.Getenv(helperEnv) == "1" {
		os.Args = []string{"jobrunner", "--mode=unsupported", "--zap-devel=false"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestJobrunnerFailureExit$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("expected failure exit code 1, got %v; output: %s", err, output)
	}
	var entry struct {
		Level   string `json:"level"`
		Logger  string `json:"logger"`
		Message string `json:"msg"`
		Error   string `json:"error"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &entry); err != nil {
		t.Fatalf("expected exactly one JSON log, got %s: %v", output, err)
	}
	if entry.Level != "error" || entry.Logger != "jobrunner" || entry.Message != "jobrunner failed" ||
		entry.Mode != "unsupported" || !strings.Contains(entry.Error, "unknown mode") {
		t.Fatalf("expected concrete structured boundary error, got %+v", entry)
	}
}

func TestRunUnknownModeReturnsErrorWithoutLogging(t *testing.T) {
	var logs []string
	log := funcr.NewJSON(func(output string) { logs = append(logs, output) }, funcr.Options{})
	err := run("unsupported", log)
	if err == nil || !strings.Contains(err.Error(), `"unsupported"`) {
		t.Fatalf("expected contextual unknown-mode error, got %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("only main should log returned errors: %v", logs)
	}
}
