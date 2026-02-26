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

package arm

import (
	"iter"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// Resource represents a basic ARM resource
type Resource struct {
	ID         *azcorearm.ResourceID `json:"id,omitempty"`
	Name       string                `json:"name,omitempty"`
	Type       string                `json:"type,omitempty"`
	SystemData *SystemData           `json:"systemData,omitempty"`
}

// NewResource returns a Resource initialized from resourceID.
func NewResource(resourceID *azcorearm.ResourceID) Resource {
	var resource Resource

	if resourceID != nil {
		resource.ID = resourceID
		resource.Name = resourceID.Name
		resource.Type = resourceID.ResourceType.String()
	}

	return resource
}

// TrackedResource represents a tracked ARM resource
type TrackedResource struct {
	Resource
	Location string            `json:"location,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

// NewTrackedResource returns a TrackedResource initialized from resourceID.
func NewTrackedResource(resourceID *azcorearm.ResourceID, azureLocation string) TrackedResource {
	return TrackedResource{
		Resource: NewResource(resourceID),
		Location: azureLocation,
	}
}

// ProxyResource represents an ARM resource without location/tags
type ProxyResource struct {
	Resource
}

// NewProxyResource returns a ProxyResource initialized from resourceID.
func NewProxyResource(resourceID *azcorearm.ResourceID) ProxyResource {
	return ProxyResource{
		Resource: NewResource(resourceID),
	}
}

// CreatedByType is the type of identity that created (or modified) the resource
type CreatedByType string

const (
	CreatedByTypeApplication     CreatedByType = "Application"
	CreatedByTypeKey             CreatedByType = "Key"
	CreatedByTypeManagedIdentity CreatedByType = "ManagedIdentity"
	CreatedByTypeUser            CreatedByType = "User"
)

var (
	ValidCreatedByTypes = sets.New[CreatedByType](
		CreatedByTypeApplication,
		CreatedByTypeKey,
		CreatedByTypeManagedIdentity,
		CreatedByTypeUser)
)

// SystemData includes creation and modification metadata for resources
// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
type SystemData struct {
	// CreatedBy is a string identifier for the identity that created the resource
	CreatedBy string `json:"createdBy,omitempty"`
	// CreatedByType is the type of identity that created the resource: User, Application, ManagedIdentity
	CreatedByType CreatedByType `json:"createdByType,omitempty"`
	// The timestamp of resource creation (UTC)
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// LastModifiedBy is a string identifier for the identity that last modified the resource
	LastModifiedBy string `json:"lastModifiedBy,omitempty"`
	// LastModifiedByType is the type of identity that last modified the resource: User, Application, ManagedIdentity
	LastModifiedByType CreatedByType `json:"lastModifiedByType,omitempty"`
	// LastModifiedAt is the timestamp of resource last modification (UTC)
	LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`
}

// ProvisioningState represents the asynchronous provisioning state of an ARM resource
// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/async-api-reference.md#provisioningstate-property
type ProvisioningState string

const (
	// Terminal states, defined by ARM
	ProvisioningStateSucceeded ProvisioningState = "Succeeded"
	ProvisioningStateFailed    ProvisioningState = "Failed"
	ProvisioningStateCanceled  ProvisioningState = "Canceled"

	// Non-terminal states, defined by ARO-HCP
	ProvisioningStateAccepted     ProvisioningState = "Accepted"
	ProvisioningStateDeleting     ProvisioningState = "Deleting"
	ProvisioningStateProvisioning ProvisioningState = "Provisioning"
	ProvisioningStateUpdating     ProvisioningState = "Updating"

	// Exclusive to ExternalAuth
	ProvisioningStateAwaitingSecret ProvisioningState = "AwaitingSecret"
)

// IsTerminal returns true if the state is terminal.
func (s ProvisioningState) IsTerminal() bool {
	switch s {
	case ProvisioningStateSucceeded, ProvisioningStateFailed, ProvisioningStateCanceled:
		return true
	default:
		return false
	}
}

// ListProvisioningStates returns an iterator that yields all recognized
// ProvisioningState values. This function is intended as a test aid.
func ListProvisioningStates() iter.Seq[ProvisioningState] {
	return slices.Values([]ProvisioningState{
		ProvisioningStateSucceeded,
		ProvisioningStateFailed,
		ProvisioningStateCanceled,
		ProvisioningStateAccepted,
		ProvisioningStateDeleting,
		ProvisioningStateProvisioning,
		ProvisioningStateUpdating,
		ProvisioningStateAwaitingSecret,
	})
}
