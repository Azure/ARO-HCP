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
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedMaestroReadonlyBundles struct {
	name string

	cosmosClient database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string
}

// NewDeleteOrphanedMaestroReadonlyBundlesController periodically looks for cosmos objs that don't have an owning cluster and deletes them.
func NewDeleteOrphanedMaestroReadonlyBundlesController(cosmosClient database.DBClient, csClient ocm.ClusterServiceClientSpec, maestroClientBuilder maestro.MaestroClientBuilder, maestroSourceEnvironmentIdentifier string) controllerutils.Controller {
	c := &deleteOrphanedMaestroReadonlyBundles{
		name:                               "DeleteOrphanedMaestroReadonlyBundles",
		cosmosClient:                       cosmosClient,
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

// SyncOnce current algorithm is:
//  1. List all ServiceProviderClusters
//  2. Build a map that contains an association between the CS Provision Shard ID and the ServiceProviderClusters that
//     are allocated to that shard. IMPORTANT NOTE: This assumes that the maestro server associated to the provision shard
//     has resources with always the same source ID. If it turns out we cannot have this assumption this logic would not
//     be good enough. In that case it might be necessary to store to what source ID a Maestro Bundle/set of Maestro Bundles
//     belongs to but then the instantiation of the Maestro client needs to be done differently as its scoped to
//     Maestro Consumer Name + Maestro Source ID. We know for example that in the CSPR environment different CS instances
//     have different Maestro source IDs using the same Maestro Server.
//  3. For each shard in the map previously built, we use the Maestro API to list all the Maestro Bundles, and for each
//     Maestro Bundle we check if its Maestro API Maestro Bundle Name matches any of the MaestroBundleReferences in any
//     of the ServiceProviderClusters allocated to that shard. If it does not match, we delete the Maestro Bundle. If it
//     matches we leave it alone
//
// We considered using the Maestro API Maestro UID which is globally unique but it's possible that there's a scenario
// where the maestro readonly bundles controller creates a bundle reference in the ServiceProviderClass, created the bundle
// using the Maestro API but failed to persist it in the database for some reason and the cluster ended up being deleted.
// In that scenario we would not have the Maestro UID to identify the Maestro Bundle and we would not be able to delete it. Furthermore
// we should not use the fact of the UID being empty as the trigger to delete because it could be that it's being created
// and not yet persisted in Cosmos.
func (c *deleteOrphanedMaestroReadonlyBundles) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned Maestro Readonly Bundles")
	allServiceProviderClusters, err := c.getAllServiceProviderClusters(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get all ServiceProviderClusters: %w", err))
	}
	logger.Info(fmt.Sprintf("Found %d ServiceProviderClusters", len(allServiceProviderClusters)))

	logger.Info("Building Provision Shard to ServiceProviderClusters index")
	provisionShardsToServiceProviderClustersIndex, err := c.buildProvisionShardToServiceProviderClustersIndex(ctx, allServiceProviderClusters)
	// We cancel the Maestro clients when the sync is done. This is important to avoid leaking resources when the sync is done.
	defer cancelMaestroClientsInIndex(provisionShardsToServiceProviderClustersIndex) // close on success or error (index may be partial on error)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Provision Shard to ServiceProviderClusters index: %w", err))
	}
	logger.Info(fmt.Sprintf("Built Provision Shard to ServiceProviderClusters index with %d keys", len(provisionShardsToServiceProviderClustersIndex)))

	logger.Info("Ensuring orphaned Maestro Readonly Bundles are deleted")
	err = c.ensureOrphanedMaestroReadonlyBundlesAreDeleted(ctx, provisionShardsToServiceProviderClustersIndex)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure orphaned Maestro Bundles are deleted: %w", err))
	}
	logger.Info("End of orphaned Maestro Readonly Bundles sync")

	return nil
}

