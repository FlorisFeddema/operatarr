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

	feddemadevv1alpha1 "github.com/FlorisFeddema/operatarr/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("Sonarr HTTP route validation", func() {
	DescribeTable("validates the hostname against whether routing is enabled",
		func(enabled bool, hostname string, valid bool) {
			ctx := context.Background()
			sonarr := &feddemadevv1alpha1.Sonarr{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sonarr-validation-",
					Namespace:    defaultNamespace,
				},
				Spec: feddemadevv1alpha1.SonarrSpec{
					PodSpec: feddemadevv1alpha1.SonarrPodTemplateSpec{
						Image: "sonarr:test",
						ConfigVolumeSpec: &feddemadevv1alpha1.ConfigVolumeSpec{
							Size: resource.MustParse("1Gi"),
						},
					},
					HttpRouteSpec: feddemadevv1alpha1.SonarrHttpRouteSpec{
						Enabled:   enabled,
						Hostname:  hostname,
						ParentRef: gatewayv1.ParentReference{Name: "test-gateway"},
					},
					MediaLibraryRef: corev1.ObjectReference{
						Kind: "MediaLibrary",
						Name: "test-library",
					},
				},
			}

			err := k8sClient.Create(ctx, sonarr)
			if err == nil {
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, sonarr)).To(Succeed())
				})
			}
			if valid {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("hostname must be set if httpRouteSpec.enabled is true")))
			}
		},
		Entry("accepts an enabled route with a hostname", true, "sonarr.example.com", true),
		Entry("rejects an enabled route without a hostname", true, "", false),
		Entry("accepts a disabled route without a hostname", false, "", true),
		Entry("accepts a disabled route with a hostname", false, "sonarr.example.com", true),
	)
})
