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
	"errors"
	"fmt"
	"strings"
	"time"

	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterresources"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterDeletionController issues a Cosmos cluster delete
// for the Clusters that have their DeletionTimestamp and ClusterServiceDeletionTimestamp set,
// their ClusterServiceID has been cleared, all cluster-scoped Maestro readonly bundles
// have been deleted from the ServiceProviderCluster, and all child NodePool and
// ExternalAuth Cosmos documents have been deleted.
type clusterDeletionController struct {
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	billingDBClient              billingcosmosstorage.BillingDBClient
	kubeApplierDBClients         kubeappliercosmosstorage.KubeApplierDBClients
	passiveClock                 utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*clusterDeletionController)(nil)

func NewClusterDeletionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	billingDBClient billingcosmosstorage.BillingDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierDBClients kubeappliercosmosstorage.KubeApplierDBClients,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &clusterDeletionController{
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
		billingDBClient:              billingDBClient,
		kubeApplierDBClients:         kubeApplierDBClients,
		passiveClock:                 clock,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterDeletionController",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

// NeedsWork reports whether the deleter has unfinished business for the given
// Cluster. All the following conditions must be met:
// - DeletionTimestamp must be set
// - ClusterServiceDeletionTimestamp must be set
// - ClusterServiceID must be nil
func (c *clusterDeletionController) NeedsWork(cluster *coreapi.HCPOpenShiftCluster) bool {
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil
}

// SyncOnce deletes a Cluster document from Cosmos after verifying that all
// deletion preconditions are satisfied:
//  1. All cluster-scoped Maestro readonly bundles are cleared.
//  2. All node pool documents are deleted.
//  3. All external auth documents are deleted.
//  4. All other Cosmos child resources are deleted.
//
// Before removing the cluster document, the corresponding billing document is
// marked as deleted (idempotent). Returns nil without acting when NeedsWork is
// false or any precondition is not yet met.
func (c *clusterDeletionController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedCluster) {
		return nil
	}

	// Quick cache check for Maestro readonly bundles
	cachedSPC, spcCacheErr := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if spcCacheErr == nil && len(cachedSPC.Status.MaestroReadonlyBundles) > 0 {
		return nil
	}

	// Confirm against the live document.
	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	if !c.NeedsWork(cluster) {
		return nil
	}

	// Precondition: all cluster-scoped ApplyDesires tagged by ClusterResourcesController must be gone.
	// The ClusterResourcesController is responsible for deleting its own desires during cluster deletion.
	preconditionMet, err := c.deletePreconditionClusterResourceApplyDesiresGone(ctx, key, cachedSPC)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check ApplyDesire precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all cluster-scoped Maestro readonly bundles must be cleared
	preconditionMet, err = c.deletePreconditionAllMaestroClusterScopedReadonlyBundlesCleared(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all node pool Cosmos docs must be deleted
	preconditionMet, err = deletePreconditionAllNodePoolsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all external auth Cosmos docs must be deleted
	preconditionMet, err = deletePreconditionAllExternalAuthsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all credential request Cosmos docs must be deleted
	preconditionMet, err = deletePreconditionAllCredentialRequestsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all credential revocation Cosmos docs must be deleted
	preconditionMet, err = deletePreconditionAllCredentialRevocationsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Precondition: all Cosmos child resources must be deleted (except controllers)
	preconditionMet, err = c.deletePreconditionCosmosChildResourcesDeleted(ctx, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check precondition: %w", err))
	}
	if !preconditionMet {
		return nil
	}

	// Mark billing document as deleted before deleting the cluster document.
	// This is idempotent: if the billing document was already marked or never
	// existed, MarkBillingDocumentDeleted returns nil.
	// ErrAmbiguousResult (multiple billing docs for one cluster) is a data
	// integrity issue that retrying won't fix, so we log and continue.
	err = controllerutils.MarkBillingDocumentDeleted(ctx, c.billingDBClient, cluster.ID, c.passiveClock.Now())
	if errors.Is(err, cosmosstorageutils.ErrAmbiguousResult) {
		logger.Error(err, "Failed to mark CosmosDB billing record for deletion")
	} else if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("deleting cluster from Cosmos")
	err = clusterCRUD.Delete(ctx, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete cluster from Cosmos: %w", err))
	}
	logger.Info("cluster deleted from Cosmos")

	return nil
}

// deletePreconditionAllMaestroClusterScopedReadonlyBundlesCleared checks if the
// ServiceProviderCluster has any Maestro readonly bundles.
func (c *clusterDeletionController) deletePreconditionAllMaestroClusterScopedReadonlyBundlesCleared(ctx context.Context, key controllerutils.HCPClusterKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	spcCRUD := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	spc, spcErr := spcCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if spcErr != nil && !cosmosstorageutils.IsNotFoundError(spcErr) {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", spcErr))
	}
	if spc != nil && len(spc.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for cluster-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"remainingBundles", len(spc.Status.MaestroReadonlyBundles))
		return false, nil
	}
	return true, nil
}

