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

package controllerutils

import (
	"context"
	"fmt"
	"net/http"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

// newInitialServiceProviderNodePool returns a new ServiceProviderNodePool with
// the given resource ID as its parent. The resource ID is assumed to be a
// node pool resource ID.
// The returned value can be used to consistently initialize a new ServiceProviderNodePool
func newInitialServiceProviderNodePool(npResourceID *azcorearm.ResourceID) *api.ServiceProviderNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", npResourceID.String(), api.ServiceProviderNodePoolResourceTypeName, api.ServiceProviderNodePoolResourceName)))
	return &api.ServiceProviderNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
	}
}

// GetOrCreateServiceProviderNodePool gets the singleton ServiceProviderNodePool
// instance named `default` for the given node pool resource ID.
// If it doesn't exist, it creates a new one.
func GetOrCreateServiceProviderNodePool(
	ctx context.Context, dbClient database.DBClient, nodePoolResourceID *azcorearm.ResourceID,
) (*api.ServiceProviderNodePool, error) {
	if !apihelpers.ResourceTypeEqual(nodePoolResourceID.ResourceType, api.NodePoolResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.NodePoolResourceType, nodePoolResourceID.ResourceType))
	}

	serviceProviderNodePoolsDBClient := dbClient.ServiceProviderNodePools(
		nodePoolResourceID.SubscriptionID,
		nodePoolResourceID.ResourceGroupName,
		nodePoolResourceID.Parent.Name,
		nodePoolResourceID.Name,
	)

	existingServiceProviderNodePool, err := serviceProviderNodePoolsDBClient.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if err == nil {
		return existingServiceProviderNodePool, nil
	}

	if !database.IsResponseError(err, http.StatusNotFound) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}

	initialServiceProviderNodePool := newInitialServiceProviderNodePool(nodePoolResourceID)
	existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Create(ctx, initialServiceProviderNodePool, nil)
	if err == nil {
		return existingServiceProviderNodePool, nil
	}

	// We optimize here and if creation failed because it already exists, we try
	// to get again one last time.
	// According to the Cosmos DB API documentation, a HTTP 409 Conflict error
	// is returned when the item already exists: https://learn.microsoft.com/en-us/rest/api/cosmos-db/create-a-document#status-codes
	if !database.IsResponseError(err, http.StatusConflict) {
		return nil, utils.TrackError(fmt.Errorf("failed to create ServiceProviderNodePool: %w", err))
	}

	existingServiceProviderNodePool, err = serviceProviderNodePoolsDBClient.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}

	return existingServiceProviderNodePool, nil
}
