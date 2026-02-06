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
)

// newInitialServiceProviderCluster returns a new ServiceProviderCluster with
// the given resource ID as its parent. The resource ID is assumed to be a
// cluster resource ID.
// The returned value can be used to consistently initialize a new ServiceProviderCluster
func newInitialServiceProviderCluster(clusterResourceID *azcorearm.ResourceID) *api.ServiceProviderCluster {
	resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", clusterResourceID.String(), api.ServiceProviderClusterResourceTypeName, api.ServiceProviderClusterResourceName)))
	return &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: resourceID,
		},
		ResourceID: *resourceID,
	}
}

// GetOrCreateServiceProviderCluster gets the singleton ServiceProviderCluster
// instance named `default` for the given cluster resource ID.
// If it doesn't exist, it creates a new one.
func GetOrCreateServiceProviderCluster(
	ctx context.Context, dbClient database.DBClient, clusterResourceID *azcorearm.ResourceID,
) (*api.ServiceProviderCluster, error) {
	if clusterResourceID.ResourceType.String() != api.ClusterResourceType.String() {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", api.ClusterResourceType, clusterResourceID.ResourceType))
	}

	serviceProviderClustersDBClient := dbClient.ServiceProviderClusters(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)

	existingServiceProviderCluster, err := serviceProviderClustersDBClient.Get(ctx, api.ServiceProviderClusterResourceName)
	if err == nil {
		return existingServiceProviderCluster, nil
	}

	if !database.IsResponseError(err, http.StatusNotFound) {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	initialServiceProviderCluster := newInitialServiceProviderCluster(clusterResourceID)
	existingServiceProviderCluster, err = serviceProviderClustersDBClient.Create(ctx, initialServiceProviderCluster, nil)
	if err == nil {
		return existingServiceProviderCluster, nil
	}

	// We optimize here and if creation failed because it already exists, we try
	// to get again one last time.
	// According to the Cosmos DB API documentation, a HTTP 409 Conflict error
	// is returned when the item already exists: https://learn.microsoft.com/en-us/rest/api/cosmos-db/create-a-document#status-codes
	if !database.IsResponseError(err, http.StatusConflict) {
		return nil, utils.TrackError(fmt.Errorf("failed to create ServiceProviderCluster: %w", err))
	}

	existingServiceProviderCluster, err = serviceProviderClustersDBClient.Get(ctx, api.ServiceProviderClusterResourceName)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	return existingServiceProviderCluster, nil
}
