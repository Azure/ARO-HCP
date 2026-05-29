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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolChildResourcesCleanupController deletes child resources scoped
// under a NodePool (e.g. ManagementClusterContent documents) recursively once
// the NodePool is marked for deletion and Cluster Service has confirmed the
// delete on its side. Controller status documents (NodePoolControllerResourceType)
// are left alone. The orphan scraper handles those after the NodePool document
// itself is removed.
type nodePoolChildResourcesCleanupController struct {
	cooldownChecker   controllerutil.CooldownChecker
	nodePoolLister    listers.NodePoolLister
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*nodePoolChildResourcesCleanupController)(nil)

func NewNodePoolChildResourcesCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolChildResourcesCleanupController{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:    nodePoolLister,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolChildResourcesCleanupController",
		resourcesDBClient,
		informers,
		time.Minute,
		syncer,
	)
}

func (c *nodePoolChildResourcesCleanupController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *nodePoolChildResourcesCleanupController) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool) bool {
	// TODO temporary check to skip the new deletion approach for NodePools that were created before the new approach was implemented.
	// This will be removed once all nodepools whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !nodePool.ServiceProviderProperties.UsesNewNodePoolDeletionApproach {
		return false
	}

	return nodePool.ServiceProviderProperties.DeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		nodePool.ServiceProviderProperties.ClusterServiceID == nil
}

func (c *nodePoolChildResourcesCleanupController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
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

	// extraDeleteGates is a map of resource types to extra delete gates that are used to determine if a resource should be deleted.
	// Keys are strings.ToLower(api resource type strings) so lookups match TypedDocument.resourceType regardless of casing.
	// The value of the map is a function that takes a context and a resource ID and returns a boolean indicating if the resource should be deleted, or
	// an error.
	// If the resource type is not in the map, the resource is deleted.
	extraDeleteGates := map[string]func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error){
		strings.ToLower(api.ServiceProviderNodePoolResourceType.String()): c.extraDeleteGateShouldDeleteServiceProviderNodePool,
		// We never delete node pool controllers here, as there might be controllers still running for the NodePool until the very
		// end of the deletion process
		strings.ToLower(api.NodePoolControllerResourceType.String()): func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error) { return false, nil },
	}

	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list node pool child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		if childResource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("child resource at cosmosID %q has no resourceID; refusing to delete", childResource.ID))
		}

		extraDeleteGate, ok := extraDeleteGates[strings.ToLower(childResource.ResourceType)]
		if ok {
			shouldDelete, err := extraDeleteGate(ctx, childResource.ResourceID)
			if err != nil {
				return utils.TrackError(err)
			}
			if !shouldDelete {
				continue
			}
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

// extraDeleteGateShouldDeleteServiceProviderNodePool is an extra delete gate that checks if the ServiceProviderNodePool has any Maestro readonly bundles.
// serviceProviderNodePoolResourceID is the child's ARM resource ID (serviceProviderNodePools/default).
// If there are bundles it returns false, otherwise it returns true.
func (c *nodePoolChildResourcesCleanupController) extraDeleteGateShouldDeleteServiceProviderNodePool(ctx context.Context, serviceProviderNodePoolResourceID *azcorearm.ResourceID) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if serviceProviderNodePoolResourceID.Parent == nil || serviceProviderNodePoolResourceID.Parent.Parent == nil {
		return false, utils.TrackError(fmt.Errorf(
			"service provider node pool resource ID missing cluster or node pool parent: %s",
			serviceProviderNodePoolResourceID.String()))
	}

	clusterName := serviceProviderNodePoolResourceID.Parent.Parent.Name
	nodePoolName := serviceProviderNodePoolResourceID.Parent.Name

	spnp, err := c.resourcesDBClient.ServiceProviderNodePools(serviceProviderNodePoolResourceID.SubscriptionID, serviceProviderNodePoolResourceID.ResourceGroupName, clusterName, nodePoolName).Get(ctx, api.ServiceProviderNodePoolResourceName)
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}

	if len(spnp.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for nodepool-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"serviceProviderNodePoolResourceID", spnp.ResourceID.String(), "remainingBundles", len(spnp.Status.MaestroReadonlyBundles))
		return false, nil
	}
	return true, nil
}
