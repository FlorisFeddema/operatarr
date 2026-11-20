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

package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestManagerSchemeRegistersHTTPRoute(t *testing.T) {
	for _, kind := range []string{"HTTPRoute", "HTTPRouteList"} {
		gvk := schema.GroupVersionKind{
			Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Kind: kind,
		}
		if !scheme.Recognizes(gvk) {
			t.Errorf("manager scheme does not register %s", gvk)
		}
	}
}
