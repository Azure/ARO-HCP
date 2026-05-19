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
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolChildResourceCleanupController deletes direct child resources scoped
// under a NodePool (e.g. ManagementClusterContent documents) once the NodePool
// is marked for deletion and Cluster Service has confirmed the delete. Controller
// status documents (NodePoolControllerResourceType) are left alone -- the orphan
// scraper handles those after the NodePool document itself is removed.
type nodePoolChildResourceCleanupController struct {
	cooldownChecker   controllerutils.CooldownChecker
	nodePoolLister    listers.NodePoolLister
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*nodePoolChildResourceCleanupController)(nil)

func NewNodePoolChildResourceCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolChildResourceCleanupController{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:    nodePoolLister,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolChildResourceCleanupController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolChildResourceCleanupController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolChildResourceCleanupController) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID == nil
}

func (c *nodePoolChildResourceCleanupController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	nodePoolResourceID := key.GetResourceID()
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*nodePoolResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create untyped CRUD for node pool children: %w", err))
	}

	childIterator, err := untypedCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list node pool child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		if strings.EqualFold(childResource.ResourceType, api.NodePoolControllerResourceType.String()) {
			continue
		}

		if childResource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("child resource at cosmosID %q has no resourceID; refusing to delete", childResource.ID))
		}
		logger.Info("deleting child resource", "childResourceID", childResource.ResourceID)
		if err := untypedCRUD.Delete(ctx, childResource.ResourceID); err != nil {
			return utils.TrackError(err)
		}
	}
	if err := childIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	return nil
}
