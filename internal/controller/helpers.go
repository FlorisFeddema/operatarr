package controller

import (
	"maps"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	finalizerName = "operatarr.feddema.dev/finalizer"

	typeAvailable = "Available"
	typeDegraded  = "Degraded"
)

func mergeMap(left, right map[string]string) map[string]string {
	if left == nil {
		return right
	} else {
		maps.Copy(left, right)
	}
	return left
}

func setMergedLabelsAndAnnotations(temp, desired client.Object) {
	temp.SetAnnotations(mergeMap(temp.GetAnnotations(), desired.GetAnnotations()))
	temp.SetLabels(mergeMap(temp.GetLabels(), desired.GetLabels()))
}
