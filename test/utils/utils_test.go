package utils

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
