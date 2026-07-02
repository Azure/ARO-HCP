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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/backup"
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
	cooldownChecker      controllerutil.CooldownChecker
	clusterLister        listers.ClusterLister
	resourcesDBClient    database.ResourcesDBClient
	kubeApplierDBClients database.KubeApplierDBClients
}

var _ controllerutils.ClusterSyncer = (*clusterChildResourcesCleanupController)(nil)

func NewClusterChildResourcesCleanupController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	syncer := &clusterChildResourcesCleanupController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		resourcesDBClient:    resourcesDBClient,
		kubeApplierDBClients: kubeApplierDBClients,
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
	// resources. ServiceProviderCluster also carries ManagementClusterResourceID,
	// which we need to reach the per-MC kube-applier container, so cluster-scoped
	// kube-applier *Desires must be removed before the ServiceProviderCluster
	// document. Nodepool-scoped *Desires are cleaned up by the nodepool deletion
	// pipeline.
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

	if err := c.ensureClusterScopedKubeApplierResourcesDeleted(ctx, clusterResourceID); err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete cluster-scoped kube-applier content: %w", err))
	}

	untypedCRUD, err := c.resourcesDBClient.UntypedCRUD(*clusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create untyped CRUD for cluster children: %w", err))
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

	logger.Info("all included cluster cosmos child resources deleted")

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

// extraDeleteGateShouldDeleteServiceProviderCluster returns false while the
// ServiceProviderCluster still has Maestro readonly bundles or cluster-scoped
// kube-applier *Desire documents.
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

	// Check if there are any Maestro readonly bundles remaining.
	if len(spc.Status.MaestroReadonlyBundles) > 0 {
		logger.Info("waiting for cluster-scoped Maestro readonly bundles to be deleted before removing Cosmos entry",
			"serviceProviderClusterResourceID", spc.ResourceID.String(), "remainingBundles", len(spc.Status.MaestroReadonlyBundles))
		return false, nil
	}

	// Check if there are any cluster-scoped kube-applier *Desire documents remaining.
	if spc.Status.ManagementClusterResourceID != nil {
		kaClient := c.kubeApplierDBClients.For(ctx, spc.Status.ManagementClusterResourceID)
		if kaClient == nil {
			logger.Info("no kube-applier client for management cluster. Continuing with deletion of ServiceProviderCluster document",
				"serviceProviderClusterResourceID", spc.ResourceID.String(),
				"managementClusterResourceID", spc.Status.ManagementClusterResourceID.String())
			return true, nil
		}

		desireCRUD, err := kaClient.UntypedCRUD(*serviceProviderClusterResourceID.Parent)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to create kube-applier untyped CRUD: %w", err))
		}

		desireIterator, err := desireCRUD.List(ctx, nil)
		if err != nil {
			return false, utils.TrackError(fmt.Errorf("failed to list cluster-scoped kube-applier resources: %w", err))
		}
		for range desireIterator.Items(ctx) {
			logger.Info("waiting for cluster-scoped kube-applier content to be deleted before removing ServiceProviderCluster",
				"serviceProviderClusterResourceID", spc.ResourceID.String(),
				"managementClusterResourceID", spc.Status.ManagementClusterResourceID.String())
			return false, nil
		}
		if err := desireIterator.GetError(); err != nil {
			return false, utils.TrackError(fmt.Errorf("error iterating cluster-scoped kube-applier resources: %w", err))
		}
	} else {
		logger.Info("no management cluster resource ID found for ServiceProviderCluster. Continuing with deletion of ServiceProviderCluster document")
		return true, nil
	}

	return true, nil
}

