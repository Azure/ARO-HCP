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

package kubeapplier

// ResourceReference identifies a single Kubernetes object on the management
// cluster. It is shared by every *Desire spec.targetItem (ApplyDesire,
// DeleteDesire, ReadDesire) so the kube-applier resolves the GVR and (if
// applicable) namespace + name without consulting a RESTMapper.
type ResourceReference struct {
	// Group is the API group of the target resource. Empty for the core API.
	Group string `json:"group"`
	// Version is the API version of the target resource (e.g. "v1", "v1beta1").
	// Required: the dynamic client builds URLs from it and has no fallback.
	Version string `json:"version"`
	// Resource is the lower-cased plural resource name (e.g. "configmaps").
	// This is the URL-path segment, not Kind; it intentionally avoids
	// RESTMapping logic in the controller.
	Resource string `json:"resource"`
	// Namespace is the namespace of the target Kubernetes resource. Leave
	// empty for cluster-scoped Kubernetes resources. Note: this is the
	// Kubernetes scope, not the Cosmos scope — every *Desire is itself
	// nested under an HCPOpenShiftCluster (and possibly a NodePool) on the
	// Cosmos side regardless of the value here.
	Namespace string `json:"namespace,omitempty"`
	// Name is the name of the target resource.
	Name string `json:"name"`
}
