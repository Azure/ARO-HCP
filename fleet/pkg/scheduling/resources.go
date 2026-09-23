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

// Package scheduling holds shared constants and helpers for management
// cluster scheduling logic used across fleet controllers.
package scheduling

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/Azure/ARO-HCP/internal/kuberesources"
)

// Resources returns the resource names relevant for management cluster
// scheduling decisions: cpu, memory, and SWIFT NICs.
func Resources() []corev1.ResourceName {
	return []corev1.ResourceName{
		corev1.ResourceCPU,
		corev1.ResourceMemory,
		kuberesources.SwiftNICResourceName,
	}
}
