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

package api

import (
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

// HcpOperatorIdentityRoleSet represents a location-based HCP Operator Identity Role Set resource.
type HcpOperatorIdentityRoleSet struct {
	arm.ProxyResource
	Properties HcpOperatorIdentityRoleSetProperties `json:"properties,omitempty"`
}

// HcpOperatorIdentityRoleSetProperties contains details of the HCP Operator Identity Role Set.
type HcpOperatorIdentityRoleSetProperties struct {
	ControlPlaneOperators []OperatorIdentityRoles `json:"controlPlaneOperators"`
	DataPlaneOperators    []OperatorIdentityRoles `json:"dataPlaneOperators"`
}

// OperatorIdentityRoles represents role definitions for a specific operator.
type OperatorIdentityRoles struct {
	Name            string                   `json:"name"`
	Required        OperatorIdentityRequired `json:"required"`
	RoleDefinitions []RoleDefinition         `json:"roleDefinitions"`
}

// OperatorIdentityRequired indicates if the identity is required.
type OperatorIdentityRequired string

const (
	// OperatorIdentityRequiredAlways indicates the identity is always required.
	OperatorIdentityRequiredAlways OperatorIdentityRequired = "Always"
	// OperatorIdentityRequiredOnEnablement indicates the identity is only required when functionality is enabled.
	OperatorIdentityRequiredOnEnablement OperatorIdentityRequired = "OnEnablement"
)

// RoleDefinition represents a single role definition required by a given operator.
type RoleDefinition struct {
	Name       string `json:"name"`
	ResourceID string `json:"resourceId"`
}