// ensureClusterScopedKubeApplierResourcesDeleted ensures that the cluster-scoped *Desire documents are deleted
// from the database. *Desire documents on non-cluster scoped resources are deleted by their corresponding deletion controllers.
func (c *clusterChildResourcesCleanupController) ensureClusterScopedKubeApplierResourcesDeleted(ctx context.Context, clusterResourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

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
		logger.Info("no management cluster resource ID found for ServiceProviderCluster; skipping deletion of cluster-scoped kube-applier content",
			"serviceProviderClusterResourceID", spc.ResourceID.String())
		return nil
	}

	// Best-effort: if the kube-applier client is unavailable, skip desire deletion here.
	// Remaining *Desires are cleaned up by later deletion stages and the orphan scraper.
	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		logger.Info("no kube-applier client configured for management cluster; skipping cluster-scoped desire deletion",
			"managementClusterResourceID", mcResourceID.String())
		return nil
	}

	// Backup desires are managed by ensureBackupScheduleKubeObjectsDeleted
	// and must not be swept here while that cleanup is in progress.
	skipBackupDesires := func(_ context.Context, resourceID *azcorearm.ResourceID) (bool, error) {
		if strings.HasPrefix(resourceID.Name, backup.BackupDesireNamePrefix) {
			return false, nil
		}
		return true, nil
	}
	// extraDeleteGates uses lowercased kubeapplier.*DesireResourceTypeName keys. Types not
	// in the map are deleted unconditionally.
	extraDeleteGates := map[string]func(ctx context.Context, resourceID *azcorearm.ResourceID) (bool, error){
		strings.ToLower(kubeapplier.ClusterScopedApplyDesireResourceType.String()): skipBackupDesires,
	}

	desireCRUD, err := kaClient.UntypedCRUD(*clusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to create kube-applier untyped CRUD: %w", err))
	}

	// We do non-recursive list so we only deal with cluster-scoped *Desires. Other scoped resources are handled by their corresponding
	// deletion controllers.
	desireIterator, err := desireCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list cluster-scoped kube-applier resources: %w", err))
	}
	for _, resource := range desireIterator.Items(ctx) {
		if resource.ResourceID == nil {
			return utils.TrackError(fmt.Errorf("kube-applier document at cosmosID %q has no resourceID. Refusing to delete", resource.ID))
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

		logger.Info("deleting cluster-scoped kube-applier resource", "resourceID", resource.ResourceID)
		if err := desireCRUD.DeleteByCosmosID(ctx, resource.PartitionKey, resource.ID); err != nil {
			return utils.TrackError(fmt.Errorf("failed to delete cluster-scoped kube-applier resource %q: %w", resource.CosmosResourceID, err))
		}
	}
	if err := desireIterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating cluster-scoped kube-applier resources: %w", err))
	}

	if err := c.ensureBackupScheduleKubeObjectsDeleted(ctx, clusterResourceID); err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure backup schedule kube objects deleted: %w", err))
	}

	logger.Info("all included cluster-scoped kube-applier child resources deleted")

	return nil
}

// ensureBackupScheduleKubeObjectsDeleted converts backup ApplyDesires from
// ServerSideApply to Delete type so the kube-applier removes the OADP Schedule
// kube objects from the management cluster. Once the Delete-type ApplyDesire
// reports Successful, all related desires are cleaned up from Cosmos.
func (c *clusterChildResourcesCleanupController) ensureBackupScheduleKubeObjectsDeleted(ctx context.Context, clusterResourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	spc, err := c.resourcesDBClient.ServiceProviderClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name).Get(ctx, api.ServiceProviderClusterResourceName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	mcResourceID := spc.Status.ManagementClusterResourceID
	if mcResourceID == nil {
		return nil
	}

	kaClient := c.kubeApplierDBClients.For(ctx, mcResourceID)
	if kaClient == nil {
		return nil
	}

	adCrud, err := kaClient.ApplyDesiresForCluster(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ApplyDesire CRUD: %w", err))
	}
	rdCrud, err := kaClient.ReadDesiresForCluster(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ReadDesire CRUD: %w", err))
	}

	adIterator, err := adCrud.List(ctx, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ApplyDesires: %w", err))
	}
	for _, ad := range adIterator.Items(ctx) {
		if !strings.HasPrefix(ad.ResourceID.Name, backup.BackupDesireNamePrefix) {
			continue
		}
		desireName := ad.ResourceID.Name

		if ad.Spec.Type == kubeapplier.ApplyDesireTypeServerSideApply {
			logger.Info("converting backup ApplyDesire to Delete type", "desireName", desireName)
			deleteAD := &kubeapplier.ApplyDesire{
				CosmosMetadata: *ad.CosmosMetadata.DeepCopy(),
				Spec: kubeapplier.ApplyDesireSpec{
					ManagementCluster: mcResourceID,
					Type:              kubeapplier.ApplyDesireTypeDelete,
					TargetItem:        ad.Spec.TargetItem,
				},
				Status: *ad.Status.DeepCopy(),
			}
			if _, err := adCrud.Replace(ctx, deleteAD, nil); err != nil {
				return utils.TrackError(fmt.Errorf("failed to replace ApplyDesire %s with Delete type: %w", desireName, err))
			}
			continue
		}

		if !isApplyDesireSuccessful(ad.Status.Conditions) {
			logger.Info("waiting for backup Delete ApplyDesire to complete", "desireName", desireName)
			continue
		}

		logger.Info("backup Delete ApplyDesire successful, cleaning up desires", "desireName", desireName)
		if err := adCrud.Delete(ctx, desireName); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete ApplyDesire %s: %w", desireName, err))
		}
		if err := rdCrud.Delete(ctx, desireName); err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to delete ReadDesire %s: %w", desireName, err))
		}
	}
	if err := adIterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("error iterating ApplyDesires: %w", err))
	}

	return nil
}

func isApplyDesireSuccessful(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == kubeapplier.ConditionTypeSuccessful && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
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
