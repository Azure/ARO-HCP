// Copyright 2025 Microsoft Corporation
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

package database

import (
	"encoding/json"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// ResourceDocument captures the mapping of an Azure resource ID
// to an internal resource ID (the OCM API path), as well as any
// ARM-specific metadata for the resource.
type ResourceDocument struct {
	ResourceID        *azcorearm.ResourceID       `json:"resourceId,omitempty"`
	InternalID        ocm.InternalID              `json:"internalId,omitempty"`
	ActiveOperationID string                      `json:"activeOperationId,omitempty"`
	ProvisioningState arm.ProvisioningState       `json:"provisioningState,omitempty"`
	Identity          *arm.ManagedServiceIdentity `json:"identity,omitempty"`
	SystemData        *arm.SystemData             `json:"systemData,omitempty"`
	Tags              map[string]string           `json:"tags,omitempty"`

	InternalState map[string]any `json:"internalState,omitempty"`
}

func NewResourceDocument(resourceID *azcorearm.ResourceID) *ResourceDocument {
	return &ResourceDocument{
		ResourceID: resourceID,
	}
}

// GetValidTypes returns the valid resource types for a ResourceDocument.
func (doc ResourceDocument) GetValidTypes() []string {
	return []string{
		api.ClusterResourceType.String(),
		api.NodePoolResourceType.String(),
		api.ExternalAuthResourceType.String(),
	}
}

func (o *ResourceDocument) SetResourceID(newResourceID *azcorearm.ResourceID) {
	if newResourceID == nil {
		panic("newResourceID cannot be nil")
	}
	if !strings.EqualFold(o.ResourceID.String(), newResourceID.String()) {
		panic(fmt.Sprintf("cannot change resource ID from %s to %s", o.ResourceID.String(), newResourceID.String()))
	}
	o.ResourceID = newResourceID
}

// ResourceDocumentStateFilter is used to remove unknown fields from ResourceDocumentProperties.
// Long-term, we want to reach a point where we store different types so we have full type-safety
// throughout the stack.
// Short-term, we want a low-touch modification that makes it safe to store new fields.
type ResourceDocumentStateFilter interface {
	// RemoveUnknownFields checks the customerDesiredState and serviceProviderState and removes unknown fields.
	// The simplest implementation is "remove everything" and the next simplest is round-tripping through JSON.
	RemoveUnknownFields(*ResourceDocument) error
}

const (
	ResourceDocumentJSONPathActiveOperationID = typedDocumentJSONPathProperties + "/activeOperationId"
	ResourceDocumentJSONPathProvisioningState = typedDocumentJSONPathProperties + "/provisioningState"
)

// ResourceDocumentPatchOperations represents a patch request for a ResourceDocument.
type ResourceDocumentPatchOperations struct {
	azcosmos.PatchOperations
}

// SetActiveOperationID appends a set or remove operation for the ActiveOperationID
// field, depending on whether activeOperationID is nil.
//
// Be careful when appending a remove patch operation as it is NOT idempotent.
// If the field to remove is not present in the Cosmos DB document, the entire
// patch request will fail with a "400 Bad Request" status code.
func (p *ResourceDocumentPatchOperations) SetActiveOperationID(activeOperationID *string) {
	if activeOperationID != nil {
		p.AppendSet(ResourceDocumentJSONPathActiveOperationID, *activeOperationID)
	} else {
		p.AppendRemove(ResourceDocumentJSONPathActiveOperationID)
	}
}

// SetProvisioningState appends a set operation for the ProvisioningState field.
func (p *ResourceDocumentPatchOperations) SetProvisioningState(provisioningState arm.ProvisioningState) {
	p.AppendSet(ResourceDocumentJSONPathProvisioningState, provisioningState)
}

// resourceDocumentMarshal returns the JSON encoding of typedDoc with innerDoc
// as the properties value. First, however, typedDocumentMarshal validates
// the type field in typeDoc against innerDoc to ensure compatibility. If
// validation fails, typedDocumentMarshal returns a typedDocumentError.
func resourceDocumentMarshal(typedDoc *TypedDocument, innerDoc *ResourceDocument, documentFilter ResourceDocumentStateFilter) ([]byte, error) {
	err := typedDoc.validateType(*innerDoc)
	if err != nil {
		return nil, err
	}

	if err := documentFilter.RemoveUnknownFields(innerDoc); err != nil {
		return nil, fmt.Errorf("failed to remove unknown fields from ResourceDocument: %w", err)
	}

	data, err := json.Marshal(innerDoc)
	if err != nil {
		return nil, err
	}

	typedDoc.Properties = data

	return json.Marshal(typedDoc)
}
