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

package nodepooldeletion

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDeletionController issues a Cosmos nodepool delete
// for the Node Pools that have their DeletionTimestamp and ClusteServiceDeletionTimestamp set,
// and their ClusterServiceID has been cleared.
type nodePoolDeletionController struct {
	cooldownChecker   controllerutils.CooldownChecker
	nodePoolLister    listers.NodePoolLister
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDeletionController)(nil)

func NewNodePoolDeletionController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolDeletionController{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:    nodePoolLister,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolDeletionController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// NeedsWork reports whether the deleter has unfinished business for the given
// NodePool. All the following conditions must be met:
// - DeletionTimestamp must be set
// - ClusterServiceDeletionTimestamp must be set
// - ClusterServiceID must be nil
func (c *nodePoolDeletionController) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID == nil
}

// SyncOnce calls Cosmos to delete the NodePool when the NeedsWork condition is met.
// Note: for now this controller only deletes the NodePool from Cosmos but we might end up placing
// the logic that deletes ManagementClusterContents scoped at the NodePool level as well as Maestro Bundles
// scoped at the NodePool level as well, and maybe even other conditions. Maybe we even decide it's just a coordinator
// waiting for the N conditions and other controllers handling the individual conditions with this one being reserved
// just for the coordination and the final Cosmos entry deletion.
func (c *nodePoolDeletionController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if !c.NeedsWork(cachedNodePool) {
		return nil
	}

	// Confirm against the live document. The cache can lag behind a write that
	// that modified one of the NeedsWork conditions.
	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	nodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}
	if !c.NeedsWork(nodePool) {
		return nil
	}

	logger.Info("deleting node pool from Cosmos")
	err = nodePoolCRUD.Delete(ctx, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete node pool from Cosmos: %w", err))
	}
	logger.Info("node pool deleted from Cosmos")

	return nil
}
