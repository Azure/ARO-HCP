package controllers

// Copyright 2025 Microsoft Corporation
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

import (
	"context"
	"errors"
	"fmt"
	"time"

	workv1 "open-cluster-management.io/api/work/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedMaestroReadonlyBundles struct {
	name string

	resourcesDBClient database.ResourcesDBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string
}

// NewDeleteOrphanedMaestroReadonlyBundlesController periodically looks for Maestro readonly bundles in the Maestro API that are not referenced
// by any of the supported cosmos resources by this controller and deletes them.
func NewDeleteOrphanedMaestroReadonlyBundlesController(resourcesDBClient database.ResourcesDBClient, csClient ocm.ClusterServiceClientSpec, maestroClientBuilder maestro.MaestroClientBuilder, maestroSourceEnvironmentIdentifier string) controllerutils.Controller {
	c := &deleteOrphanedMaestroReadonlyBundles{
		name:                               "DeleteOrphanedMaestroReadonlyBundles",
		resourcesDBClient:                  resourcesDBClient,
		clusterServiceClient:               csClient,
		maestroClientBuilder:               maestroClientBuilder,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedMaestroReadonlyBundles",
			},
		),
	}

	return c
}

// SyncOnce algorithm:
//  1. Build a map from Cluster Service provision shard ID to Maestro client (one client per registered provision shard).
//  2. For each resource type that contains Maestro readonly bundles that we decide to include here:
//    2.1 List all documents (initial snapshot).
//    2.2 Build a map from Cluster Service provision shard ID to the documents on that shard (from the initial list).
//    2.3 For each shard, list Maestro bundles (paginated, and with a label selector that filters for the readonly managed-by label associated to
//        the specifc resource type we are processing). A bundle is a delete candidate if it passes the resource-scoped readonly managed-by label
//        filter and its name is not referenced by any document on that shard in the initial map. Each candidate records the provision shard
//        id and a pointer to the listed ManifestWork.
//    2.4 List all documents again (fresh snapshot), rebuild the map from it in the same way as 2.2.
//    2.5 For each candidate, if the bundle name is still not referenced on that shard in the fresh snapshot, delete it via Maestro
//        bundle deletion API.

// Cross-store: The fresh SPC list and per-shard reference set (steps 5-6) prevent deleting a bundle that is already referenced
// in committed Cosmos documents by the time that snapshot is built, so a stale initial list alone does not cause accidental
// delete.
//
// IMPORTANT NOTE: This assumes that the maestro server associated to the provision shard
// has resources with always the same source ID. If it turns out we cannot have this assumption this logic would not
// be good enough. In that case it might be necessary to store to what source ID a Maestro Bundle/set of Maestro Bundles
// belongs to but then the instantiation of the Maestro client needs to be done differently as its scoped to
// Maestro Consumer Name + Maestro Source ID. We know for example that in the CSPR environment different CS instances
// have different Maestro source IDs using the same Maestro Server.
//
// Note: We considered using the Maestro API Maestro UID which is globally unique but it's possible that there's a scenario
// where a maestro create readonly bundles controller creates a bundle, then creates the Maestro bundle using the Maestro API
// but then for some reason fails to persist it in the database, which in that case the cluster ended up being deleted by the
// orphan controller accidentally. In that scenario we would not have the Maestro UID to identify the Maestro Bundle and
// we would not be able to delete it. Furthermore we should not use the fact of the UID being empty as the trigger
// to delete because it could be that it's being created and not yet persisted in Cosmos.
func (c *deleteOrphanedMaestroReadonlyBundles) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned Maestro Readonly Bundles")

	logger.Info("Building Maestro clients per Cluster Service provision shard")
	maestroClientsByShard, err := c.buildMaestroClientsByProvisionShard(ctx)
	// Cancel Maestro clients when the sync is done to avoid leaking resources (map may be partial on error).
	defer cancelMaestroClientsByProvisionShard(maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Maestro clients by provision shard: %w", err))
	}
	logger.Info(fmt.Sprintf("Built Maestro clients for %d provision shards", len(maestroClientsByShard)))

	var syncErrors []error

	logger.Info("Ensuring orphaned cluster scoped Maestro Readonly Bundles are deleted")
	if err := c.ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx, maestroClientsByShard); err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to ensure orphaned cluster-scoped Maestro Bundles are deleted: %w", err)))
	}

	logger.Info("Ensuring orphaned nodepool scoped Maestro Readonly Bundles are deleted")
	if err := c.ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(ctx, maestroClientsByShard); err != nil {
		syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to ensure orphaned nodepool-scoped Maestro Bundles are deleted: %w", err)))
	}

	logger.Info("End of orphaned Maestro Readonly Bundles sync")
	return errors.Join(syncErrors...)
}

// ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted ensures that Maestro readonly bundles managed by the cluster-scoped
// controller are deleted when no ServiceProviderCluster on that provision shard references them.
func (c *deleteOrphanedMaestroReadonlyBundles) ensureClusterScopedOrphanedMaestroReadonlyBundlesAreDeleted(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient) error {
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("maestroReadonlyBundleReferencesResourceType", api.ServiceProviderClusterResourceType)
	ctx = utils.ContextWithLogger(ctx, logger)

	return c.ensureOrphanedReadonlyBundlesDeleted(ctx, maestroClientsByShard, readonlyBundleManagedByK8sLabelValueClusterScoped, c.clusterScopedPersistedMaestroBundleRefsByShardFromCosmos)
}

// ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted ensures that Maestro readonly bundles managed by the
// nodepool-scoped controller are deleted when no ServiceProviderNodePool on that provision shard references them.
func (c *deleteOrphanedMaestroReadonlyBundles) ensureOrphanedNodePoolScopedMaestroReadonlyBundlesAreDeleted(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient) error {
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues("maestroReadonlyBundleReferencesResourceType", api.ServiceProviderNodePoolResourceType)
	ctx = utils.ContextWithLogger(ctx, logger)

	return c.ensureOrphanedReadonlyBundlesDeleted(ctx, maestroClientsByShard, readonlyBundleManagedByK8sLabelValueNodePoolScoped, c.nodePoolScopedPersistedMaestroBundleRefsByShardFromCosmos)
}

// getAllServiceProviderClusters returns all ServiceProviderClusters via database.ListAll.
func (c *deleteOrphanedMaestroReadonlyBundles) getAllServiceProviderClusters(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	// We list all ServiceProviderClusters in chunks of 500 at most to avoid putting
	// too much pressure on the Cosmos DB.
	// Any failure to iterate over the ServiceProviderclusters ends the sync process because otherwise
	// we would not have the complete information to evaluate the deletion and we could
	// accidentally delete Maestro Bundles that are still in use.
	return database.ListAll(ctx, 500, c.resourcesDBClient.ResourcesGlobalListers().ServiceProviderClusters().List)
}

// getAllServiceProviderNodePools returns all ServiceProviderNodePools via database.ListAll.
func (c *deleteOrphanedMaestroReadonlyBundles) getAllServiceProviderNodePools(ctx context.Context) ([]*api.ServiceProviderNodePool, error) {
	// We list all ServiceProviderNodePools in chunks of 500 at most to avoid putting
	// too much pressure on the Cosmos DB.
	// Any failure to iterate over the ServiceProviderNodePools ends the sync process because otherwise
	// we would not have the complete information to evaluate the deletion and we could
	// accidentally delete Maestro Bundles that are still in use.
	return database.ListAll(ctx, 500, c.resourcesDBClient.ResourcesGlobalListers().ServiceProviderNodePools().List)
}

// shardMaestroClient holds a Maestro API client for one Cluster Service provision shard and its teardown cancel func.
type shardMaestroClient struct {
	maestroClient           maestro.Client
	maestroClientCancelFunc context.CancelFunc
}

// cancelMaestroClientsByProvisionShard runs the cancel function for each Maestro client entry in maestroClientsByProvisionShard.
func cancelMaestroClientsByProvisionShard(maestroClientsByProvisionShard map[string]*shardMaestroClient) {
	for _, entry := range maestroClientsByProvisionShard {
		entry.maestroClientCancelFunc()
	}
}

