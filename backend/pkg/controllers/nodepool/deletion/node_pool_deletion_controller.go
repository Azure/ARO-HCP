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

package deletion

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDeletionController issues a Cosmos nodepool delete
// for the Node Pools that have their DeletionTimestamp and ClusterServiceDeletionTimestamp set,
// their ClusterServiceID has been cleared, all nodepool-scoped Maestro readonly bundles
// have been deleted from the ServiceProviderNodePool, and all nodepool-scoped kube-applier
// *Desire documents have been deleted.
type nodePoolDeletionController struct {
	nodePoolLister                corelisters.NodePoolLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister
	serviceProviderClusterLister  corelisters.ServiceProviderClusterLister
	resourcesDBClient             corecosmosstorage.ResourcesDBClient
	kubeApplierDBClients          kubeappliercosmosstorage.KubeApplierDBClients
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDeletionController)(nil)

func NewNodePoolDeletionController(
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &nodePoolDeletionController{
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		resourcesDBClient:             resourcesDBClient,
		kubeApplierDBClients:          kubeApplierDBClients,
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDeletionController",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether the deleter has unfinished business for the given
// NodePool. All the following conditions must be met:
// - DeletionTimestamp must be set
// - ClusterServiceDeletionTimestamp must be set
// - ClusterServiceID must be nil
func (c *nodePoolDeletionController) NeedsWork(nodePool *coreapi.HCPOpenShiftClusterNodePool) bool {
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

// SyncOnce calls Cosmos to delete the NodePool when the NeedsWork condition is met and
// all the delete preconditions are met:
//  1. All nodepool-scoped Maestro readonly bundles are cleared.
//  2. All other Cosmos child resources are deleted.
func (c *nodePoolDeletionController) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
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
	// modified one of the NeedsWork conditions.
	nodePoolCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	nodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool: %w", err))
	}
	if !c.NeedsWork(nodePool) {
		return nil
	}

	// Precondition: all ApplyDesires for this NodePool must be gone.
	preconditionMet, err := c.deletePreconditionAllApplyDesiresGone(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check ApplyDesire precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// We do not proceed until we know that all the maestro readonly bundles have been eliminated
	preconditionMet, err = c.deletePreconditionAllMaestroNodePoolScopedReadonlyBundlesCleared(ctx, key)
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
	spnp, spnpErr := spnpCRUD.Get(ctx, coreapi.ServiceProviderNodePoolResourceName)
	if spnpErr != nil && !cosmosstorageutils.IsNotFoundError(spnpErr) {
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
		if strings.EqualFold(childResource.ResourceType, coreapi.NodePoolControllerResourceType.String()) {
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

// deletePreconditionAllApplyDesiresGone checks that no ApplyDesires remain for
// this NodePool. The log message reports remaining counts per controller.
func (c *nodePoolDeletionController) deletePreconditionAllApplyDesiresGone(ctx context.Context, key controllerutils.HCPNodePoolKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	spc, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return true, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	if spc.Status.ManagementClusterResourceID == nil {
		return true, nil
	}

	managementClusterID := spc.Status.ManagementClusterResourceID
	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementClusterID)
	if kubeApplierDBClient == nil {
		logger.Info("waiting for kube-applier DB client to be available for ApplyDesire precondition", "managementCluster", managementClusterID.String())
		return false, nil
	}

	kubeApplierCRUD, err := kubeApplierDBClient.ApplyDesiresForNodePool(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return false, fmt.Errorf("failed to get kube-applier CRUD for ApplyDesire precondition: %w", err)
	}

	applyDesireIterator, err := kubeApplierCRUD.List(ctx, &cosmosstorageutils.DBClientListResourceDocsOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list ApplyDesire documents for precondition check: %w", err)
	}

	controllerCounts := map[string]int{}
	for _, desire := range applyDesireIterator.Items(ctx) {
		controller := "unknown"
		if desire.Tags != nil {
			if name := desire.Tags[kubeapplierapi.TagControllerName]; name != "" {
				controller = name
			}
		}
		controllerCounts[controller]++
	}
	if err := applyDesireIterator.GetError(); err != nil {
		return false, fmt.Errorf("error iterating ApplyDesires for precondition check: %w", err)
	}

	if len(controllerCounts) > 0 {
		var parts []string
		for controller, count := range controllerCounts {
			parts = append(parts, fmt.Sprintf("%d from %s", count, controller))
		}
		slices.Sort(parts)
		logger.Info("waiting for all ApplyDesires to be deleted",
			"remaining", strings.Join(parts, ", "))
		return false, nil
	}
	return true, nil
}
