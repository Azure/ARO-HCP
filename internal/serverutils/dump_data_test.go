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

package serverutils

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
)

func TestRedactTypedDocument_RedactsSupportedResourceTypes(t *testing.T) {
	tests := []struct {
		name         string
		resourceID   string
		resourceType string
		newDocument  func() (any, *database.TypedDocument)
	}{
		{
			name:         "cluster",
			resourceID:   api.TestClusterResourceID,
			resourceType: api.ClusterResourceType.String(),
			newDocument: func() (any, *database.TypedDocument) {
				resourceID := mustParseResourceID(t, api.TestClusterResourceID)
				createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
				obj := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: api.ClusterResourceType.String(),
							SystemData: &arm.SystemData{
								CreatedBy:      "cluster-created-by",
								LastModifiedBy: "cluster-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, api.ClusterResourceType.String(), obj)
			},
		},
		{
			name:         "nodepool",
			resourceID:   api.TestNodePoolResourceID,
			resourceType: api.NodePoolResourceType.String(),
			newDocument: func() (any, *database.TypedDocument) {
				resourceID := mustParseResourceID(t, api.TestNodePoolResourceID)
				createdAt := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
				obj := &api.HCPOpenShiftClusterNodePool{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: api.NodePoolResourceType.String(),
							SystemData: &arm.SystemData{
								CreatedBy:      "nodepool-created-by",
								LastModifiedBy: "nodepool-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, api.NodePoolResourceType.String(), obj)
			},
		},
		{
			name:         "external-auth",
			resourceID:   api.TestExternalAuthResourceID,
			resourceType: api.ExternalAuthResourceType.String(),
			newDocument: func() (any, *database.TypedDocument) {
				resourceID := mustParseResourceID(t, api.TestExternalAuthResourceID)
				createdAt := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
				obj := &api.HCPOpenShiftClusterExternalAuth{
					ProxyResource: arm.ProxyResource{
						Resource: arm.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: api.ExternalAuthResourceType.String(),
							SystemData: &arm.SystemData{
								CreatedBy:      "external-auth-created-by",
								LastModifiedBy: "external-auth-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, api.ExternalAuthResourceType.String(), obj)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectedObject, doc := tt.newDocument()
			originalProperties := append(json.RawMessage(nil), doc.Properties...)

			if err := redactTypedDocument(doc); err != nil {
				t.Fatalf("redactTypedDocument() error = %v", err)
			}

			if doc.ResourceType != tt.resourceType {
				t.Fatalf("ResourceType = %q, want %q", doc.ResourceType, tt.resourceType)
			}

			if doc.ResourceID == nil {
				t.Fatal("ResourceID was nil")
			}
			if doc.ResourceID.String() != tt.resourceID {
				t.Fatalf("ResourceID = %q, want %q", doc.ResourceID.String(), tt.resourceID)
			}

			var raw map[string]any
			if err := json.Unmarshal(doc.Properties, &raw); err != nil {
				t.Fatalf("unmarshal redacted properties: %v", err)
			}

			systemData, ok := raw["systemData"].(map[string]any)
			if !ok {
				t.Fatalf("redacted properties missing systemData: %#v", raw)
			}
			if systemData["createdBy"] != RedactStr {
				t.Fatalf("systemData.createdBy = %v, want %q", systemData["createdBy"], RedactStr)
			}
			if systemData["lastModifiedBy"] != RedactStr {
				t.Fatalf("systemData.lastModifiedBy = %v, want %q", systemData["lastModifiedBy"], RedactStr)
			}

			if string(doc.Properties) == string(originalProperties) {
				t.Fatalf("redacted document properties matched original properties JSON")
			}

			if expectedObject == nil {
				t.Fatal("expected object was nil")
			}
		})
	}
}

func TestRedactTypedDocument_ReturnsNestedFieldTypeError(t *testing.T) {
	resourceID := mustParseResourceID(t, api.TestClusterResourceID)
	doc := &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: resourceID.Name,
		},
		PartitionKey: resourceID.SubscriptionID,
		ResourceID:   resourceID,
		ResourceType: api.ClusterResourceType.String(),
		Properties:   json.RawMessage(`{"systemData":{"createdBy":123}}`),
	}

	err := redactTypedDocument(doc)
	if err == nil {
		t.Fatal("redactTypedDocument() error = nil, want nested field type error")
	}
	if !strings.Contains(err.Error(), "failed to read systemData.createdBy") {
		t.Fatalf("error = %q, want it to mention createdBy read failure", err.Error())
	}
	if !strings.Contains(err.Error(), resourceID.String()) {
		t.Fatalf("error = %q, want it to include the resource ID", err.Error())
	}
}

func newTypedDocument(t *testing.T, resourceID *azcorearm.ResourceID, resourceType string, properties any) *database.TypedDocument {
	t.Helper()

	propertiesBytes, err := json.Marshal(properties)
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}

	return &database.TypedDocument{
		BaseDocument: database.BaseDocument{
			ID: resourceID.Name,
		},
		PartitionKey: resourceID.SubscriptionID,
		ResourceID:   resourceID,
		ResourceType: resourceType,
		Properties:   propertiesBytes,
	}
}

func mustParseResourceID(t *testing.T, resourceID string) *azcorearm.ResourceID {
	t.Helper()

	id, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		t.Fatalf("parse resource ID %q: %v", resourceID, err)
	}
	return id
}