// buildMaestroClientsByProvisionShard lists registered provision shards from Cluster Service and builds a map of
// provision shard ID to Maestro client. The key of the map is the CS provision shard ID.
// On error the returned map may be partial (clients created before the error). The caller must defer cancelMaestroClientsByProvisionShard unconditionally.
func (c *deleteOrphanedMaestroReadonlyBundles) buildMaestroClientsByProvisionShard(ctx context.Context) (map[string]*shardMaestroClient, error) {
	maestroClientsByProvisionShard := map[string]*shardMaestroClient{}

	// TODO we list the provision shards from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account the provision shard status on what to consider (active, maintenance, offline, ...)?
	// for now we consider all provision shards independently of their status.
	provisionShardIter := c.clusterServiceClient.ListProvisionShards()
	for provisionShard := range provisionShardIter.Items(ctx) {
		// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
		// This is important to avoid leaking resources when the sync is done.
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := createMaestroClientFromCSProvisionShard(maestroClientCtx, c.maestroSourceEnvironmentIdentifier, c.maestroClientBuilder, provisionShard)
		if err != nil {
			cancel() // on error creating the Maestro client we ensure we cancel the context that we just created too
			return maestroClientsByProvisionShard, utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
		}
		maestroClientsByProvisionShard[provisionShard.ID()] = &shardMaestroClient{
			maestroClient:           maestroClient,
			maestroClientCancelFunc: cancel,
		}
	}
	err := provisionShardIter.GetError()
	if err != nil {
		return maestroClientsByProvisionShard, utils.TrackError(fmt.Errorf("failed to list Cluster Service provision shards: %w", err))
	}

	return maestroClientsByProvisionShard, nil
}

// mapServiceProviderClustersByProvisionShard groups ServiceProviderClusters by Cluster Service provision shard ID.
// Every resolved shard must exist in maestroClientsByShard (registered provision shards).
// ServiceProviderClusters whose parent cluster has no ClusterServiceID yet (pre–Cluster Service registration) are skipped
// so the syncer doesn't fail if there are some resources that still don't have it set
func (c *deleteOrphanedMaestroReadonlyBundles) mapServiceProviderClustersByProvisionShard(ctx context.Context, spcs []*api.ServiceProviderCluster, maestroClientsByShard map[string]*shardMaestroClient) (map[string][]*api.ServiceProviderCluster, error) {
	res := make(map[string][]*api.ServiceProviderCluster)
	for _, spc := range spcs {
		shardID, skip, err := c.clusterProvisionShardIDForServiceProviderCluster(ctx, spc)
		if err != nil {
			return nil, err
		}
		if skip {
			// It should be safe to skip those because if a maestro bundle in the maestro API exists it means that there should be a corresponding Cosmos
			// resource with a maestro bundle reference. If for some reason during the orphan calculation inbetween the first read of cosmos
			// resources and the read in the Maestro api there's a new bundle in maestro, the second read of cosmos resources will catch that
			// and prevent accidental deletion.
			// We should also be able to skip the ServiceProviderCluster if the cluster associated with it does not exist anymore
			// between the time the sync iteration started and this point. In that case it's correct that we eliminate the maestro readonly bundle
			// in the API because the cluster is no longer there.
			continue
		}
		if _, ok := maestroClientsByShard[shardID]; !ok {
			return nil, utils.TrackError(fmt.Errorf("provision shard %s for ServiceProviderCluster %s is not present in provision shards map", shardID, spc.ResourceID.String()))
		}
		res[shardID] = append(res[shardID], spc)
	}
	return res, nil
}

// mapServiceProviderNodePoolsByProvisionShard groups ServiceProviderNodePools by Cluster Service provision shard ID
// of their owning cluster. Every resolved shard must exist in maestroClientsByShard (registered provision shards).
// ServiceProviderNodePools whose parent cluster has no ClusterServiceID yet (pre–Cluster Service registration) are skipped
// so the syncer doesn't fail if there are some resources that still don't have it set.
func (c *deleteOrphanedMaestroReadonlyBundles) mapServiceProviderNodePoolsByProvisionShard(ctx context.Context, spnps []*api.ServiceProviderNodePool, maestroClientsByShard map[string]*shardMaestroClient) (map[string][]*api.ServiceProviderNodePool, error) {
	res := make(map[string][]*api.ServiceProviderNodePool)
	for _, spnp := range spnps {
		shardID, skip, err := c.clusterProvisionShardIDForServiceProviderNodePool(ctx, spnp)
		if err != nil {
			return nil, err
		}
		if skip {
			// It should be safe to skip those because if a maestro bundle in the maestro API exists it means that there should be a corresponding Cosmos
			// resource with a maestro bundle reference. If for some reason during the orphan calculation inbetween the first read of cosmos
			// resources and the read in the Maestro api there's a new bundle in maestro, the second read of cosmos resources will catch that
			// and prevent accidental deletion.
			// We should also be able to skip the ServiceProviderNodePool if the cluster associated with it does not exist anymore
			// between the time the sync iteration started and this point. In that case it's correct that we eliminate the maestro readonly bundle
			// in the API because the cluster is no longer there.
			continue
		}
		if _, ok := maestroClientsByShard[shardID]; !ok {
			return nil, utils.TrackError(fmt.Errorf("provision shard %s for ServiceProviderNodePool %s is not present in provision shards map", shardID, spnp.ResourceID.String()))
		}
		res[shardID] = append(res[shardID], spnp)
	}
	return res, nil
}

