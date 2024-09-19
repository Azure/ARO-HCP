package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"maps"
	"net/url"
	"time"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

// ResourceID is a wrappered ResourceID from azcore with text marshaling and unmarshaling methods.
type ResourceID struct {
	azcorearm.ResourceID
}

// ParseResourceID parses a string to an instance of ResourceID.
func ParseResourceID(id string) (*ResourceID, error) {
	newId, err := azcorearm.ParseResourceID(id)
	if err != nil {
		return nil, err
	}
	return &ResourceID{ResourceID: *newId}, nil
}

// GetParent returns the parent resource ID, if any. Handles the
// type-casting necessary to access the parent as a wrapper type.
func (id *ResourceID) GetParent() *ResourceID {
	var parent *ResourceID
	if id.Parent != nil {
		parent = &ResourceID{ResourceID: *id.Parent}
	}
	return parent
}

// MarshalText returns a textual representation of the ResourceID.
func (id *ResourceID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

// UnmarshalText decodes the textual representation of a ResourceID.
func (id *ResourceID) UnmarshalText(text []byte) error {
	newId, err := azcorearm.ParseResourceID(string(text))
	if err != nil {
		return err
	}
	id.ResourceID = *newId
	return nil
}

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

// PagedResponse is the response format for resource collection requests.
type PagedResponse struct {
	Value    []json.RawMessage `json:"value"`
	NextLink string            `json:"nextLink,omitempty"`
}

// AddValue adds a JSON encoded value to a PagedResponse.
func (r *PagedResponse) AddValue(value json.RawMessage) {
	r.Value = append(r.Value, value)
}

// SetNextLink sets NextLink to a URL with a $skipToken parameter.
// If skipToken is empty, the function does nothing and returns nil.
func (r *PagedResponse) SetNextLink(baseURL, skipToken string) error {
	if skipToken == "" {
		return nil
	}

	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return err
	}

	values := u.Query()
	values.Set("$skipToken", skipToken)
	u.RawQuery = values.Encode()

	r.NextLink = u.String()
	return nil
}
