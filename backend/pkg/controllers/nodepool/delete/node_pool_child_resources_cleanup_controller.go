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

package delete

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
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolChildResourcesCleanupController deletes child resources scoped
// under a NodePool (e.g. ManagementClusterContent documents) recursively once
// the NodePool is marked for deletion and Cluster Service has confirmed the
// delete on its side. Controller status documents (NodePoolControllerResourceType)
// are left alone. Nodepool-scoped kube-applier *Desires are deleted here using
// the parent cluster's ServiceProviderCluster placement. The orphan scraper
// handles controller status after the NodePool document itself is removed.
type nodePoolChildResourcesCleanupController struct {
	cooldownChecker      controllerutil.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

var _ controllerutils.NodePoolSyncer = (*nodePoolChildResourcesCleanupController)(nil)

func NewNodePoolChildResourcesCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolChildResourcesCleanupController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolChildResourcesCleanupController",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
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
	if err := c.ensureNodePoolScopedKubeApplierResourcesDeleted(ctx, nodePoolResourceID); err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete nodepool-scoped kube-applier content: %w", err))
	}

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

	logger.Info("all included nodepool child resources deleted")

	return nil
}

// extraDeleteGateShouldDeleteServiceProviderNodePool returns false while the
// ServiceProviderNodePool still has Maestro readonly bundles or nodepool-scoped
// kube-applier *Desire documents.
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

	// Check if there are any Maestro readonly bundles remaining.
	if len(spnp.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for nodepool-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"serviceProviderNodePoolResourceID", spnp.ResourceID.String(), "remainingBundles", len(spnp.Status.MaestroReadonlyBundles))
		return false, nil
	}

	// Check if there are any nodepool-scoped kube-applier *Desire documents remaining.
	nodePoolResourceID := serviceProviderNodePoolResourceID.Parent
	clusterResourceID := nodePoolResourceID.Parent
	spc, err := c.resourcesDBClient.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		logger.Info("no ServiceProviderCluster found associated to the cluster. Continuing with deletion of ServiceProviderNodePool document")
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID != nil {
		kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if kaClient == nil {
			logger.Info("no kube-applier client for management cluster. Continuing with deletion of ServiceProviderNodePool document",
				"serviceProviderNodePoolResourceID", spnp.ResourceID.String(),
				"managementClusterResourceID", mcResourceID.String())
			return true, nil
		}
		desireCRUD, err := kaClient.UntypedCRUD(*nodePoolResourceID)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create kube-applier untyped CRUD: %w", err))
		}

		desireIterator, err := desireCRUD.List(ctx, nil)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to list nodepool-scoped kube-applier resources: %w", err))
		}
		for range desireIterator.Items(ctx) {
			logger.Info("waiting for nodepool-scoped kube-applier content to be deleted before removing ServiceProviderNodePool",
				"serviceProviderNodePoolResourceID", spnp.ResourceID.String(),
				"managementClusterResourceID", mcResourceID.String())
			return false, nil
		}
		if err := desireIterator.GetError(); err != nil {
			return false, utils.TrackError(fmt.Errorf("error iterating nodepool-scoped kube-applier resources: %w", err))
		}

	} else {
		logger.Info("no management cluster resource ID found for ServiceProviderNodePool. Continuing with deletion of ServiceProviderNodePool document")
	}

	return true, nil
}

func (c *nodePoolChildResourcesCleanupController) ensureNodePoolScopedKubeApplierResourcesDeleted(ctx context.Context, nodePoolResourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	clusterResourceID := nodePoolResourceID.Parent
	spc, err := c.resourcesDBClient.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		// If there is no ServiceProviderCluster, we cannot determine the management cluster resource ID, so we skip the deletion of the
		// *Desire documents without erroring.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// If the ServiceProviderCluster has no management cluster resource ID, we cannot determine the management
	// cluster resource ID, so we skip the deletion of the *Desire documents without erroring.
	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		logger.Info("no management cluster resource ID found for ServiceProviderCluster; skipping deletion of nodepool-scoped kube-applier content",
			"serviceProviderClusterResourceID", spc.ResourceID.String())
		return nil
	}

	// Best-effort: if the kube-applier client is unavailable, skip desire deletion here.
	// Remaining *Desires are cleaned up by later deletion stages and the orphan scraper.
	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		logger.Info("no kube-applier client configured for management cluster; skipping nodepool-scoped desire deletion",
			"managementClusterResourceID", mcResourceID.String())
		return nil
	}

	// extraDeleteGates uses lowercased kubeapplier.*DesireResourceTypeName keys. Types not
	// in the map are deleted unconditionally.
	extraDeleteGates := map[string]func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error){
		// strings.ToLower(kubeapplier.ClusterScopedReadDesireResourceType.String()): c.extraDeleteGateShouldDeleteReadDesire,
		// strings.ToLower(kubeapplier.ClusterScopedApplyDesireResourceType.String()): c.extraDeleteGateShouldDeleteApplyDesire,
		// strings.ToLower(kubeapplier.ClusterScopedDeleteDesireResourceType.String()): c.extraDeleteGateShouldDeleteDeleteDesire,
	}

	desireCRUD, err := kaClient.UntypedCRUD(*nodePoolResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create kube-applier untyped CRUD: %w", err))
	}

	desireIterator, err := desireCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list nodepool-scoped kube-applier resources: %w", err))
	}
	for _, resource := range desireIterator.Items(ctx) {
		if resource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("kube-applier document at cosmosID %q has no resourceID; refusing to delete", resource.ID))
		}

		if extraDeleteGate, ok := extraDeleteGates[strings.ToLower(resource.ResourceType)]; ok {
			shouldDelete, err := extraDeleteGate(ctx, resource.ResourceID)
			if err != nil {
				return utils.TrackError(err)
			}
			if !shouldDelete {
				continue
			}
		}

		logger.Info("deleting nodepool-scoped kube-applier resource", "resourceID", resource.ResourceID)
		if err := desireCRUD.DeleteByCosmosID(ctx, resource.PartitionKey, resource.ID); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete nodepool-scoped kube-applier resource %q: %w", resource.CosmosResourceID, err))
		}
	}
	if err := desireIterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating nodepool-scoped kube-applier resources: %w", err))
	}

	logger.Info("all included nodepool-scoped kube-applier child resources deleted")

	return nil
}