// maestroBundleNamesByShard maps Cluster Service provision shard IDs to a set of Maestro API Maestro bundle names
// The outer map key is the Cluster Service provision shard ID. The inner map key is the Maestro API Maestro bundle name.
// The inner map value is a struct{} to indicate the presence of the bundle name.
type maestroBundleNamesByShard map[string]map[string]struct{}

// maestroBundleNamesByShardRetrieverFunc retrieves a map of Cluster Service provision shard IDs to a set of Maestro API Maestro bundle names.
type maestroBundleNamesByShardRetrieverFunc func(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient) (bundleNamesByShard maestroBundleNamesByShard, err error)

// orphanReadonlyBundleDeleteCandidate is a Maestro bundle listed on a provision shard that was not referenced by the
// initial snapshot for that shard; delete still requires a fresh snapshot check.
type orphanReadonlyBundleDeleteCandidate struct {
	csShardID string
	bundle    *workv1.ManifestWork
}

// listOrphanReadonlyBundleCandidates lists Maestro bundles for each shard using the Maestro API
// and it returns a list of candidate orphan maestro readonly bundles for deletion. The criteria to consider a
// maestro readonly bundle as a candidate for deletion is that it matches the managedByLabelValue and is not
// referenced in bundleNamesByShard for that shard.
func (c *deleteOrphanedMaestroReadonlyBundles) listOrphanReadonlyBundleCandidates(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient,
	bundleNamesByShard maestroBundleNamesByShard, managedByLabelValue string,
) ([]orphanReadonlyBundleDeleteCandidate, error) {
	logger := utils.LoggerFromContext(ctx)
	var deleteCandidates []orphanReadonlyBundleDeleteCandidate

	for csShardID, shardEntry := range maestroClientsByShard {
		shardLogger := logger.WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		maestroClient := shardEntry.maestroClient
		// We list all the Maestro Bundles in chunks of 400 to avoid putting too much pressure on the Maestro API.
		// We filter by the K8s label that identifies which controller manages the bundle.
		listOpts := metav1.ListOptions{
			Limit:         400,
			LabelSelector: fmt.Sprintf("%s=%s", readonlyBundleManagedByK8sLabelKey, managedByLabelValue),
		}
		err := maestro.ForEachMaestroBundle(ctxShard, maestroClient, listOpts, func(maestroBundle *workv1.ManifestWork) error {
			// Even though Maestro should filter by the K8s label we specified we double check it here to be sure.
			if maestroBundle.Labels[readonlyBundleManagedByK8sLabelKey] != managedByLabelValue {
				return nil
			}
			// Check if the bundle is referenced by any resource allocated to this shard.
			if shardRefSet := bundleNamesByShard[csShardID]; shardRefSet != nil {
				if _, referenced := shardRefSet[maestroBundle.Name]; referenced {
					// The Maestro API Maestro Bundle Name should be unique within a given Maestro Consumer Name and Maestro Source ID.
					// If we find a match, it means the Maestro Bundle is referenced and we should not delete it.
					return nil
				}
			}
			deleteCandidates = append(deleteCandidates, orphanReadonlyBundleDeleteCandidate{
				csShardID: csShardID,
				bundle:    maestroBundle,
			})
			return nil
		})
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list Maestro Bundles for shard %s: %w", csShardID, err))
		}
	}

	return deleteCandidates, nil
}

