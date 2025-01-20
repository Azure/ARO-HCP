package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// Represents to support the ManagedServiceIdentity ARM resource.
type ManagedServiceIdentity struct {
	PrincipalID            string                           `json:"principalId,omitempty"`
	TenantID               string                           `json:"tenantId,omitempty"`
	Type                   ManagedServiceIdentityType       `json:"type"`
	UserAssignedIdentities map[string]*UserAssignedIdentity `json:"userAssignedIdentities,omitempty"`
}

// UserAssignedIdentity - User assigned identity properties https://azure.github.io/typespec-azure/docs/libraries/azure-resource-manager/reference/data-types/#Azure.ResourceManager.CommonTypes.UserAssignedIdentity
type UserAssignedIdentity struct {
	ClientID    *string
	PrincipalID *string
}

type ManagedServiceIdentityType string

const (
	ManagedServiceIdentityTypeNone                       ManagedServiceIdentityType = "None"
	ManagedServiceIdentityTypeSystemAssigned             ManagedServiceIdentityType = "SystemAssigned"
	ManagedServiceIdentityTypeSystemAssignedUserAssigned ManagedServiceIdentityType = "SystemAssigned,UserAssigned"
	ManagedServiceIdentityTypeUserAssigned               ManagedServiceIdentityType = "UserAssigned"
)
