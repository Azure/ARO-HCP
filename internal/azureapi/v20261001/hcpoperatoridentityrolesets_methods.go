// Copyright 2026 Microsoft Corporation
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

package v20261001

import (
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/azureapi/v20261001/generated"
)

type HcpOperatorIdentityRoleSet struct {
	generated.HcpOperatorIdentityRoleSet
}

func (v version) NewOperatorIdentityRoleSet(from *coreapi.OperatorIdentityRoleSet) coreapi.VersionedOperatorIdentityRoleSet {
	idString := ""
	if from.ID != nil {
		idString = from.ID.String()
	}

	controlPlaneOperators := make([]*generated.OperatorIdentityRoles, len(from.Properties.ControlPlaneOperators))
	for i, operator := range from.Properties.ControlPlaneOperators {
		roleDefinitions := make([]*generated.RoleDefinition, len(operator.RoleDefinitions))
		for j, role := range operator.RoleDefinitions {
			roleDefinitions[j] = &generated.RoleDefinition{
				Name:       metadataapihelpers.PtrOrNil(role.Name),
				ResourceID: metadataapihelpers.PtrOrNil(role.ResourceID),
			}
		}
		controlPlaneOperators[i] = &generated.OperatorIdentityRoles{
			Name:            metadataapihelpers.PtrOrNil(operator.Name),
			Required:        (*generated.OperatorIdentityRequired)(metadataapihelpers.PtrOrNil(string(operator.Required))),
			RoleDefinitions: roleDefinitions,
		}
	}

	dataPlaneOperators := make([]*generated.OperatorIdentityRoles, len(from.Properties.DataPlaneOperators))
	for i, operator := range from.Properties.DataPlaneOperators {
		roleDefinitions := make([]*generated.RoleDefinition, len(operator.RoleDefinitions))
		for j, role := range operator.RoleDefinitions {
			roleDefinitions[j] = &generated.RoleDefinition{
				Name:       metadataapihelpers.PtrOrNil(role.Name),
				ResourceID: metadataapihelpers.PtrOrNil(role.ResourceID),
			}
		}
		dataPlaneOperators[i] = &generated.OperatorIdentityRoles{
			Name:            metadataapihelpers.PtrOrNil(operator.Name),
			Required:        (*generated.OperatorIdentityRequired)(metadataapihelpers.PtrOrNil(string(operator.Required))),
			RoleDefinitions: roleDefinitions,
		}
	}

	return &HcpOperatorIdentityRoleSet{
		generated.HcpOperatorIdentityRoleSet{
			ID:   metadataapihelpers.PtrOrNil(idString),
			Name: metadataapihelpers.PtrOrNil(from.Name),
			Type: metadataapihelpers.PtrOrNil(from.Type),
			Properties: &generated.HcpOperatorIdentityRoleSetProperties{
				ControlPlaneOperators: controlPlaneOperators,
				DataPlaneOperators:    dataPlaneOperators,
			},
		},
	}
}

func (v *HcpOperatorIdentityRoleSet) GetVersion() coreapi.Version {
	return versionedInterface
}