// clusterScopedPersistedMaestroBundleRefsByShardFromCosmos lists ServiceProviderClusters from Cosmos, maps them by
// provision shard, and returns referenced Maestro API bundle names per shard.
func (c *deleteOrphanedMaestroReadonlyBundles) clusterScopedPersistedMaestroBundleRefsByShardFromCosmos(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient) (maestroBundleNamesByShard, error) {
	clusters, err := c.getAllServiceProviderClusters(ctx)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get all ServiceProviderClusters: %w", err))
	}
	shardDocs, err := c.mapServiceProviderClustersByProvisionShard(ctx, clusters, maestroClientsByShard)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to map ServiceProviderClusters to provision shards: %w", err))
	}
	refs, err := c.buildClusterScopedMaestroAPIMaestroBundleNamesByShard(shardDocs)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("error building cluster scoped Maestro API Maestro bundle names by shard: %w", err))
	}
	return refs, nil
}

// nodePoolScopedPersistedMaestroBundleRefsByShardFromCosmos lists ServiceProviderNodePools from Cosmos, maps them by
// provision shard, and returns referenced Maestro API bundle names per shard.
func (c *deleteOrphanedMaestroReadonlyBundles) nodePoolScopedPersistedMaestroBundleRefsByShardFromCosmos(ctx context.Context, maestroClientsByShard map[string]*shardMaestroClient) (maestroBundleNamesByShard, error) {
	pools, err := c.getAllServiceProviderNodePools(ctx)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get all ServiceProviderNodePools: %w", err))
	}
	shardDocs, err := c.mapServiceProviderNodePoolsByProvisionShard(ctx, pools, maestroClientsByShard)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to map ServiceProviderNodePools to provision shards: %w", err))
	}
	refs, err := c.buildNodePoolScopedMaestroAPIMaestroBundleNamesByShard(shardDocs)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("error building nodepool scoped Maestro API Maestro bundle names by shard: %w", err))
	}
	return refs, nil
}

// ensureOrphanedReadonlyBundlesDeleted runs the two-phase list/compare/delete flow shared by every Cosmos-backed resource type
// that can reference Maestro readonly bundles:
//  1. Initial Cosmos snapshot of all the instances of that resource type that can reference Maestro readonly bundles
//  2. For each shard, use its Maestro client and list the maestro bundles including them as deletion candidates
//     when the bundle is not referenced on that shard and the readonlyBundleManagedByK8sLabelKey label matches the managedByLabelValue
//  3. Retrieve a fresh snapshot of all the instances of that resource type that can reference Maestro readonly bundles
//  4. Delete each candidate that is still unreferenced on that shard in the fresh snapshot
func (c *deleteOrphanedMaestroReadonlyBundles) ensureOrphanedReadonlyBundlesDeleted(
	ctx context.Context,
	maestroClientsByShard map[string]*shardMaestroClient,
	managedByLabelValue string,
	persistedMaestroBundleRefsByShardRetriever maestroBundleNamesByShardRetrieverFunc,
) error {
	initialPersistedMaestroBundlesByShard, err := persistedMaestroBundleRefsByShardRetriever(ctx, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to retrieve initial persisted Maestro bundle references by shard: %w", err))
	}

	deleteCandidates, err := c.listOrphanReadonlyBundleCandidates(ctx, maestroClientsByShard, initialPersistedMaestroBundlesByShard, managedByLabelValue)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list orphaned Maestro readonly bundle candidates: %w", err))
	}

	freshPersistedMaestroBundlesByShard, err := persistedMaestroBundleRefsByShardRetriever(ctx, maestroClientsByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to retrieve fresh persisted Maestro bundle references by shard: %w", err))
	}

	err = c.conditionallyDeleteOrphanReadonlyBundleCandidates(ctx, maestroClientsByShard, deleteCandidates, freshPersistedMaestroBundlesByShard)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to delete orphaned Maestro readonly bundle candidates: %w", err))
	}

	return nil
}

