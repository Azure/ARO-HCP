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

package database

import (
	"context"
	"fmt"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
)

// newInitialServiceProviderExternalAuth returns a new ServiceProviderExternalAuth with
// the given resource ID as its parent. The resource ID is assumed to be an
// external auth resource ID.
// The returned value can be used to consistently initialize a new ServiceProviderExternalAuth
func newInitialServiceProviderExternalAuth(eaResourceID *azcorearm.ResourceID) *api.ServiceProviderExternalAuth {
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", eaResourceID.String(), api.ServiceProviderExternalAuthResourceTypeName, api.ServiceProviderExternalAuthResourceName)))
	return &api.ServiceProviderExternalAuth{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

// GetOrCreateServiceProviderExternalAuth gets the singleton ServiceProviderExternalAuth
// instance named `default` for the given external auth resource ID.
// If it doesn't exist, it creates a new one.
func GetOrCreateServiceProviderExternalAuth(
	ctx context.Context, dbClient ResourcesDBClient, externalAuthResourceID *azcorearm.ResourceID,
) (*api.ServiceProviderExternalAuth, error) {
	if !armhelpers.ResourceTypeEqual(externalAuthResourceID.ResourceType, api.ExternalAuthResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.ExternalAuthResourceType, externalAuthResourceID.ResourceType))
	}

	serviceProviderExternalAuthsDBClient := dbClient.ServiceProviderExternalAuths(
		externalAuthResourceID.SubscriptionID,
		externalAuthResourceID.ResourceGroupName,
		externalAuthResourceID.Parent.Name,
		externalAuthResourceID.Name,
	)

	existingServiceProviderExternalAuth, err := serviceProviderExternalAuthsDBClient.Get(ctx, api.ServiceProviderExternalAuthResourceName)
	if err == nil {
		return existingServiceProviderExternalAuth, nil
	}

	if !IsNotFoundError(err) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth: %w", err))
	}

	initialServiceProviderExternalAuth := newInitialServiceProviderExternalAuth(externalAuthResourceID)
	existingServiceProviderExternalAuth, err = serviceProviderExternalAuthsDBClient.Create(ctx, initialServiceProviderExternalAuth, nil)
	if err == nil {
		return existingServiceProviderExternalAuth, nil
	}

	// We optimize here and if creation failed because it already exists, we try
	// to get again one last time.
	// According to the Cosmos DB API documentation, a HTTP 409 Conflict error
	// is returned when the item already exists: https://learn.microsoft.com/en-us/rest/api/cosmos-db/create-a-document#status-codes
	if !IsConflictError(err) {
		return nil, utils.TrackError(fmt.Errorf("failed to create ServiceProviderExternalAuth: %w", err))
	}

	existingServiceProviderExternalAuth, err = serviceProviderExternalAuthsDBClient.Get(ctx, api.ServiceProviderExternalAuthResourceName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderExternalAuth: %w", err))
	}

	return existingServiceProviderExternalAuth, nil
}
