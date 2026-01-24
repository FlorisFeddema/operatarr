package controller

import (
	"context"
	"errors"
	"maps"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
	}
	maps.Copy(left, right)
	return left
}

// getMediaLibraryFromRef fetches the MediaLibrary object based on the provided reference
func getMediaLibraryFromRef(ctx context.Context, c client.Client, ref corev1.ObjectReference) (*feddemadevv1alpha1.MediaLibrary, error) {
	if ref.Kind != "MediaLibrary" {
		return nil, errors.New("unsupported media library reference kind")
	}

	mediaLibrary := &feddemadevv1alpha1.MediaLibrary{}
	mediaLibraryKey := client.ObjectKey{
		Name:      ref.Name,
		Namespace: ref.Namespace,
	}
	if err := c.Get(ctx, mediaLibraryKey, mediaLibrary); err != nil {
		return nil, err
	}
	return mediaLibrary, nil
}