// conditionallyDeleteOrphanReadonlyBundleCandidates processes a list of Maestro readonly bundle delete candidates. An
// orphan Maestro readonly bundle delete candidate is deleted only if it is not referenced in persistedMaestroBundlesByShard
// for the same shard.
func (c *deleteOrphanedMaestroReadonlyBundles) conditionallyDeleteOrphanReadonlyBundleCandidates(
	ctx context.Context,
	maestroClientsByShard map[string]*shardMaestroClient,
	candidates []orphanReadonlyBundleDeleteCandidate,
	persistedMaestroBundlesByShard maestroBundleNamesByShard,
) error {
	var syncErrors []error
	for _, cand := range candidates {
		csShardID := cand.csShardID
		candidateMaestroBundle := cand.bundle
		shardEntry, ok := maestroClientsByShard[csShardID]
		if !ok {
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("no Maestro client for shard %s when deleting bundle %q", csShardID, candidateMaestroBundle.Name)))
			continue
		}
		maestroClient := shardEntry.maestroClient

		shardLogger := utils.LoggerFromContext(ctx).WithValues("csProvisionShardID", csShardID)
		ctxShard := utils.ContextWithLogger(ctx, shardLogger)
		if shardRefSet := persistedMaestroBundlesByShard[csShardID]; shardRefSet != nil {
			if _, referenced := shardRefSet[candidateMaestroBundle.Name]; referenced {
				// If the Maestro bundle is referenced by any persisted Maestro bundle references for that shard, skip it.
				continue
			}
		}

		shardLogger.Info("Deleting orphaned Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		err := maestroClient.Delete(ctxShard, candidateMaestroBundle.Name, metav1.DeleteOptions{})
		if err != nil {
			//  Failure to delete does not end the sync process. We log the error and we continue with the processing of other Maestro bundle deletion candidates.
			syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete Maestro Bundle: %w", err)))
		} else {
			shardLogger.Info("Deleted orphaned Maestro readonly Bundle", "maestroConsumerName", candidateMaestroBundle.Namespace, "maestroAPIMaestroBundleName", candidateMaestroBundle.Name, "maestroAPIMaestroBundleID", candidateMaestroBundle.UID)
		}
	}
	return utils.TrackError(errors.Join(syncErrors...))
}

// clusterProvisionShardIDForServiceProviderCluster returns the Cluster Service provision shard ID for the cluster that owns the SPC.
// skip is true when the parent cluster has no ClusterServiceID yet or when the Cluster associated with the node pool does not exist anymore.
func (c *deleteOrphanedMaestroReadonlyBundles) clusterProvisionShardIDForServiceProviderCluster(ctx context.Context, spc *api.ServiceProviderCluster) (shardID string, skip bool, err error) {
	clusterResourceID := spc.ResourceID.Parent
	if clusterResourceID == nil {
		return "", false, utils.TrackError(fmt.Errorf("ServiceProviderCluster %s has no parent resource ID", spc.ResourceID.String()))
	}
	cluster, err := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if database.IsNotFoundError(err) {
		// if the cluster does not exist, then any maestro resources associated with it are orphans.
		return "", true, nil
	}
	if err != nil {
		return "", false, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	return c.provisionShardIDFromCluster(ctx, cluster)
}

// clusterProvisionShardIDForServiceProviderNodePool returns the Cluster Service provision shard ID for the cluster that owns the node pool.
// skip is true when the parent cluster has no ClusterServiceID yet or when the Cluster associated with the node pool does not exist anymore.
func (c *deleteOrphanedMaestroReadonlyBundles) clusterProvisionShardIDForServiceProviderNodePool(ctx context.Context, spnp *api.ServiceProviderNodePool) (shardID string, skip bool, err error) {
	nodePoolResourceID := spnp.ResourceID.Parent
	if nodePoolResourceID == nil {
		return "", false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no parent resource ID", spnp.ResourceID.String()))
	}
	clusterResourceID := nodePoolResourceID.Parent
	if clusterResourceID == nil {
		return "", false, utils.TrackError(fmt.Errorf("ServiceProviderNodePool %s has no grandparent cluster resource ID", spnp.ResourceID.String()))
	}
	cluster, err := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if database.IsNotFoundError(err) {
		// if the cluster does not exist, then any maestro resources associated with it are orphans.
		return "", true, nil
	}
	if err != nil {
		return "", false, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	return c.provisionShardIDFromCluster(ctx, cluster)
}

// provisionShardIDFromCluster resolves the provision shard for a Cosmos cluster document. skip is true when ClusterServiceID
// is unset so the cluster is not yet registered with Cluster Service (same gate as create-*-scoped Maestro bundle controllers).
func (c *deleteOrphanedMaestroReadonlyBundles) provisionShardIDFromCluster(ctx context.Context, cluster *api.HCPOpenShiftCluster) (shardID string, skip bool, err error) {
	if cluster.ServiceProviderProperties.ClusterServiceID == nil || len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return "", true, nil
	}
	// TODO We get the provision shard ID from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account that at some point in the future we will implement migration between management
	// clusters, where a cluster could have bundles allocated to different provision shards at the same time? For now
	// we assume that the cluster is associated to a single provision shard at a time.
	clusterCSShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, *cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return "", false, utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
	}
	return clusterCSShard.ID(), false, nil
}

