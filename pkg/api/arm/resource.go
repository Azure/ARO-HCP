package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"maps"
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
type CreatedByType int

const (
	CreatedByTypeApplication CreatedByType = iota
	CreatedByTypeKey
	CreatedByTypeManagedIdentity
	CreatedByTypeUser

	createdByTypeLength // private, must be last
)

func (v CreatedByType) String() string {
	switch v {
	case CreatedByTypeApplication:
		return "Application"
	case CreatedByTypeKey:
		return "Key"
	case CreatedByTypeManagedIdentity:
		return "ManagedIdentity"
	case CreatedByTypeUser:
		return "User"
	default:
		return ""
	}
}

func (v CreatedByType) MarshalText() (text []byte, err error) {
	text = []byte(v.String())
	if len(text) == 0 {
		err = fmt.Errorf("Cannot marshal value %d", v)
	}
	return
}

func (v *CreatedByType) UnmarshalText(text []byte) error {
	for i := range createdByTypeLength {
		if i.String() == string(text) {
			*v = i
			return nil
		}
	}

	return fmt.Errorf("Cannot unmarshal '%s' to a %T enum value", string(text), *v)
}

// SystemData includes creation and modification metadata for resources
// See https://eng.ms/docs/products/arm/api_contracts/resourcesystemdata
type SystemData struct {
	CreatedBy          string        `json:"createdBy,omitempty"`
	CreatedByType      CreatedByType `json:"createdByType,omitempty"`
	CreatedAt          *time.Time    `json:"createdAt,omitempty"`
	LastModifiedBy     string        `json:"lastModifiedBy,omitempty"`
	LastModifiedByType CreatedByType `json:"lastModifiedByType,omitempty"`
	LastModifiedAt     *time.Time    `json:"lastModifiedAt,omitempty"`
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
type ProvisioningState int

const (
	// Terminal states, defined by ARM
	ProvisioningStateSucceeded ProvisioningState = iota
	ProvisioningStateFailed
	ProvisioningStateCanceled

	// Non-terminal states (TBD, these are from ARO-RP)
	ProvisioningStateAccepted
	ProvisioningStateDeleting
	ProvisioningStateProvisioning
	ProvisioningStateUpdating

	provisioningStateLength // private, must be last
)

func (v ProvisioningState) String() string {
	switch v {
	case ProvisioningStateSucceeded:
		return "Succeeded"
	case ProvisioningStateFailed:
		return "Failed"
	case ProvisioningStateCanceled:
		return "Canceled"
	case ProvisioningStateAccepted:
		return "Accepted"
	case ProvisioningStateDeleting:
		return "Deleting"
	case ProvisioningStateProvisioning:
		return "Provisioning"
	case ProvisioningStateUpdating:
		return "Updating"
	default:
		return ""
	}
}

func (v ProvisioningState) MarshalText() (text []byte, err error) {
	text = []byte(v.String())
	if len(text) == 0 {
		err = fmt.Errorf("Cannot marshal value %d", v)
	}
	return
}

func (v *ProvisioningState) UnmarshalText(text []byte) error {
	for i := range provisioningStateLength {
		if i.String() == string(text) {
			*v = i
			return nil
		}
	}

	return fmt.Errorf("Cannot unmarshal '%s' to a %T enum value", string(text), *v)
}