// getAllServiceProviderClusters returns the list of all ServiceProviderClusters in the database.
func (c *deleteOrphanedMaestroReadonlyBundles) getAllServiceProviderClusters(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
	// We list all ServiceProviderClusters in chunks of 500 to avoid putting
	// too much pressure on the Cosmos DB.
	// Any failure to iterate over the ServiceProviderclusters ends the sync process because otherwise
	// we would not have the complete information to evaluate the deletion and we could
	// accidentally delete Maestro Bundles that are still in use.
	listOptions := &database.DBClientListResourceDocsOptions{
		PageSizeHint: ptr.To(int32(500)),
	}
	allServiceProviderClusters := []*api.ServiceProviderCluster{}
	for {
		iterator, err := c.cosmosClient.GlobalListers().ServiceProviderClusters().List(ctx, listOptions)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed to list ServiceProviderClusters: %w", err))
		}
		for _, spc := range iterator.Items(ctx) {
			allServiceProviderClusters = append(allServiceProviderClusters, spc)
		}
		err = iterator.GetError()
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("failed iterating ServiceProviderClusters: %w", err))
		}

		continuationToken := iterator.GetContinuationToken()
		if continuationToken == "" {
			break
		}
		listOptions.ContinuationToken = &continuationToken
	}

	return allServiceProviderClusters, nil
}

// provisionShardServiceProviderClusters represents the set of ServiceProviderClusters whose Maestro bundles
// are associated to a Provision Shard (Management Cluster)
type provisionShardServiceProviderClusters struct {
	// maestroClient is the Maestro client to communicate with the Maestro server
	// associated to the provision shard. IMPORTANT NOTE: This assumes that
	// all the resources in the Maestro server associated to the provision shard
	// have the same source ID. If it turns out we cannot have this assumption
	// this logic would not be good enough.
	maestroClient maestro.Client
	// maestroClientCancelFunc is the cancel function to cancel the Maestro client.
	// Calling this function is important when the client is not needed anymore to avoid leaking resources when the sync is done.
	maestroClientCancelFunc context.CancelFunc
	// serviceProviderClusters is the list of ServiceProviderClusters that are allocated to the shard.
	// IMPORTANT NOTE: This assumes that all the resources in the Maestro server associated to the provision shard
	// have the same source ID. If it turns out we cannot have this assumption
	// this logic would not be good enough.
	serviceProviderClusters []*api.ServiceProviderCluster
}

// cancelMaestroClientsInIndex runs the cancel function for each entry in the index.
// The caller of buildProvisionShardToServiceProviderClustersIndex should defer this so the index is cancelled on success or error (on error the index may be partial).
func cancelMaestroClientsInIndex(index map[string]*provisionShardServiceProviderClusters) {
	for _, shardToSPCs := range index {
		shardToSPCs.maestroClientCancelFunc()
	}
}

