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

package swiftpod

import (
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ownershipLabel = "node-mitigation.aro-hcp.azure.com/managed-by"

// Builds a UID- and resourceVersion-guarded ownership patch, rejecting another mitigation owner.
func ownershipPatch(meta metav1.ObjectMeta) ([]byte, error) {
	if owner := meta.Labels[ownershipLabel]; owner != "" && owner != ControllerName {
		return nil, fmt.Errorf("resource has another mitigation owner")
	}
	labels := make(map[string]string, len(meta.Labels)+1)
	for key, value := range meta.Labels {
		labels[key] = value
	}
	labels[ownershipLabel] = ControllerName
	return json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": meta.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": meta.ResourceVersion},
		{"op": "add", "path": "/metadata/labels", "value": labels},
	})
}
