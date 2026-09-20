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

import corev1 "k8s.io/api/core/v1"

// PodRequestsSwiftNIC includes limits because admission can copy an extended
// resource limit into requests. Init containers use the same delegated network.
func PodRequestsSwiftNIC(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for _, container := range containers {
			if _, ok := container.Resources.Requests[SwiftNICResourceName]; ok {
				return true
			}
			if _, ok := container.Resources.Limits[SwiftNICResourceName]; ok {
				return true
			}
		}
	}
	return false
}
