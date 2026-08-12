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

package validationutils

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	azurecheckaccessv2client "github.com/Azure/checkaccess-v2-go-sdk/client"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// collectNotAllowedAndDeniedActions returns CheckAccessV2 decisions where access was not granted.
func collectNotAllowedAndDeniedActions(authDecisionsResponse []azurecheckaccessv2client.AuthorizationDecision) []*checkaccessv2AuthorizationDecisionData {
	var missingPermissions []*checkaccessv2AuthorizationDecisionData
	for _, authDecision := range authDecisionsResponse {
		if authDecision.AccessDecision == azurecheckaccessv2client.NotAllowed || authDecision.AccessDecision == azurecheckaccessv2client.Denied {
			missingPermissions = append(missingPermissions, &checkaccessv2AuthorizationDecisionData{
				ActionID:       authDecision.ActionId,
				IsDataAction:   authDecision.IsDataAction,
				AccessDecision: authDecision.AccessDecision,
			})
		}
	}

	return missingPermissions
}

// fetchRoleDefinitions fetches the role definitions for the given resource IDs.
func fetchRoleDefinitions(ctx context.Context, resourceIDs []*azcorearm.ResourceID, backendIdentityAzureCachedReaders *cachedreader.BackendIdentityAzureCachedReaders) ([]armauthorization.RoleDefinition, error) {
	roleDefinitions := make([]armauthorization.RoleDefinition, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		response, err := backendIdentityAzureCachedReaders.RoleDefinitionsCachedReader.GetCachedByID(ctx, resourceID.String(), nil)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to get role definition %q: %w", resourceID.String(), err))
		}
		roleDefinitions = append(roleDefinitions, response.RoleDefinition)
	}
	return roleDefinitions, nil
}
