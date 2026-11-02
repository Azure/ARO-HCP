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

package corecosmosstorage

import (
	"context"
	"fmt"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// newInitialServiceProviderCluster returns a new ServiceProviderCluster with
// the given resource ID as its parent. The resource ID is assumed to be a
// cluster resource ID.
// The returned value can be used to consistently initialize a new ServiceProviderCluster
func newInitialServiceProviderCluster(clusterResourceID *azcorearm.ResourceID) *coreapi.ServiceProviderCluster {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s", clusterResourceID.String(), coreapi.ServiceProviderClusterResourceTypeName, coreapi.ServiceProviderClusterResourceName)))
	return &coreapi.ServiceProviderCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
	}
}

// GetOrCreateServiceProviderCluster gets the singleton ServiceProviderCluster
// instance named `default` for the given cluster resource ID.
// Missing state is initialized atomically with the parent initialization marker,
// only before initialization or Cluster Service deletion dispatch. Unexpected
// provider loss and transaction conflicts return errors for reconciliation.
func GetOrCreateServiceProviderCluster(
	ctx context.Context, dbClient ResourcesDBClient, clusterResourceID *azcorearm.ResourceID,
	secondAttempt ...bool,
) (*coreapi.ServiceProviderCluster, error) {
	if !metadataapi.ResourceTypeEqual(clusterResourceID.ResourceType, coreapi.ClusterResourceType) {
		return nil, utils.TrackError(fmt.Errorf("expected resource type %s, got %s", coreapi.ClusterResourceType, clusterResourceID.ResourceType))
	}

	serviceProviderClustersDBClient := dbClient.ServiceProviderClusters(
		clusterResourceID.SubscriptionID,
		clusterResourceID.ResourceGroupName,
		clusterResourceID.Name,
	)

	existingServiceProviderCluster, err := serviceProviderClustersDBClient.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	switch {
	case err == nil:
		return existingServiceProviderCluster, nil
	case cosmosstorageutils.IsNotFoundError(err):
		// fall through
	default:
		return nil, utils.TrackError(err)
	}

	clusterCRUD := dbClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, clusterResourceID.Name)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if cluster.ServiceProviderProperties.ServiceProviderClusterInitialized || cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil {
		return nil, utils.TrackError(fmt.Errorf("provider state missing for initialized or deleting cluster %s", clusterResourceID))
	}
	initialServiceProviderCluster := newInitialServiceProviderCluster(clusterResourceID)
	cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = true
	transaction := dbClient.NewTransaction(cluster.GetPartitionKey())
	if _, err := clusterCRUD.AddReplaceToTransaction(ctx, transaction, cluster, nil); err != nil {
		return nil, err
	}
	if _, err := serviceProviderClustersDBClient.AddCreateToTransaction(ctx, transaction, initialServiceProviderCluster, nil); err != nil {
		return nil, err
	}
	if _, err := transaction.Execute(ctx, nil); err != nil {
		return nil, err
	}
	return serviceProviderClustersDBClient.Get(ctx, coreapi.ServiceProviderClusterResourceName)
}
