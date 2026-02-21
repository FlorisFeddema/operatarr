/*
Copyright 2025 Floris Feddema.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
)

var _ = Describe("MediaLibrary Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		medialibrary := &feddemadevv1alpha1.MediaLibrary{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MediaLibrary")
			err := k8sClient.Get(ctx, typeNamespacedName, medialibrary)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &feddemadevv1alpha1.MediaLibrary{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: feddemadevv1alpha1.MediaLibrarySpec{
						PVC: feddemadevv1alpha1.MediaLibraryPVC{
							Size: resource.MustParse("1Gi"),
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &feddemadevv1alpha1.MediaLibrary{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance MediaLibrary")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &MediaLibraryReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

// Helper to fetch condition by type for assertions
func getConditionByType(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

var _ = Describe("MediaLibrary Existing PVC Scenarios", func() {
	var (
		ctx        context.Context
		reconciler *MediaLibraryReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		reconciler = &MediaLibraryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	It("successfully reconciles a MediaLibrary referencing an existing RWX PVC", func() {
		mlName := "ml-existing-success"
		pvcName := "pvc-existing-success"
		ns := "default"

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: ns},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getConditionByType(updated.Status.Conditions, "Available")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("ExistingPVCFound"))
			g.Expect(updated.Status.EffectivePVC).NotTo(BeNil())
			g.Expect(*updated.Status.EffectivePVC).To(Equal(pvcName))
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		// Ensure no managed PVC with ML name exists
		managedPVC := &corev1.PersistentVolumeClaim{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, managedPVC)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("fails to reconcile when existing PVC lacks ReadWriteMany", func() {
		mlName := "ml-existing-bad-access"
		pvcName := "pvc-bad-access"
		ns := "default"

		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: ns},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Or(
			ContainSubstring("access mode invalid"),
			ContainSubstring("does not have ReadWriteMany"),
			ContainSubstring("pre-existing PVC access mode invalid"),
		))

		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getConditionByType(updated.Status.Conditions, "Available")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("ExistingPVCInvalidAccessMode"))
			g.Expect(updated.Status.EffectivePVC).To(BeNil())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())
	})

	It("fails to reconcile when referenced PVC does not exist", func() {
		mlName := "ml-existing-missing"
		pvcName := "nonexistent-pvc"
		ns := "default"

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch pre-existing PVC"))

		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getConditionByType(updated.Status.Conditions, "Available")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("ExistingPVCNotFound"))
			g.Expect(updated.Status.EffectivePVC).To(BeNil())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())
	})
})

var _ = Describe("MediaLibrary Managed PVC Scenarios", func() {
	var (
		ctx        context.Context
		reconciler *MediaLibraryReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		reconciler = &MediaLibraryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	It("resizes a managed PVC when MediaLibrary size increases", func() {
		mlName := "ml-managed-resize"
		ns := "default"

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi")}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		pvc := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("1Gi")))

		// Increase size
		Eventually(func(g Gomega) {
			current := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, current)).To(Succeed())
			current.Spec.PVC.Size = resource.MustParse("2Gi")
			g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("2Gi")))
	})

	It("merges and updates annotations on managed PVC across reconciles", func() {
		mlName := "ml-managed-annotations"
		ns := "default"

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi"), Annotations: map[string]string{"a": "1"}}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		pvc := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Annotations).To(HaveKeyWithValue("a", "1"))

		// Update annotations: modify existing and add new
		Eventually(func(g Gomega) {
			current := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, current)).To(Succeed())
			current.Spec.PVC.Annotations["a"] = "10"
			current.Spec.PVC.Annotations["b"] = "2"
			g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Annotations).To(HaveKeyWithValue("a", "10"))
		Expect(pvc.Annotations).To(HaveKeyWithValue("b", "2"))
	})

	It("rejects attempts to change StorageClassName for an existing managed PVC", func() {
		mlName := "ml-managed-sc-immutable"
		ns := "default"
		originalSC := "fast-storage"
		newSC := "slow-storage"

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi"), StorageClassName: &originalSC}},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		// Attempt to change StorageClass
		Eventually(func(g Gomega) {
			current := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, current)).To(Succeed())
			current.Spec.PVC.StorageClassName = &newSC
			g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("StorageClassName"))

		// Validate condition
		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getConditionByType(updated.Status.Conditions, "Available")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("StorageClassImmutable"))
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())

		// Ensure PVC still has original storage class
		pvc := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Spec.StorageClassName).NotTo(BeNil())
		Expect(*pvc.Spec.StorageClassName).To(Equal(originalSC))
	})
})
