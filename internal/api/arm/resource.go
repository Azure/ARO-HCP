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
	"maps"
	"slices"
	"time"
)

// Resource represents a basic ARM resource
type Resource struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Type       string      `json:"type,omitempty"`
	SystemData *SystemData `json:"systemData,omitempty"`
}

func (src *Resource) Copy(dst *Resource) {
	dst.ID = src.ID
	dst.Name = src.Name
	dst.Type = src.Type
	if src.SystemData == nil {
		dst.SystemData = nil
	} else {
		dst.SystemData = &SystemData{}
		src.SystemData.Copy(dst.SystemData)
	}
}

// TrackedResource represents a tracked ARM resource
type TrackedResource struct {
	Resource
	Location string            `json:"location,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

func (src *TrackedResource) Copy(dst *TrackedResource) {
	src.Resource.Copy(&dst.Resource)
	dst.Location = src.Location
	dst.Tags = maps.Clone(src.Tags)
}

// CreatedByType is the type of identity that created (or modified) the resource
type CreatedByType string

const (
	CreatedByTypeApplication     CreatedByType = "Application"
	CreatedByTypeKey             CreatedByType = "Key"
	CreatedByTypeManagedIdentity CreatedByType = "ManagedIdentity"
	CreatedByTypeUser            CreatedByType = "User"
)

// SystemData includes creation and modification metadata for resources
// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
type SystemData struct {
	// CreatedBy is a string identifier for the identity that created the resource
	CreatedBy string `json:"createdBy,omitempty"`
	// CreatedByType is the type of identity that created the resource: User, Application, ManagedIdentity
	CreatedByType CreatedByType `json:"createdByType,omitempty"      validate:"omitempty,enum_createdbytype"`
	// The timestamp of resource creation (UTC)
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	// LastModifiedBy is a string identifier for the identity that last modified the resource
	LastModifiedBy string `json:"lastModifiedBy,omitempty"`
	// LastModifiedByType is the type of identity that last modified the resource: User, Application, ManagedIdentity
	LastModifiedByType CreatedByType `json:"lastModifiedByType,omitempty" validate:"omitempty,enum_createdbytype"`
	// LastModifiedAt is the timestamp of resource last modification (UTC)
	LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`
}

func (src *SystemData) Copy(dst *SystemData) {
	dst.CreatedBy = src.CreatedBy
	dst.CreatedByType = src.CreatedByType
	if src.CreatedAt == nil {
		dst.CreatedAt = nil
	} else {
		t := time.Unix(src.CreatedAt.Unix(), 0)
		dst.CreatedAt = &t
	}
	dst.LastModifiedBy = src.LastModifiedBy
	dst.LastModifiedByType = src.LastModifiedByType
	if dst.LastModifiedAt == nil {
		dst.LastModifiedAt = nil
	} else {
		t := time.Unix(src.LastModifiedAt.Unix(), 0)
		dst.LastModifiedAt = &t
	}
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
	})
}
