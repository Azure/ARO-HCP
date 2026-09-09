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
	"strings"
	"testing"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apitesting/coreapitesting"
)

func TestRedactTypedDocument_RedactsSupportedResourceTypes(t *testing.T) {
	tests := []struct {
		name         string
		resourceID   string
		resourceType string
		newDocument  func() (any, *TypedDocument)
	}{
		{
			name:         "cluster",
			resourceID:   coreapitesting.TestClusterResourceID,
			resourceType: coreapi.ClusterResourceType.String(),
			newDocument: func() (any, *TypedDocument) {
				resourceID := mustParseResourceID(t, coreapitesting.TestClusterResourceID)
				createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
				obj := &coreapi.HCPOpenShiftCluster{
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: coreapi.ClusterResourceType.String(),
							SystemData: &coreapi.SystemData{
								CreatedBy:      "cluster-created-by",
								LastModifiedBy: "cluster-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, coreapi.ClusterResourceType.String(), obj)
			},
		},
		{
			name:         "nodepool",
			resourceID:   coreapitesting.TestNodePoolResourceID,
			resourceType: coreapi.NodePoolResourceType.String(),
			newDocument: func() (any, *TypedDocument) {
				resourceID := mustParseResourceID(t, coreapitesting.TestNodePoolResourceID)
				createdAt := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
				obj := &coreapi.HCPOpenShiftClusterNodePool{
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: coreapi.NodePoolResourceType.String(),
							SystemData: &coreapi.SystemData{
								CreatedBy:      "nodepool-created-by",
								LastModifiedBy: "nodepool-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, coreapi.NodePoolResourceType.String(), obj)
			},
		},
		{
			name:         "external-auth",
			resourceID:   coreapitesting.TestExternalAuthResourceID,
			resourceType: coreapi.ExternalAuthResourceType.String(),
			newDocument: func() (any, *TypedDocument) {
				resourceID := mustParseResourceID(t, coreapitesting.TestExternalAuthResourceID)
				createdAt := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
				obj := &coreapi.HCPOpenShiftClusterExternalAuth{
					ProxyResource: coreapi.ProxyResource{
						Resource: coreapi.Resource{
							ID:   resourceID,
							Name: resourceID.Name,
							Type: coreapi.ExternalAuthResourceType.String(),
							SystemData: &coreapi.SystemData{
								CreatedBy:      "external-auth-created-by",
								LastModifiedBy: "external-auth-last-modified-by",
								CreatedAt:      &createdAt,
							},
						},
					},
				}
				return obj, newTypedDocument(t, resourceID, coreapi.ExternalAuthResourceType.String(), obj)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectedObject, doc := tt.newDocument()
			originalProperties := append(json.RawMessage(nil), doc.Properties...)

			if err := RedactTypedDocument(doc); err != nil {
				t.Fatalf("RedactTypedDocument() error = %v", err)
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
	resourceID := mustParseResourceID(t, coreapitesting.TestClusterResourceID)
	doc := &TypedDocument{
		BaseDocument: BaseDocument{
			ID: resourceID.Name,
		},
		PartitionKey: resourceID.SubscriptionID,
		ResourceID:   resourceID,
		ResourceType: coreapi.ClusterResourceType.String(),
		Properties:   json.RawMessage(`{"systemData":{"createdBy":123}}`),
	}

	err := RedactTypedDocument(doc)
	if err == nil {
		t.Fatal("RedactTypedDocument() error = nil, want nested field type error")
	}
	if !strings.Contains(err.Error(), "failed to read systemData.createdBy") {
		t.Fatalf("error = %q, want it to mention createdBy read failure", err.Error())
	}
	if !strings.Contains(err.Error(), resourceID.String()) {
		t.Fatalf("error = %q, want it to include the resource ID", err.Error())
	}
}

func newTypedDocument(t *testing.T, resourceID *azcorearm.ResourceID, resourceType string, properties any) *TypedDocument {
	t.Helper()

	propertiesBytes, err := json.Marshal(properties)
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}

	return &TypedDocument{
		BaseDocument: BaseDocument{
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
