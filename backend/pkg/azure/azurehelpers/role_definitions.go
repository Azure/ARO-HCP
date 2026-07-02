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

package azurehelpers

import (
	"fmt"

	"k8s.io/utils/set"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// ActionsFromRoleDefinition returns allowed actions from a role definition's permissions.
func ActionsFromRoleDefinition(roleDefinition armauthorization.RoleDefinition) ([]string, error) {
	if roleDefinition.Properties == nil || roleDefinition.Properties.Permissions == nil {
		return nil, utils.TrackError(fmt.Errorf("role definition '%s' doesn't contain permissions", safeRoleDefinitionID(roleDefinition)))
	}

	var actions []string
	for _, permission := range roleDefinition.Properties.Permissions {
		for _, action := range permission.Actions {
			actions = append(actions, *action)
		}
	}

	return actions, nil
}

// DataActionsFromRoleDefinition returns allowed data actions from a role definition's permissions.
func DataActionsFromRoleDefinition(roleDefinition armauthorization.RoleDefinition) ([]string, error) {
	if roleDefinition.Properties == nil || roleDefinition.Properties.Permissions == nil {
		return nil, utils.TrackError(fmt.Errorf("role definition '%s' doesn't contain permissions", safeRoleDefinitionID(roleDefinition)))
	}

	var dataActions []string
	for _, permission := range roleDefinition.Properties.Permissions {
		for _, dataAction := range permission.DataActions {
			dataActions = append(dataActions, *dataAction)
		}
	}

	return dataActions, nil
}

// UnionActions returns the set union of allowed actions across role definitions.
func UnionActions(roleDefinitions []armauthorization.RoleDefinition) ([]string, error) {
	actionsUnion := set.Set[string]{}
	for i := range roleDefinitions {
		actions, err := ActionsFromRoleDefinition(roleDefinitions[i])
		if err != nil {
			return nil, utils.TrackError(err)
		}
		actionsUnion.Insert(actions...)
	}
	return actionsUnion.UnsortedList(), nil
}

// UnionDataActions returns the set union of allowed data actions across role definitions.
func UnionDataActions(roleDefinitions []armauthorization.RoleDefinition) ([]string, error) {
	dataActionsUnion := set.Set[string]{}
	for i := range roleDefinitions {
		dataActions, err := DataActionsFromRoleDefinition(roleDefinitions[i])
		if err != nil {
			return nil, utils.TrackError(err)
		}
		dataActionsUnion.Insert(dataActions...)
	}
	return dataActionsUnion.UnsortedList(), nil
}

func safeRoleDefinitionID(roleDefinition armauthorization.RoleDefinition) string {
	if roleDefinition.ID != nil {
		return *roleDefinition.ID
	}
	return "<unknown>"
}
