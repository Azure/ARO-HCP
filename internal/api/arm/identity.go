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
