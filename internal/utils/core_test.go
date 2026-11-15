package utils

import (
	"errors"
	"testing"
)

func TestRunConcurrentlyPreservesAllCauses(t *testing.T) {
	first := errors.New("first failure")
	second := errors.New("second failure")
	err := RunConcurrently(
		func() error { return first },
		func() error { return nil },
		func() error { return second },
	)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("expected both errors, got %v", err)
	}
	for _, err := range []error{RunConcurrently(), RunConcurrently(func() error { return nil })} {
		if err != nil {
			t.Fatalf("expected nil without failures, got %v", err)
		}
	}
}
