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

package v20240610preview

import (
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview/generated"
)

type HcpOperatorIdentityRoleSet struct {
	generated.HcpOperatorIdentityRoleSet
}

func NewHcpOperatorIdentityRoleSet(from *api.HcpOperatorIdentityRoleSet) *HcpOperatorIdentityRoleSet {
	controlPlaneOperators := make([]*generated.OperatorIdentityRoles, len(from.Properties.ControlPlaneOperators))
	for i, operator := range from.Properties.ControlPlaneOperators {
		roleDefinitions := make([]*generated.RoleDefinition, len(operator.RoleDefinitions))
		for j, role := range operator.RoleDefinitions {
			roleDefinitions[j] = &generated.RoleDefinition{
				Name:       api.PtrOrNil(role.Name),
				ResourceID: api.PtrOrNil(role.ResourceID),
			}
		}
		controlPlaneOperators[i] = &generated.OperatorIdentityRoles{
			Name:            api.PtrOrNil(operator.Name),
			Required:        (*generated.OperatorIdentityRequired)(api.PtrOrNil(string(operator.Required))),
			RoleDefinitions: roleDefinitions,
		}
	}

	dataPlaneOperators := make([]*generated.OperatorIdentityRoles, len(from.Properties.DataPlaneOperators))
	for i, operator := range from.Properties.DataPlaneOperators {
		roleDefinitions := make([]*generated.RoleDefinition, len(operator.RoleDefinitions))
		for j, role := range operator.RoleDefinitions {
			roleDefinitions[j] = &generated.RoleDefinition{
				Name:       api.PtrOrNil(role.Name),
				ResourceID: api.PtrOrNil(role.ResourceID),
			}
		}
		dataPlaneOperators[i] = &generated.OperatorIdentityRoles{
			Name:            api.PtrOrNil(operator.Name),
			Required:        (*generated.OperatorIdentityRequired)(api.PtrOrNil(string(operator.Required))),
			RoleDefinitions: roleDefinitions,
		}
	}

	return &HcpOperatorIdentityRoleSet{
		generated.HcpOperatorIdentityRoleSet{
			ID:   api.PtrOrNil(from.ID),
			Name: api.PtrOrNil(from.Name),
			Type: api.PtrOrNil(from.Type),
			Properties: &generated.HcpOperatorIdentityRoleSetProperties{
				ControlPlaneOperators: controlPlaneOperators,
				DataPlaneOperators:    dataPlaneOperators,
			},
		},
	}
}

func (v version) MarshalHcpOperatorIdentityRoleSet(from *api.HcpOperatorIdentityRoleSet) ([]byte, error) {
	return arm.MarshalJSON(NewHcpOperatorIdentityRoleSet(from))
}
