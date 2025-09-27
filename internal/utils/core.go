package utils

import (
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)
import "errors"

// RunConcurrently runs a list of functions concurrently and returns a channel with their errors
func RunConcurrently(fnList ...func() error) error {
	errorList := make(chan error)
	wg := sync.WaitGroup{}

	// Run all the functions concurrently
	for _, fn := range fnList {
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorList <- fn()
		}()
	}

	// Close the output channel whenever all the functions completed
	go func() {
		wg.Wait()
		close(errorList)
	}()

	// Collect all the errors and return them as a single error
	if err := errors.Join(channelToSlice(errorList)...); err != nil {
		return err
	}
	return nil
}

func channelToSlice[T any](c chan T) []T {
	var list []T
	for value := range c {
		list = append(list, value)
	}
	return list
}

func MergeConditions(i *[]metav1.Condition, cond metav1.Condition) {
	existingConditions := *i
	now := metav1.Now()
	cond.LastTransitionTime = now

	for idx, c := range existingConditions {
		if c.Type == cond.Type {
			// Only update the condition if the status has changed
			if c.Status != cond.Status {
				existingConditions[idx] = cond
			}
			*i = existingConditions
			return
		}
	}
}
