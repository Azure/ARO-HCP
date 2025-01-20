package arm

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

// Represents to support the ManagedServiceIdentity ARM resource.
type ManagedServiceIdentity struct {
	PrincipalID            string                           `json:"principalId,omitempty"            visibility:"read"`
	TenantID               string                           `json:"tenantId,omitempty"               visibility:"read"`
	Type                   ManagedServiceIdentityType       `json:"type"                                               validate:"omitempty,enum_managedserviceidentitytype"`
	UserAssignedIdentities map[string]*UserAssignedIdentity `json:"userAssignedIdentities,omitempty"                   validate:"dive,keys,resource_id=Microsoft.ManagedIdentity/userAssignedIdentities,endkeys"`
}

// UserAssignedIdentity - User assigned identity properties https://azure.github.io/typespec-azure/docs/libraries/azure-resource-manager/reference/data-types/#Azure.ResourceManager.CommonTypes.UserAssignedIdentity
type UserAssignedIdentity struct {
	ClientID    *string `json:"clientId,omitempty"    visibility:"read"`
	PrincipalID *string `json:"principalId,omitempty" visibility:"read"`
}

type ManagedServiceIdentityType string

const (
	ManagedServiceIdentityTypeNone                       ManagedServiceIdentityType = "None"
	ManagedServiceIdentityTypeSystemAssigned             ManagedServiceIdentityType = "SystemAssigned"
	ManagedServiceIdentityTypeSystemAssignedUserAssigned ManagedServiceIdentityType = "SystemAssigned,UserAssigned"
	ManagedServiceIdentityTypeUserAssigned               ManagedServiceIdentityType = "UserAssigned"
)
