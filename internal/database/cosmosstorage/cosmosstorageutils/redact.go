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

package cosmosstorageutils

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const RedactStr = "REDACTED"

// RedactTypedDocument replaces the caller identities in the document's systemData with RedactStr.
// Every code path that logs an HCP cluster must call this first. On error the document is left
// untouched, so callers must not log it.
func RedactTypedDocument(d *TypedDocument) error {
	if d == nil {
		return fmt.Errorf("typed document is nil")
	}

	if len(d.Properties) == 0 {
		return nil
	}

	var props unstructured.Unstructured
	if err := json.Unmarshal(d.Properties, &props.Object); err != nil {
		return fmt.Errorf("failed to unmarshal typed document properties for %s: %w", resourceIDToString(d.ResourceID), err)
	}

	if _, found, err := unstructured.NestedString(props.Object, "systemData", "createdBy"); err != nil {
		return fmt.Errorf("failed to read systemData.createdBy for %s: %w", resourceIDToString(d.ResourceID), err)
	} else if found {
		if err := unstructured.SetNestedField(props.Object, RedactStr, "systemData", "createdBy"); err != nil {
			return fmt.Errorf("failed to set systemData.createdBy for %s: %w", resourceIDToString(d.ResourceID), err)
		}
	}

	if _, found, err := unstructured.NestedString(props.Object, "systemData", "lastModifiedBy"); err != nil {
		return fmt.Errorf("failed to read systemData.lastModifiedBy for %s: %w", resourceIDToString(d.ResourceID), err)
	} else if found {
		if err := unstructured.SetNestedField(props.Object, RedactStr, "systemData", "lastModifiedBy"); err != nil {
			return fmt.Errorf("failed to set systemData.lastModifiedBy for %s: %w", resourceIDToString(d.ResourceID), err)
		}
	}

	redactedProps, err := json.Marshal(props.Object)
	if err != nil {
		return fmt.Errorf("failed to marshal redacted typed document properties for %s: %w", resourceIDToString(d.ResourceID), err)
	}

	d.Properties = redactedProps
	return nil
}

func resourceIDToString(id *azcorearm.ResourceID) string {
	if id == nil {
		return "<missing>"
	}
	return id.String()
}
