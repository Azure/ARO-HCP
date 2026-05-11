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

package nodepoolpropertiescontroller

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

// nodePoolCustomerPropertiesMigrationController is a NodePool controller that migrates properties (customer properties)
// from cluster-service to cosmos DB. It uses the .platform.vmSize attribute to know that customerProperties are missing.
// Old records will lack those fields and once we read from cluster-service, we'll have the information we need.
type nodePoolCustomerPropertiesMigrationController struct {
	cooldownChecker controllerutils.CooldownChecker

	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolCustomerPropertiesMigrationController)(nil)

func NewNodePoolCustomerPropertiesMigrationController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()

	syncer := &nodePoolCustomerPropertiesMigrationController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"NodePoolCustomerPropertiesMigration",
		resourcesDBClient,
		informers,
		60*time.Minute, // Check every 60 minutes
		syncer,
	)

	return controller
}

func (c *nodePoolCustomerPropertiesMigrationController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolCustomerPropertiesMigrationController) NeedsWork(ctx context.Context, existingNodePool *api.HCPOpenShiftClusterNodePool) bool {
	// Check if we have a Clusters Service's NodePool service ID to query. We will lack this information for newly created records when we
	// transition to async Clusters Service's NodePool creation.
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return false
	}

	// We use .properties.platform.vmSize as the marker to know if customer properties
	// need to be migrated for the NodePool being processed.
	// .properties.platform.vmSize is a required attribute at ARM API level, so its
	// absence in Cosmos signals that the customer properties of the NodePool are not
	// migrated into Cosmos yet and we need to migrate them.
	needsVMSize := len(existingNodePool.Properties.Platform.VMSize) == 0
	return needsVMSize
}

func (c *nodePoolCustomerPropertiesMigrationController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		// we'll be re-fired if it is created again
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get nodePool from cache: %w", err))
	}
	if !c.NeedsWork(ctx, cachedNodePool) {
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		// if the values don't change, then we still have no work to do.
		return nil
	}

	// Get the nodePool from Cosmos
	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	existingNodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil // nodePool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get nodePool: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache and obviates the need to any work
	if !c.NeedsWork(ctx, existingNodePool) {
		return nil
	}

	// Fetch the NodePool from Cluster Service
	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, existingNodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get nodePool from Cluster Service: %w", err))
	}

	// Use ConvertCStoNodePool to convert the nodePool and extract the Properties (customer properties)
	convertedNodePool, err := ocm.ConvertCStoNodePool(existingNodePool.ID, existingNodePool.Location, csNodePool)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert nodePool from Cluster Service: %w", err))
	}

	// Update only the Properties from the converted nodePool
	existingNodePool.Properties = convertedNodePool.Properties

	// Write the updated nodePool back to Cosmos
	if _, err := nodePoolCRUD.Replace(ctx, existingNodePool, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace nodePool: %w", err))
	}

	logger.Info("migrated nodePool properties from Cluster Service to Cosmos")

	return nil
}
