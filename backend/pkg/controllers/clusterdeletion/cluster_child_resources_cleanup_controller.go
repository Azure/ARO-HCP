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

package clusterdeletion

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

// clusterChildResourcesCleanupController deletes child resources scoped
// under a Cluster recursively once the Cluster is marked for deletion and
// Cluster Service has confirmed the delete on its side. Controller status
// documents (ClusterControllerResourceType) are left alone. Resources scoped
// under NodePools and ExternalAuths are skipped because they have their own
// deletion pipelines. The orphan scraper handles controller status after the
// Cluster document itself is removed.
type clusterChildResourcesCleanupController struct {
	cooldownChecker   controllerutil.CooldownChecker
	clusterLister     listers.ClusterLister
	resourcesDBClient database.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*clusterChildResourcesCleanupController)(nil)

func NewClusterChildResourcesCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterChildResourcesCleanupController{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:     clusterLister,
		resourcesDBClient: resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterChildResourcesCleanupController",
		resourcesDBClient,
		informers,
		nil,
		time.Minute,
		syncer,
	)
}

func (c *clusterChildResourcesCleanupController) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterChildResourcesCleanupController) NeedsWork(cluster *api.HCPOpenShiftCluster) bool {
	// TODO temporary check to skip the new deletion approach for Clusters that were created before the new approach was implemented.
	// This will be removed once all clusters whose deletion was triggered before the new approach is fully rolled out have been
	// fully deleted in all ARO-HCP permanent environments, for all regions.
	if !cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach {
		return false
	}

	return cluster.ServiceProviderProperties.DeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil &&
		cluster.ServiceProviderProperties.ClusterServiceID == nil
}

func (c *clusterChildResourcesCleanupController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(cachedCluster) {
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	cluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	if !c.NeedsWork(cluster) {
		return nil
	}

	// We must not delete cluster-scoped resources (like ServiceProviderCluster,
	// ManagementClusterContent) until all nodepools and externalauths are fully
	// deleted, because their deletion controllers may depend on cluster-scoped
	// resources.
	allNodePoolsGone, err := deletePreconditionAllNodePoolsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check nodepool precondition: %w", err))
	}
	if !allNodePoolsGone {
		logger.Info("waiting for all nodepools to be deleted before cleaning up cluster child resources")
		return nil
	}

	allExternalAuthsGone, err := deletePreconditionAllExternalAuthsDeleted(ctx, c.resourcesDBClient, key)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to check externalauth precondition: %w", err))
	}
	if !allExternalAuthsGone {
		logger.Info("waiting for all external auths to be deleted before cleaning up cluster child resources")
		return nil
	}

	clusterResourceID := cluster.ID
	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*clusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create untyped CRUD for cluster children: %w", err))
	}

	// skipSubtreeTypes lists resource types whose entire subtrees are skipped.
	// A child resource is left alone if its type path starts with any of these
	// types, because those subtrees have their own deletion pipelines.
	skipSubtreeTypes := []azcorearm.ResourceType{
		api.NodePoolResourceType,
		api.ExternalAuthResourceType,
	}

	// extraDeleteGates contains per-resource-type conditional logic for
	// resources that are not part of a skipped subtree. If the resource type
	// is not in this map, the resource is deleted unconditionally.
	extraDeleteGates := map[string]func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error){
		strings.ToLower(api.ServiceProviderClusterResourceType.String()): c.extraDeleteGateShouldDeleteServiceProviderCluster,
		// We never delete cluster controllers here, as there might be controllers still running
		// for the Cluster until the very end of the deletion process
		strings.ToLower(api.ClusterControllerResourceType.String()): func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error) { return false, nil },
	}

	childIterator, err := untypedCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list cluster child resources: %w", err))
	}
	for _, childResource := range childIterator.Items(ctx) {
		if childResource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("child resource at cosmosID %q has no resourceID; refusing to delete", childResource.ID))
		}

		if hasSkippedResourceTypePrefix(childResource.ResourceID, skipSubtreeTypes) {
			continue
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

// hasSkippedResourceTypePrefix returns true if the resource's type path starts with
// any of the skip entries. Each entry is a lowercased ResourceType.Type such
// as "hcpopenshiftclusters/nodepools". This catches both the resource itself
// and all its descendants.
func hasSkippedResourceTypePrefix(resourceID *azcorearm.ResourceID, skipSubtreeTypes []azcorearm.ResourceType) bool {
	resourceTypeLower := strings.ToLower(resourceID.ResourceType.Type)
	for _, skip := range skipSubtreeTypes {
		skipLower := strings.ToLower(skip.Type)
		if resourceTypeLower == skipLower || strings.HasPrefix(resourceTypeLower, skipLower+"/") {
			return true
		}
	}
	return false
}

// extraDeleteGateShouldDeleteServiceProviderCluster checks if the
// ServiceProviderCluster has any Maestro readonly bundles. If there are
// bundles it returns false, otherwise it returns true.
func (c *clusterChildResourcesCleanupController) extraDeleteGateShouldDeleteServiceProviderCluster(ctx context.Context, serviceProviderClusterResourceID *azcorearm.ResourceID) (bool, error) {
	logger := utils.LoggerFromContext(ctx)

	if serviceProviderClusterResourceID.Parent == nil {
		return false, utils.TrackError(fmt.Errorf(
			"service provider cluster resource ID missing cluster parent: %s",
			serviceProviderClusterResourceID.String()))
	}

	clusterName := serviceProviderClusterResourceID.Parent.Name

	spc, err := c.resourcesDBClient.ServiceProviderClusters(
		serviceProviderClusterResourceID.SubscriptionID,
		serviceProviderClusterResourceID.ResourceGroupName,
		clusterName,
	).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	if len(spc.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for cluster-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"serviceProviderClusterResourceID", spc.ResourceID.String(), "remainingBundles", len(spc.Status.MaestroReadonlyBundles))
		return false, nil
	}
	return true, nil
}

func deletePreconditionAllNodePoolsDeleted(ctx context.Context, dbClient database.ResourcesDBClient, key controllerutils.HCPClusterKey) (bool, error) {
	nodePoolIterator, err := dbClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list node pools: %w", err))
	}
	for range nodePoolIterator.Items(ctx) {
		return false, nil
	}
	if err := nodePoolIterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("error iterating node pools: %w", err))
	}
	return true, nil
}

func deletePreconditionAllExternalAuthsDeleted(ctx context.Context, dbClient database.ResourcesDBClient, key controllerutils.HCPClusterKey) (bool, error) {
	externalAuthIterator, err := dbClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).ExternalAuth(key.HCPClusterName).List(ctx, nil)
	if err != nil {
		return false, utils.TrackError(fmt.Errorf("failed to list external auths: %w", err))
	}
	for range externalAuthIterator.Items(ctx) {
		return false, nil
	}
	if err := externalAuthIterator.GetError(); err != nil {
		return false, utils.TrackError(fmt.Errorf("error iterating external auths: %w", err))
	}
	return true, nil
}
