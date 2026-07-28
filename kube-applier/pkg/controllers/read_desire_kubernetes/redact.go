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

// unsafeAnnotations lists metadata annotations that can embed the full
// Secret manifest (including private keys and tokens). These are removed
// during redaction even though the rest of metadata is preserved.
var unsafeAnnotations = map[string]struct{}{
	"kubectl.kubernetes.io/last-applied-configuration": {},
}

// redactSecret strips all unsafe fields from a Secret. Structural fields
// (apiVersion, kind, type) and metadata are preserved, except for
// annotations that can embed the full Secret (e.g.
// kubectl.kubernetes.io/last-applied-configuration). In the data map, only
// keys listed in safeSecretDataKeys survive; on any error reading data the
// field is removed entirely (fail closed). The write-only stringData and the
// binaryData fields are always removed, since they can carry the same
// sensitive payloads as data but hold no key we mirror. The object is
// modified in place; callers must pass a deep copy if the original must be
// preserved (e.g. informer cache objects).
func redactSecret(obj *unstructured.Unstructured) {
	data, found, err := unstructured.NestedMap(obj.Object, "data")
	if err != nil {
		unstructured.RemoveNestedField(obj.Object, "data")
	} else if found {
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

	// binaryData can carry the same sensitive payloads as data (base64-encoded
	// binary values). We never mirror any key out of it, so remove it entirely
	// (fail closed) rather than risk leaking private keys or tokens.
	unstructured.RemoveNestedField(obj.Object, "binaryData")

	stripUnsafeAnnotations(obj)
}

// stripUnsafeAnnotations removes annotations that can embed a full Secret
// manifest. If the annotations map becomes empty it is removed entirely so
// the serialized object stays clean.
func stripUnsafeAnnotations(obj *unstructured.Unstructured) {
	annotations, found, err := unstructured.NestedStringMap(obj.Object, "metadata", "annotations")
	if err != nil {
		// annotations are present but not a strict map[string]string, so we
		// cannot safely inspect them. Remove the whole annotations field
		// rather than risk leaving an unsafe annotation (e.g.
		// kubectl.kubernetes.io/last-applied-configuration, which can embed
		// the full Secret) in place. Fail closed.
		unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
		return
	}
	if !found {
		return
	}
	for key := range unsafeAnnotations {
		delete(annotations, key)
	}
	if len(annotations) > 0 {
		_ = unstructured.SetNestedStringMap(obj.Object, annotations, "metadata", "annotations")
	} else {
		unstructured.RemoveNestedField(obj.Object, "metadata", "annotations")
	}
}