// deletePreconditionCosmosChildResourcesDeleted checks if the cosmos child resources
// have been deleted, ignoring cluster controllers and orphaned nodepool/externalauth
// controller status documents (which are left behind by the nodepool/externalauth
// deletion pipelines and cleaned up by the orphan scraper).
func (c *clusterDeletionController) deletePreconditionCosmosChildResourcesDeleted(ctx context.Context, key controllerutils.HCPClusterKey) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}

	// Orphaned controller status documents may linger under nodepools and
	// externalauths after their parent documents have been deleted. We skip
	// those here because the orphan scraper handles them.
	skipSubtreeTypes := []azcorearm.ResourceType{
		coreapi.NodePoolResourceType,
		coreapi.ExternalAuthResourceType,
		coreapi.SystemAdminCredentialRequestResourceType,
		coreapi.SystemAdminCredentialRevocationResourceType,
	}

	clusterResourceID := cluster.ID
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*clusterResourceID)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to create untyped CRUD for child check: %w", err))
	}
	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		if strings.EqualFold(childResource.ResourceType, coreapi.ClusterControllerResourceType.String()) {
			continue
		}
		if hasSkippedResourceTypePrefix(childResource.ResourceID, skipSubtreeTypes) {
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

// deletePreconditionClusterResourceApplyDesiresGone checks that no ApplyDesires
// tagged by the ClusterResourcesController remain for this cluster.
// The ClusterResourcesController is responsible for deleting its own desires;
// this controller only verifies they are gone before proceeding.
func (c *clusterDeletionController) deletePreconditionClusterResourceApplyDesiresGone(ctx context.Context, key controllerutils.HCPClusterKey, spc *coreapi.ServiceProviderCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if spc == nil || spc.Status.ManagementClusterResourceID == nil {
		return true, nil
	}

	managementClusterID := spc.Status.ManagementClusterResourceID
	kubeApplierDBClient := c.kubeApplierDBClients.For(ctx, managementClusterID)
	if kubeApplierDBClient == nil {
		logger.Info("waiting for kube-applier DB client to be available for ApplyDesire precondition", "managementCluster", managementClusterID.String())
		return false, nil
	}

	kubeApplierCRUD, err := kubeApplierDBClient.ApplyDesiresForCluster(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return false, fmt.Errorf("failed to get kube-applier CRUD for ApplyDesire precondition: %w", err)
	}

	applyDesireIterator, err := kubeApplierCRUD.List(ctx, &cosmosstorageutils.DBClientListResourceDocsOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to list ApplyDesire documents for precondition check: %w", err)
	}

	remaining := 0
	for _, desire := range applyDesireIterator.Items(ctx) {
		if desire.Tags == nil {
			continue
		}
		if desire.Tags[kubeapplierapi.TagKeyControllerName] == clusterresources.ClusterResourcesControllerName {
			remaining++
		}
	}
	if err := applyDesireIterator.GetError(); err != nil {
		return false, fmt.Errorf("error iterating ApplyDesires for precondition check: %w", err)
	}

	if remaining > 0 {
		logger.Info("waiting for ClusterResourcesController to delete its ApplyDesires",
			"remaining", remaining)
		return false, nil
	}
	return true, nil
}
