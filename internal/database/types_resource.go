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
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// ResourceDocument captures the mapping of an Azure resource ID
// to an internal resource ID (the OCM API path), as well as any
// ARM-specific metadata for the resource.
type ResourceDocument struct {
	ResourceID        *azcorearm.ResourceID       `json:"resourceId,omitempty"`
	InternalID        *ocm.InternalID             `json:"internalId,omitempty"`
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
