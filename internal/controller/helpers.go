package controller

import (
	"maps"
)

const (
	finalizerName = "operatarr.feddema.dev/finalizer"

	typeAvailable = "Available"
	typeDegraded  = "Degraded"
)

func mergeMap(left, right map[string]string) map[string]string {
	if left == nil {
		return right
	}
	maps.Copy(left, right)
	return left
}
