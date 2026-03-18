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
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// managementClusterPlacementSyncer is a Cluster syncer that resolves which management cluster
// an HCP is placed on and writes the ManagementClusterID into the ServiceProviderCluster document.
// It fetches the provision shard from Cluster Service and resolves it to a ManagementCluster
// in CosmosDB via the management cluster lister.
// Once ManagementClusterID is set, it is immutable and the controller becomes a no-op for that cluster.
type managementClusterPlacementSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	serviceProviderClusterLister listers.ServiceProviderClusterLister
	clusterLister                listers.ClusterLister
	managementClusterLister      listers.ManagementClusterLister
	cosmosClient                 database.DBClient
	clusterServiceClient         ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*managementClusterPlacementSyncer)(nil)

// NewManagementClusterPlacementSyncController creates a new controller that syncs the
// management cluster placement from Cluster Service into the ServiceProviderCluster document.
// It resolves the CS provision shard to a ManagementCluster document in CosmosDB and
// sets ManagementClusterID on the ServiceProviderCluster.
func NewManagementClusterPlacementSyncController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	managementClusterLister listers.ManagementClusterLister,
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

func (c *managementClusterPlacementSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// needsWork checks if the ServiceProviderCluster still needs its ManagementClusterID resolved.
func (c *managementClusterPlacementSyncer) needsWork(spc *api.ServiceProviderCluster) bool {
	return spc.Status.ManagementClusterID == nil
}

// SyncOnce resolves the management cluster placement for a single HCP cluster.
// It fetches the provision shard from Cluster Service, resolves it to a ManagementCluster
// document in CosmosDB, and sets ManagementClusterID on the ServiceProviderCluster.
func (c *managementClusterPlacementSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedSPC, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// we'll be re-fired if it is created again
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if !c.needsWork(cachedSPC) {
		return nil
	}

	// Get the ServiceProviderCluster from Cosmos (live read)
	spcCRUD := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	existingSPC, err := spcCRUD.Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache
	if !c.needsWork(existingSPC) {
		return nil
	}

	// Get the cluster from cache to check if it has a CS ID to query
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if len(cachedCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	// Get the provision shard from Cluster Service
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, *cachedCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get provision shard from Cluster Service: %w", err))
	}

	if len(csCluster.ProvisionShard().HREF()) == 0 {
		// provision shard not yet allocated, retry later
		return nil
	}
	provisionShardID, err := api.NewInternalID(csCluster.ProvisionShard().HREF())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse provision shard href: %w", err))
	}

	// Resolve the provision shard to a management cluster in CosmosDB
	managementCluster, err := c.managementClusterLister.GetByCSProvisionShardID(ctx, provisionShardID.ID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to resolve provision shard %q to management cluster: %w", provisionShardID.Path(), err))
	}

	// Set the ManagementClusterID on the ServiceProviderCluster
	existingSPC.Status.ManagementClusterID = managementCluster.ResourceID

	if _, err := spcCRUD.Replace(ctx, existingSPC, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to update ServiceProviderCluster: %w", err))
	}

	logger.Info("synced management cluster placement",
		"managementClusterID", managementCluster.ResourceID.String(),
	)
	return nil
}
