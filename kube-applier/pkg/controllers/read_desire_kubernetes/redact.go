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

package read_desire_kubernetes

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
)

// safeSecretDataKeys lists the data keys that are safe to mirror into
// ReadDesire.status.kubeContent when the target is a v1 Secret. All other
// data keys are stripped before the object is persisted to Cosmos. This
// prevents private keys, passwords, and tokens from leaking into the
// service cluster's database.
var safeSecretDataKeys = map[string]struct{}{
	"tls.crt": {},
}

// isSecret returns true when the ResourceReference points at a core/v1 Secret.
func isSecret(ref kubeapplier.ResourceReference) bool {
	return ref.Group == "" && ref.Version == "v1" && ref.Resource == "secrets"
}

// redactSecret strips all unsafe fields from a Secret. Only metadata (name,
// namespace, etc.) and data keys listed in safeSecretDataKeys survive. The
// object is modified in place; callers must pass a deep copy if the original
// must be preserved (e.g. informer cache objects).
func redactSecret(obj *unstructured.Unstructured) {
	data, found, err := unstructured.NestedMap(obj.Object, "data")
	if found && err == nil {
		for key := range data {
			if _, safe := safeSecretDataKeys[key]; !safe {
				delete(data, key)
			}
		}
		if len(data) > 0 {
			_ = unstructured.SetNestedField(obj.Object, data, "data")
		} else {
			unstructured.RemoveNestedField(obj.Object, "data")
		}
	}

	// stringData is the write-only variant of data; the API server never
	// returns it, but strip it defensively.
	unstructured.RemoveNestedField(obj.Object, "stringData")
}
