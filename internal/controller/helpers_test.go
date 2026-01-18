// filepath: /Users/florisfeddema/repos/operatarr/internal/controller/helpers_test.go
package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("helpers.mergeMap", func() {
	Context("when left is nil", func() {
		It("returns right (same underlying map) and preserves contents", func() {
			right := map[string]string{"a": "1"}
			result := mergeMap(nil, right)
			Expect(result).To(Equal(map[string]string{"a": "1"}))
			// mutate result; right should reflect change proving same underlying map
			result["b"] = "2"
			Expect(right).To(HaveKeyWithValue("b", "2"))
		})
		It("returns nil when both left and right are nil", func() {
			var left map[string]string
			var right map[string]string
			result := mergeMap(left, right)
			Expect(result).To(BeNil())
		})
	})

	Context("when right is nil", func() {
		It("returns left unchanged and allows mutation in place", func() {
			left := map[string]string{"x": "y"}
			result := mergeMap(left, nil)
			Expect(result).To(Equal(map[string]string{"x": "y"}))
			result["z"] = "1"
			Expect(left).To(HaveKeyWithValue("z", "1"))
		})
	})

	Context("when both maps are non-nil", func() {
		It("copies keys from right into left, overwriting duplicates, returns modified left", func() {
			left := map[string]string{"a": "1", "onlyLeft": "L"}
			right := map[string]string{"a": "2", "b": "3"}
			result := mergeMap(left, right)
			Expect(result).To(Equal(map[string]string{"a": "2", "b": "3", "onlyLeft": "L"}))
			// mutate result; left should reflect change
			result["c"] = "4"
			Expect(left).To(HaveKeyWithValue("c", "4"))
			// right should remain without new key since copy is one-way
			Expect(right).NotTo(HaveKey("c"))
		})

		It("is idempotent if right is empty", func() {
			left := map[string]string{"k": "v"}
			right := map[string]string{}
			result := mergeMap(left, right)
			Expect(result).To(Equal(map[string]string{"k": "v"}))
		})
	})
})

var _ = Describe("helpers constants", func() {
	It("have expected values", func() {
		Expect(finalizerName).To(Equal("operatarr.feddema.dev/finalizer"))
		Expect(typeAvailable).To(Equal("Available"))
		Expect(typeDegraded).To(Equal("Degraded"))
	})

	It("can be combined meaningfully", func() {
		combo := fmt.Sprintf("%s|%s", typeAvailable, typeDegraded)
		Expect(combo).To(ContainSubstring("Available"))
		Expect(combo).To(ContainSubstring("Degraded"))
	})
})
