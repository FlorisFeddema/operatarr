package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func getMediaLibraryPVC(c client.Client, ctx context.Context, ml feddemadevv1alpha1.MediaLibrary) (*corev1.PersistentVolumeClaim, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, client.ObjectKey{Name: *ml.Status.EffectivePVC, Namespace: ml.Namespace}, pvc); err != nil {
		return nil, fmt.Errorf("unable to get MediaLibrary PVC: %w", err)
	}
	return pvc, nil
}

func labelsForResource(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "operatarr",
	}
}

func getLibraryVolumeName(name string) string {
	return fmt.Sprintf("%s-media", name)
}

func getHeadlessServiceName(name string) string {
	return fmt.Sprintf("%s-headless", name)
}

// gvkAvailable returns true if the apiserver serves the provided GVK.
func gvkAvailable(c client.Client, gvk schema.GroupVersionKind) (bool, error) {
	_, err := c.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err == nil {
		return true, nil
	}
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	return false, err
}

func gatewayHTTPRouteAvailable(c client.Client) (bool, error) {
	available, err := gvkAvailable(c, schema.FromAPIVersionAndKind("gateway.networking.k8s.io/v1", "HTTPRoute"))
	if err != nil {
		return false, fmt.Errorf("error checking for HTTPRoute availability: %w", err)
	}
	return available, nil
}
