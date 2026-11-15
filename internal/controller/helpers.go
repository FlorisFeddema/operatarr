package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	finalizerName = "operatarr.feddema.dev/finalizer"

	mediaLibraryKind = "MediaLibrary"

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
	if ref.Kind != mediaLibraryKind {
		return nil, fmt.Errorf("unsupported media library reference kind %q", ref.Kind)
	}

	mediaLibrary := &feddemadevv1alpha1.MediaLibrary{}
	mediaLibraryKey := client.ObjectKey{
		Name:      ref.Name,
		Namespace: ref.Namespace,
	}
	if err := c.Get(ctx, mediaLibraryKey, mediaLibrary); err != nil {
		return nil, fmt.Errorf("unable to fetch MediaLibrary %s: %w", mediaLibraryKey, err)
	}
	return mediaLibrary, nil
}

func getMediaLibraryPVC(c client.Client, ctx context.Context, ml feddemadevv1alpha1.MediaLibrary) (*corev1.PersistentVolumeClaim, error) {
	if ml.Status.EffectivePVC == nil || *ml.Status.EffectivePVC == "" {
		return nil, errors.New("MediaLibrary has no effective PVC")
	}
	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Name: *ml.Status.EffectivePVC, Namespace: ml.Namespace}
	if err := c.Get(ctx, key, pvc); err != nil {
		return nil, fmt.Errorf("unable to get MediaLibrary PVC %s: %w", key, err)
	}
	return pvc, nil
}

func logResourceOperation(log logr.Logger, kind string, object client.Object, result controllerutil.OperationResult) {
	if result != controllerutil.OperationResultNone {
		log.Info("Resource reconciled", "kind", kind, "resource", client.ObjectKeyFromObject(object), "operation", result)
	}
}

func labelsForResource(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "operatarr",
	}
}

func effectiveMediaPermissions(permissions *feddemadevv1alpha1.MediaPermissions) feddemadevv1alpha1.MediaPermissions {
	effective := feddemadevv1alpha1.MediaPermissions{}
	if permissions != nil {
		effective = *permissions
	}

	if effective.RunAsUser == nil {
		effective.RunAsUser = new(int64(5000))
	}
	if effective.RunAsGroup == nil {
		effective.RunAsGroup = new(int64(5000))
	}
	if effective.FSGroup == nil {
		effective.FSGroup = new(int64(5000))
	}

	return effective
}

func getLibraryVolumeName(name string) string {
	return fmt.Sprintf("%s-media", name)
}

func getHeadlessServiceName(name string) string {
	return fmt.Sprintf("%s-headless", name)
}