// buildProvisionShardServiceProviderClustersIndex builds an index of Provision Shard ID to provisionShardServiceProviderClusters.
// The key is the Cluster Service Provision Shard ID. To achieve that the following steps are followed:
//  1. List all the registered provision shards (currently using the Clusters Service API)
//  2. For each provision shard, create a Maestro client.
//  3. For each ServiceProviderCluster, get the Cluster Service Provision Shard ID associated to it (currently using the Clusters Service API) and
//     add the ServiceProviderCluster to the corresponding provisionShardServiceProviderClusters entry in the index.
//
// On error the returned index may be partial (clients created before the error). The caller must close the index in all cases (e.g. defer closeMaestroClientsInIndex).
func (c *deleteOrphanedMaestroReadonlyBundles) buildProvisionShardToServiceProviderClustersIndex(ctx context.Context, allServiceProviderClusters []*api.ServiceProviderCluster) (map[string]*provisionShardServiceProviderClusters, error) {
	provisionShardsToServiceProviderClustersIndex := map[string]*provisionShardServiceProviderClusters{}

	// TODO we list the provision shards from CS but at some point we should have
	// the information in Cosmos and this should be changed to use that instead.
	// TODO should we take into account the provision shard status on what to consider (active, maintenance, offline, ...)?
	// for now we consider all provision shards independently of their status.
	provisionShardsIterator := c.clusterServiceClient.ListProvisionShards()
	for provisionShard := range provisionShardsIterator.Items(ctx) {
		// We create a new context with a cancel function so we can cancel the Maestro client when the sync is done.
		// This is important to avoid leaking resources when the sync is done.
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := c.createMaestroClientFromProvisionShard(maestroClientCtx, provisionShard)
		if err != nil {
			cancel() // on error creating the Maestro client we ensure we cancel the just created context too
			return provisionShardsToServiceProviderClustersIndex, utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
		}
		provisionShardsToServiceProviderClustersIndex[provisionShard.ID()] = &provisionShardServiceProviderClusters{
			maestroClient:           maestroClient,
			maestroClientCancelFunc: cancel,
			serviceProviderClusters: []*api.ServiceProviderCluster{},
		}
	}

	for _, spc := range allServiceProviderClusters {
		clusterResourceID := spc.ResourceID.Parent
		cluster, err := c.cosmosClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
		if err != nil {
			return provisionShardsToServiceProviderClustersIndex, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
		}
		// TODO we should store the provision shard ID somewhere so we don't need to fetch it from cluster service repeatedly
		// TODO should we take into account that at some point in the future we will implement migration between management
		// clusters, where a cluster could have bundles allocated to different provision shards at the same time?
		clusterCSShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			return provisionShardsToServiceProviderClustersIndex, utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
		}

		currentProvisionShardServiceProviderClusters, ok := provisionShardsToServiceProviderClustersIndex[clusterCSShard.ID()]
		if !ok {
			return provisionShardsToServiceProviderClustersIndex, utils.TrackError(fmt.Errorf("provision shard %s for cluster %s is not present in provision shards index", clusterCSShard.ID(), cluster.Name))
		}
		currentProvisionShardServiceProviderClusters.serviceProviderClusters = append(currentProvisionShardServiceProviderClusters.serviceProviderClusters, spc)
	}

	return provisionShardsToServiceProviderClustersIndex, nil
}

