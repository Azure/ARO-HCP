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
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDeletionController issues a Cosmos nodepool delete
// for the Node Pools that have their DeletionTimestamp and ClusteServiceDeletionTimestamp set,
// their ClusterServiceID has been cleared, and all nodepool-scoped Maestro readonly bundles
// have been deleted from the ServiceProviderNodePool.
type nodePoolDeletionController struct {
	cooldownChecker               controllerutil.CooldownChecker
	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
	resourcesDBClient             database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDeletionController)(nil)

func NewNodePoolDeletionController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()
	syncer := &nodePoolDeletionController{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		resourcesDBClient:             resourcesDBClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolDeletionController) CooldownChecker() controllerutil.CooldownChecker {
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

	// We do a quick check to see if the ServiceProviderNodePool has any Maestro readonly bundles.
	// If it does, we return early as we need to wait for the bundles to be deleted.
	cachedSPNP, spnpCacheErr := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if spnpCacheErr == nil && len(cachedSPNP.Status.MaestroReadonlyBundles) > 0 {
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

	// We do not proceed until we know that all the maestro readonly bundles have been eliminated
	preconditionMet, err := c.deletePreconditionAllMaestroNodePoolScopedReadonlyBundlesCleared(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// We do not proceed until we know that the cosmos child resources have been eliminated
	preconditionMet, err = c.deletePreconditionCosmosChildResourcesDeleted(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
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

// deletePreconditionAllMaestroNodePoolScopedReadonlyBundlesCleared checks if the ServiceProviderNodePool has any Maestro readonly bundles.
// If it does, it returns false, otherwise it returns true.
func (c *nodePoolDeletionController) deletePreconditionAllMaestroNodePoolScopedReadonlyBundlesCleared(ctx context.Context, key controllerutils.HCPNodePoolKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	spnpCRUD := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	spnp, spnpErr := spnpCRUD.Get(ctx, api.ServiceProviderNodePoolResourceName)
	if spnpErr != nil && !database.IsNotFoundError(spnpErr) {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", spnpErr))
	}
	if spnp != nil && len(spnp.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for nodepool-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"remainingBundles", len(spnp.Status.MaestroReadonlyBundles))
		return false, nil
	}
	return true, nil
}

// deletePreconditionCosmosChildResourcesDeleted checks if the cosmos child resources have been deleted.
// If they have, it returns true, otherwise it returns false.
// It ignores node pool controllers here, as there might be controllers still running for the NodePool until the very
// end of the deletion process.
func (c *nodePoolDeletionController) deletePreconditionCosmosChildResourcesDeleted(ctx context.Context, key controllerutils.HCPNodePoolKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	nodePoolResourceID := key.GetResourceID()
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*nodePoolResourceID)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to create untyped CRUD for child check: %w", err))
	}
	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		// We ignore node pool controllers here, as there might be controllers still running for the NodePool until the very
		// end of the deletion process
		if strings.EqualFold(childResource.ResourceType, api.NodePoolControllerResourceType.String()) {
			continue
		}
		logger.Info("child resource still exists, waiting for cleanup", "childResourceID", childResource.ResourceID)
		return false, nil
	}
	if err := childIterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("error iterating child resources: %w", err))
	}

	return true, nil
}
