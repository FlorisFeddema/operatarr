package utils

import (
	"errors"
	"sync"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ReturnObject struct {
	Status  metav1.ConditionStatus
	Type    string
	Reason  string
	Message string
	Error   error
}

type ReconcileResult struct {
	Success    bool
	Conditions []metav1.Condition
}

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
	return errors.Join(channelToSlice(errorList)...)
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

	for idx, c := range existingConditions {
		if c.Type == cond.Type {
			if c.Status == cond.Status {
				cond.LastTransitionTime = c.LastTransitionTime
			} else {
				cond.LastTransitionTime = now
			}
			existingConditions[idx] = cond
			return
		}
	}
	cond.LastTransitionTime = now
	*i = append(existingConditions, cond)
}

func MustParseResource(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(err)
	}
	return q
}
