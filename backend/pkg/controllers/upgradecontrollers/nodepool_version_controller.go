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

package upgradecontrollers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolVersionSyncer reads the node pool version from Cluster Service
// and actuates the ServiceProviderNodePool data in Cosmos.
type nodePoolVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolVersionSyncer)(nil)

// NewNodePoolVersionController creates a new syncer that reads node pool versions
// from Cluster Service.
func NewNodePoolVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &nodePoolVersionSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"NodePoolVersions",
		cosmosClient,
		informers,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// SyncOnce synchronizes node pool version information between Cluster Service
// and the ServiceProviderNodePool in Cosmos DB. It:
//   - Reads the actual running version from Cluster Service and stores it in
//     ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersion
//   - Reads the customer's desired version from HCPNodePool and stores it in
//     ServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
//
// This allows other controllers to watch ServiceProviderNodePool for version
// changes.
func (c *nodePoolVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	// Get node pool from Cosmos to get CS internal ID
	nodePool, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)

	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // nodepool doesn't exists
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cosmos: %w", err))
	}

	existingServiceProviderNodePool, err := controllerutils.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// Read node pool from Cluster Service
	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool from CS: %w", err))
	}

	// For now we get the CS desired version
	// In the future it should be good to use the node pool Status information from the node pool CR
	version, ok := csNodePool.GetVersion()
	if !ok {
		return utils.TrackError(fmt.Errorf("node pool version not found in Cluster Service response"))
	}

	serviceProviderCosmosNodePoolClient := c.cosmosClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	actualVersion := semver.MustParse(version.ID())

	// check if actualVersion from node pool in clusterService is different that the one in serviceProviderNodePool
	// if it is different update the ActualVersion in the serviceProviderNodePool
	if existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersion == nil ||
		!actualVersion.EQ(*existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersion) {
		existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersion = &actualVersion
		existingServiceProviderNodePool, err = serviceProviderCosmosNodePoolClient.Replace(ctx, existingServiceProviderNodePool, nil)

		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
		}
	}

	customerDesiredVersion := semver.MustParse(nodePool.Properties.Version.ID)
	// If the new customerDesired version is different for the serviceProvider version
	// update the serviceProviderNodePool DesiredVersion
	if existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion == nil ||
		!customerDesiredVersion.EQ(*existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion) {
		existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion = &customerDesiredVersion
		_, err = serviceProviderCosmosNodePoolClient.Replace(ctx, existingServiceProviderNodePool, nil)

		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
		}
	}

	return nil
}

func (c *nodePoolVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
