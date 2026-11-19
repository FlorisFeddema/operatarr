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

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	"github.com/FlorisFeddema/operatarr/jobrunner/medialibrary"
)

const (
	defaultNamespace   = "default"
	testJobRunnerImage = "test:latest"
)

var _ = Describe("MediaLibrary Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: defaultNamespace,
		}
		medialibrary := &feddemadevv1alpha1.MediaLibrary{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MediaLibrary")
			err := k8sClient.Get(ctx, typeNamespacedName, medialibrary)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &feddemadevv1alpha1.MediaLibrary{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: defaultNamespace,
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

var _ = Describe("MediaLibrary initialization Job spec", func() {
	It("mounts the MediaLibrary PVC at the jobrunner library root", func() {
		effectivePVC := "library-pvc"
		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: "library", Namespace: defaultNamespace},
			Status:     feddemadevv1alpha1.MediaLibraryStatus{EffectivePVC: &effectivePVC},
		}

		podSpec := newPvcInitJob(ml, testJobRunnerImage).Spec.Template.Spec
		Expect(podSpec.Volumes).To(HaveLen(1))
		Expect(podSpec.Volumes[0].PersistentVolumeClaim).NotTo(BeNil())
		Expect(podSpec.Volumes[0].PersistentVolumeClaim.ClaimName).To(Equal(effectivePVC))
		Expect(podSpec.Containers).To(HaveLen(1))
		Expect(podSpec.Containers[0].VolumeMounts).To(ConsistOf(corev1.VolumeMount{
			Name:      podSpec.Volumes[0].Name,
			MountPath: medialibrary.DefaultLibraryRootPath,
		}))
		Expect(medialibrary.DefaultLibraryRootPath).To(Equal("/library"))
	})

	It("does not restrict the initialization Job to a particular architecture", func() {
		effectivePVC := "library-pvc"
		ml := &feddemadevv1alpha1.MediaLibrary{
			Status: feddemadevv1alpha1.MediaLibraryStatus{EffectivePVC: &effectivePVC},
		}

		podSpec := newPvcInitJob(ml, testJobRunnerImage).Spec.Template.Spec
		Expect(podSpec.NodeSelector).NotTo(HaveKey("kubernetes.io/arch"))
	})
})

var _ = Describe("MediaLibrary initialization Job status", func() {
	testJobCondition := func(name string, jobCondition batchv1.JobCondition, initialized bool, conditionStatus metav1.ConditionStatus, reason string) {
		ctx := context.Background()
		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: defaultNamespace},
			Spec: feddemadevv1alpha1.MediaLibrarySpec{
				PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi")},
			},
		}
		Expect(k8sClient.Create(ctx, ml)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ml)).To(Succeed()) })

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ml), ml)).To(Succeed())
		effectivePVC := ml.Name
		ml.Status.EffectivePVC = &effectivePVC
		Expect(k8sClient.Status().Update(ctx, ml)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ml), ml)).To(Succeed())

		job := newPvcInitJob(ml, testJobRunnerImage)
		Expect(k8sClient.Create(ctx, job)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, job)).To(Succeed()) })
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(job), job)).To(Succeed())
		now := metav1.Now()
		job.Status.StartTime = &now
		if jobCondition.Type == batchv1.JobComplete {
			job.Status.CompletionTime = &now
			job.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobConditionType("SuccessCriteriaMet"), Status: corev1.ConditionTrue},
				jobCondition,
			}
		} else {
			job.Status.Conditions = []batchv1.JobCondition{
				{Type: batchv1.JobConditionType("FailureTarget"), Status: corev1.ConditionTrue},
				jobCondition,
			}
		}
		Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

		reconciler := &MediaLibraryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		state := &mediaLibraryReconcile{
			MediaLibraryReconciler: *reconciler,
			ctx:                    ctx,
			log:                    logr.Discard(),
			object:                 *ml,
		}
		Expect(initializeJob(state)).To(Succeed())

		updated := &feddemadevv1alpha1.MediaLibrary{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ml), updated)).To(Succeed())
		Expect(updated.Status.Initialized).To(Equal(initialized))
		condition := getAvailableCondition(updated.Status.Conditions)
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(conditionStatus))
		Expect(condition.Reason).To(Equal(reason))
	}

	It("marks the library initialized when the initialization Job succeeds", func() {
		testJobCondition("ml-job-success", batchv1.JobCondition{
			Type:   batchv1.JobComplete,
			Status: corev1.ConditionTrue,
		}, true, metav1.ConditionTrue, "PVCInitialized")
	})

	It("keeps the library uninitialized when the initialization Job fails", func() {
		testJobCondition("ml-job-failure", batchv1.JobCondition{
			Type:    batchv1.JobFailed,
			Status:  corev1.ConditionTrue,
			Message: "initialization failed",
		}, false, metav1.ConditionFalse, "PVCInitializationFailed")
	})
})

