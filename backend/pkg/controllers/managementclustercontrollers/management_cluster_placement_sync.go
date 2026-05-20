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

package managementclustercontrollers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// managementClusterPlacementSyncer resolves the management cluster an HCP runs on
// and updates the ServiceProviderCluster document with the ManagementClusterResourceID.
type managementClusterPlacementSyncer struct {
	cooldownChecker controllerutil.CooldownChecker

	serviceProviderClusterLister listers.ServiceProviderClusterLister
	clusterLister                listers.ClusterLister
	managementClusterLister      dblisters.ManagementClusterLister
	cosmosClient                 database.ResourcesDBClient
	clusterServiceClient         ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*managementClusterPlacementSyncer)(nil)

// NewManagementClusterPlacementSyncController creates a new controller that syncs the
// management cluster placement from Cluster Service into the ServiceProviderCluster document.
func NewManagementClusterPlacementSyncController(
	cosmosClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	managementClusterLister dblisters.ManagementClusterLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &managementClusterPlacementSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		managementClusterLister:      managementClusterLister,
		cosmosClient:                 cosmosClient,
		clusterServiceClient:         clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ManagementClusterPlacementSync",
		cosmosClient,
		informers,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)

	return controller
}

func (c *managementClusterPlacementSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// needsWork checks if the ServiceProviderCluster still needs its ManagementClusterResourceID resolved.
func (c *managementClusterPlacementSyncer) needsWork(spc *api.ServiceProviderCluster) bool {
	return spc.Status.ManagementClusterResourceID == nil
}

// SyncOnce resolves the management cluster placement for a single HCP cluster.
// It fetches the provision shard from Cluster Service, resolves it to a ManagementCluster
// document in CosmosDB, and sets ManagementClusterResourceID on the ServiceProviderCluster.
func (c *managementClusterPlacementSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedSPC, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		logger.V(1).Info("ServiceProviderCluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.needsWork(cachedSPC) {
		logger.V(1).Info("ServiceProviderCluster already has ManagementClusterResourceID, skipping")
		return nil
	}

	// Get the cluster from cache to check if it has a CS ID to query
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		logger.V(1).Info("Cluster not found in cache, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if cachedCluster.ServiceProviderProperties.ClusterServiceID == nil || len(cachedCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		logger.V(1).Info("Cluster has no ClusterServiceID, skipping")
		return nil
	}

	// Get the ServiceProviderCluster from Cosmos (live read)
	spcCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	existingSPC, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		logger.V(1).Info("ServiceProviderCluster not found in Cosmos, skipping")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache
	if !c.needsWork(existingSPC) {
		logger.V(1).Info("ServiceProviderCluster already has ManagementClusterResourceID (live read), skipping")
		return nil
	}

	// Get the provision shard from Cluster Service via the dedicated endpoint.
	csShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, *cachedCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get provision shard from Cluster Service: %w", err))
	}

	if len(csShard.HREF()) == 0 {
		logger.V(1).Info("Provision shard not yet allocated by Cluster Service, skipping")
		return nil
	}
	provisionShardID, err := api.NewInternalID(csShard.HREF())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse provision shard href: %w", err))
	}

	// Resolve the provision shard to a management cluster in CosmosDB
	managementCluster, err := c.managementClusterLister.GetByCSProvisionShardID(ctx, provisionShardID.ID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve provision shard %q to management cluster: %w", provisionShardID.Path(), err))
	}

	// Set the ManagementClusterResourceID on the ServiceProviderCluster
	existingSPC.Status.ManagementClusterResourceID = managementCluster.ResourceID

	if _, err := spcCRUD.Replace(ctx, existingSPC, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster: %w", err))
	}

	logger.Info("synced management cluster placement",
		"managementClusterID", managementCluster.ResourceID.String(),
	)
	return nil
}
