// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kuberesources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestPodRequestsSwiftNIC(t *testing.T) {
	if PodRequestsSwiftNIC(nil) || PodRequestsSwiftNIC(&corev1.Pod{}) {
		t.Fatal("nil or empty pod must not request a NIC")
	}
	for _, init := range []bool{false, true} {
		for _, limit := range []bool{false, true} {
			container := corev1.Container{}
			resources := corev1.ResourceList{SwiftNICResourceName: resource.MustParse("1")}
			if limit {
				container.Resources.Limits = resources
			} else {
				container.Resources.Requests = resources
			}
			pod := &corev1.Pod{}
			if init {
				pod.Spec.InitContainers = []corev1.Container{container}
			} else {
				pod.Spec.Containers = []corev1.Container{container}
			}
			if !PodRequestsSwiftNIC(pod) {
				t.Errorf("missed init=%v limit=%v", init, limit)
			}
		}
	}
}