// Helper to fetch condition by type for assertions
func getAvailableCondition(conds []metav1.Condition) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == typeAvailable {
			return &conds[i]
		}
	}
	return nil
}

func createMediaLibraryWithFinalizer(ctx context.Context, reconciler *MediaLibraryReconciler, ml *feddemadevv1alpha1.MediaLibrary) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, ml)).To(Succeed())

	_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: ml.Name, Namespace: ml.Namespace}})
	Expect(err).NotTo(HaveOccurred())

	current := &feddemadevv1alpha1.MediaLibrary{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ml.Name, Namespace: ml.Namespace}, current)).To(Succeed())
	Expect(current.Finalizers).To(ContainElement(finalizerName))
}

var _ = Describe("MediaLibrary finalization", func() {
	It("persists finalizer removal without deleting a referenced PVC", func() {
		ctx := context.Background()
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "finalization-existing-pvc", Namespace: defaultNamespace},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pvc))).To(Succeed()) })

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: "finalization-library", Namespace: defaultNamespace},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvc.Name}},
		}
		reconciler := &MediaLibraryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

		Expect(k8sClient.Delete(ctx, ml)).To(Succeed())
		req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(ml)}
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, req.NamespacedName, &feddemadevv1alpha1.MediaLibrary{}))
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), &corev1.PersistentVolumeClaim{})).To(Succeed())

		_, err = reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
	})
})

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
		ns := defaultNamespace

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
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getAvailableCondition(updated.Status.Conditions)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("ExistingPVCFound"))
			g.Expect(updated.Status.Initialized).To(BeTrue())
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
		ns := defaultNamespace

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
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

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
			cond := getAvailableCondition(updated.Status.Conditions)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("ExistingPVCInvalidAccessMode"))
			g.Expect(updated.Status.Initialized).To(BeFalse())
			g.Expect(updated.Status.EffectivePVC).To(BeNil())
		}).WithTimeout(5 * time.Second).WithPolling(200 * time.Millisecond).Should(Succeed())
	})

	It("fails to reconcile when referenced PVC does not exist", func() {
		mlName := "ml-existing-missing"
		pvcName := "nonexistent-pvc"
		ns := defaultNamespace

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{PVCName: &pvcName}},
		}
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch pre-existing PVC"))

		Eventually(func(g Gomega) {
			updated := &feddemadevv1alpha1.MediaLibrary{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, updated)).To(Succeed())
			cond := getAvailableCondition(updated.Status.Conditions)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("ExistingPVCNotFound"))
			g.Expect(updated.Status.Initialized).To(BeFalse())
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
		reconciler = &MediaLibraryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), JobRunnerImage: testJobRunnerImage}
	})

	It("resizes a managed PVC when MediaLibrary size increases", func() {
		mlName := "ml-managed-resize"
		ns := defaultNamespace

		allowExpansion := true
		storageClass := &storagev1.StorageClass{
			ObjectMeta:           metav1.ObjectMeta{Name: mlName + "-storage"},
			Provisioner:          "test.example.com/provisioner",
			AllowVolumeExpansion: &allowExpansion,
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, storageClass)).To(Succeed())
		})

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec: feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{
				Size:             resource.MustParse("1Gi"),
				StorageClassName: &storageClass.Name,
			}},
		}
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		pvc := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("1Gi")))

		// Envtest has no volume controller to mark claims as bound before resizing.
		pvc.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, pvc)).To(Succeed())

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
		ns := defaultNamespace

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi"), Annotations: map[string]string{"a": "1"}}},
		}
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

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
		ns := defaultNamespace
		originalSC := "fast-storage"
		newSC := "slow-storage"

		ml := &feddemadevv1alpha1.MediaLibrary{
			ObjectMeta: metav1.ObjectMeta{Name: mlName, Namespace: ns},
			Spec:       feddemadevv1alpha1.MediaLibrarySpec{PVC: feddemadevv1alpha1.MediaLibraryPVC{Size: resource.MustParse("1Gi"), StorageClassName: &originalSC}},
		}
		createMediaLibraryWithFinalizer(ctx, reconciler, ml)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: mlName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		current := &feddemadevv1alpha1.MediaLibrary{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, current)).To(Succeed())
		current.Spec.PVC.StorageClassName = &newSC
		err = k8sClient.Update(ctx, current)
		Expect(apierrors.IsInvalid(err)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("Value is immutable")))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, current)).To(Succeed())
		Expect(current.Spec.PVC.StorageClassName).To(Equal(&originalSC))

		// Ensure PVC still has original storage class
		pvc := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mlName, Namespace: ns}, pvc)).To(Succeed())
		Expect(pvc.Spec.StorageClassName).NotTo(BeNil())
		Expect(*pvc.Spec.StorageClassName).To(Equal(originalSC))
	})
})
