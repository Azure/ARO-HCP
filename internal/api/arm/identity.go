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

import "k8s.io/apimachinery/pkg/util/sets"

// Represents to support the ManagedServiceIdentity ARM resource.
type ManagedServiceIdentity struct {
	PrincipalID            string                           `json:"principalId,omitempty"`
	TenantID               string                           `json:"tenantId,omitempty"`
	Type                   ManagedServiceIdentityType       `json:"type"`
	UserAssignedIdentities map[string]*UserAssignedIdentity `json:"userAssignedIdentities,omitempty"`
}

// UserAssignedIdentity - User assigned identity properties https://azure.github.io/typespec-azure/docs/libraries/azure-resource-manager/reference/data-types/#Azure.ResourceManager.CommonTypes.UserAssignedIdentity
type UserAssignedIdentity struct {
	ClientID    *string `json:"clientId,omitempty"`
	PrincipalID *string `json:"principalId,omitempty"`
}

type ManagedServiceIdentityType string

const (
	ManagedServiceIdentityTypeNone                       ManagedServiceIdentityType = "None"
	ManagedServiceIdentityTypeSystemAssigned             ManagedServiceIdentityType = "SystemAssigned"
	ManagedServiceIdentityTypeSystemAssignedUserAssigned ManagedServiceIdentityType = "SystemAssigned,UserAssigned"
	ManagedServiceIdentityTypeUserAssigned               ManagedServiceIdentityType = "UserAssigned"
)

var (
	ValidManagedServiceIdentityTypes = sets.New[ManagedServiceIdentityType](
		ManagedServiceIdentityTypeNone,
		ManagedServiceIdentityTypeSystemAssigned,
		ManagedServiceIdentityTypeSystemAssignedUserAssigned,
		ManagedServiceIdentityTypeUserAssigned)
)