// ensureOrphanedMaestroReadonlyBundlesAreDeleted ensures that all the Maestro Readonly Bundles that are not referenced by any
// of the ServiceProviderClusters allocated to a Provision Shard (Management Cluster) are deleted.
// Only Maestro Readonly Bundles that are managed by the maestro create readonly bundles controller are considered.
func (c *deleteOrphanedMaestroReadonlyBundles) ensureOrphanedMaestroReadonlyBundlesAreDeleted(ctx context.Context, provisionShardsToServiceProviderClustersIndex map[string]*provisionShardServiceProviderClusters) error {
	logger := utils.LoggerFromContext(ctx)
	var syncErrors []error
	// We iterate over each shard to list all the maestro bundles associated to that
	// shard (assuming same maestro source ID) using the Maestro API
	for csShardID, shardToSPCs := range provisionShardsToServiceProviderClustersIndex {
		logger = logger.WithValues("csProvisionShardID", csShardID)
		ctx = utils.ContextWithLogger(ctx, logger)
		logger.Info("processing cluster service provision shard %s with %d ServiceProviderClusters", csShardID, len(shardToSPCs.serviceProviderClusters))
		maestroClient := shardToSPCs.maestroClient
		// We list all the Maestro Bundles in chunks of 400 to avoid putting
		// too much pressure on the Maestro API.
		listOptions := metav1.ListOptions{Limit: 400, Continue: ""}
		for {
			maestroBundles, err := maestroClient.List(ctx, listOptions)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to list Maestro Bundles for shard %s: %w", csShardID, err))
			}
			// For each Maestro Bundle retrieved from the Maestro API we check if the Maestro Bundle has the K8s annotation
			// that indicates that the Maestro Bundle is managed by the maestro create readonly bundles controller. If it
			// does not we filter it out. If it is then we check if the Maestro Bundle is referenced by any of the ServiceProviderClusters
			// allocated to that shard by checking the MaestroBundleReferences in the ServiceProviderCluster status and checking if the
			// Maestro API Maestro Bundle Name matches. If it matches, we leave it alone. If it does not match, we delete it.
			for _, maestroBundle := range maestroBundles.Items {
				// TODO Maestro might allow filtering with LabelSelector. If it's supported, do we want to change to
				// create our Maestro Readonly bundles to use a K8s Label instead of a K8s annotation? Labels are more restrictive
				// regarding length and charset restrictions but in this specific case it seems we have fixed length and charsets
				// so it should be possible from that side.
				if maestroBundle.Annotations["aro-hcp.azure.com/readonly-bundle-managed-by"] != "create-maestro-readonly-bundles-controller" {
					continue
				}

				// We iterate over all the Maestro Bundle References among all the Service Provider Clusters allocated to that shard
				// to check if the Maestro Bundle is referenced by any of them.
				found := slices.ContainsFunc(shardToSPCs.serviceProviderClusters, func(spc *api.ServiceProviderCluster) bool {
					found := slices.ContainsFunc(spc.Status.MaestroReadonlyBundles, func(ref *api.MaestroBundleReference) bool {
						// The Maestro API Maestro Bundle Name should be unique within a given Maestro Consumer Name and Maestro Source ID.
						// If we find a match, it means the Maestro Bundle is referenced by the ServiceProviderCluster and we should
						// not delete it.
						return ref.MaestroAPIMaestroBundleName == maestroBundle.Name
					})
					return found
				})
				if found {
					continue
				}

				// TODO it would be nice to be able to log the Maestro Source ID.
				logger.Info("Deleting orphaned Maestro readonly Bundle", "maestroBundleMetadataName", maestroBundle.Name, "maestroConsumerName", maestroBundle.Namespace, "maestroBundleID", maestroBundle.UID)
				err := maestroClient.Delete(ctx, maestroBundle.Name, metav1.DeleteOptions{})
				if err != nil {
					// Failure to delete does not end the sync process. We log the error and we continue with the processing
					// of other Maestro Bundles.
					syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete Maestro Bundle: %w", err)))
				} else {
					logger.Info("Deleted orphaned Maestro readonly Bundle", "maestroBundleMetadataName", maestroBundle.Name, "maestroConsumerName", maestroBundle.Namespace, "maestroBundleID", maestroBundle.UID)
				}

			}
			continuationToken := maestroBundles.GetContinue()
			if continuationToken == "" {
				break
			}
			listOptions.Continue = continuationToken
		}
	}

	return errors.Join(syncErrors...)
}

func (c *deleteOrphanedMaestroReadonlyBundles) Run(ctx context.Context, threadiness int) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

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

// createMaestroClientFromProvisionShard creates a Maestro client for the given provision shard.
// The client is scoped to the Maestro Consumer associated to the provision shard, as well
// as to the the Maestro Source ID associated to the provision shard which is calculated from the provision shard ID and the
// environment specified in c.maestroSourceEnvironmentIdentifier.
func (c *deleteOrphanedMaestroReadonlyBundles) createMaestroClientFromProvisionShard(
	ctx context.Context, provisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := provisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	// This allows us to be able to have visibility on the Maestro Bundles owned by the same source ID for a given
	// provision shard and environment. This should have the same source ID as what CS has in each corresponding environment
	// because otherwise we would not have visibility on the Maestro Bundles owned
	// TODO do we want to use the same source ID that CS uses or do we want intentionally a different one? This has consequences
	// on the visibility of the Maestro Bundles, including processing of events sent by Maestro.
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, provisionShard.ID())

	maestroClient, err := c.maestroClientBuilder.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)

	return maestroClient, err
}