// buildClusterScopedMaestroAPIMaestroBundleNamesByShard maps provision shard ID to the set of Maestro API bundle names referenced by
// ServiceProviderClusters grouped under that shard (shard assignment is already resolved in spcsByShard). Nil list entries or empty
// maestroAPIMaestroBundleName return an error so the reference set cannot silently omit in-use bundles.
func (c *deleteOrphanedMaestroReadonlyBundles) buildClusterScopedMaestroAPIMaestroBundleNamesByShard(spcsByShard map[string][]*api.ServiceProviderCluster) (maestroBundleNamesByShard, error) {
	out := make(maestroBundleNamesByShard)

	for shardID, spcs := range spcsByShard {
		// If it is the first time we are processing this shard we initialize the map entry for it
		if out[shardID] == nil {
			out[shardID] = make(map[string]struct{})
		}
		// We iterate over the ServiceProviderClusters on the shard and we add the Maestro API Maestro bundle names to the map.
		for _, spc := range spcs {
			if spc == nil {
				return nil, utils.TrackError(fmt.Errorf("nil ServiceProviderCluster under provision shard %s", shardID))
			}
			for i, ref := range spc.Status.MaestroReadonlyBundles {
				if ref == nil {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderCluster %s: MaestroReadonlyBundles[%d] is nil", spc.ResourceID.String(), i))
				}
				if ref.MaestroAPIMaestroBundleName == "" {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderCluster %s: MaestroReadonlyBundles[%d] (internal name %q) has empty maestroAPIMaestroBundleName", spc.ResourceID.String(), i, ref.Name))
				}
				out[shardID][ref.MaestroAPIMaestroBundleName] = struct{}{}
			}
		}
	}

	return out, nil
}

// buildNodePoolScopedMaestroAPIMaestroBundleNamesByShard builds a map of provision shard ID to the set of Maestro API bundle names referenced by
// ServiceProviderNodePools grouped under that shard (shard assignment is already resolved in spnpsByShard). Nil list entries or empty
// maestroAPIMaestroBundleName return an error so the reference set cannot silently omit in-use bundles.
func (c *deleteOrphanedMaestroReadonlyBundles) buildNodePoolScopedMaestroAPIMaestroBundleNamesByShard(spnpsByShard map[string][]*api.ServiceProviderNodePool) (maestroBundleNamesByShard, error) {
	out := make(maestroBundleNamesByShard)

	for shardID, spnps := range spnpsByShard {
		// If it is the first time we are processing this shard we initialize the map entry for it
		if out[shardID] == nil {
			out[shardID] = make(map[string]struct{})
		}
		// We iterate over the ServiceProviderNodePools on the shard and we add the Maestro API Maestro bundle names to the map.
		for _, spnp := range spnps {
			if spnp == nil {
				return nil, utils.TrackError(fmt.Errorf("nil ServiceProviderNodePool under provision shard %s", shardID))
			}
			for i, ref := range spnp.Status.MaestroReadonlyBundles {
				if ref == nil {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderNodePool %s: MaestroReadonlyBundles[%d] is nil", spnp.ResourceID.String(), i))
				}
				if ref.MaestroAPIMaestroBundleName == "" {
					return nil, utils.TrackError(fmt.Errorf("serviceProviderNodePool %s: MaestroReadonlyBundles[%d] (internal name %q) has empty maestroAPIMaestroBundleName", spnp.ResourceID.String(), i, ref.Name))
				}
				out[shardID][ref.MaestroAPIMaestroBundleName] = struct{}{}
			}
		}
	}

	return out, nil
}

func (c *deleteOrphanedMaestroReadonlyBundles) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	// start up your worker threads based on threadiness.  Some controllers
	// have multiple kinds of workers
	for i := 0; i < threadiness; i++ {
		// runWorker will loop until "something bad" happens.  The .Until will
		// then rekick the worker after one second
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// We run this periodically enqueuing an arbitrary item named "doWork" to trigger the sync.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedMaestroReadonlyBundles) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *deleteOrphanedMaestroReadonlyBundles) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	controllerutils.ReconcileTotal.WithLabelValues(c.name).Inc()
	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
