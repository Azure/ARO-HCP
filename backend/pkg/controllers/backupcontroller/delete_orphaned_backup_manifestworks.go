// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// TODO: extract shared orphan-cleanup infrastructure (SPC listing, shard index, workqueue boilerplate)
// into a common helper — duplicated from deleteOrphanedMaestroReadonlyBundles
package backupcontroller

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

type deleteOrphanedBackupManifestWorks struct {
	name string

	cosmosClient database.DBClient

	queue workqueue.TypedRateLimitingInterface[string]

	clusterServiceClient ocm.ClusterServiceClientSpec

	maestroClientBuilder maestro.MaestroClientBuilder

	maestroSourceEnvironmentIdentifier string
}

// NewDeleteOrphanedBackupManifestWorksController periodically finds backup ManifestWorks
// in Maestro that are not referenced by any ServiceProviderCluster and deletes them.
func NewDeleteOrphanedBackupManifestWorksController(
	cosmosClient database.DBClient,
	csClient ocm.ClusterServiceClientSpec,
	maestroClientBuilder maestro.MaestroClientBuilder,
	maestroSourceEnvironmentIdentifier string,
) controllerutils.Controller {
	c := &deleteOrphanedBackupManifestWorks{
		name:                               "DeleteOrphanedBackupManifestWorks",
		cosmosClient:                       cosmosClient,
		clusterServiceClient:               csClient,
		maestroClientBuilder:               maestroClientBuilder,
		maestroSourceEnvironmentIdentifier: maestroSourceEnvironmentIdentifier,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedBackupManifestWorks",
			},
		),
	}

	return c
}

// backupShardServiceProviderClusters represents the set of ServiceProviderClusters whose backup
// ManifestWorks are associated to a Provision Shard (Management Cluster).
type backupShardServiceProviderClusters struct {
	maestroClient           maestro.Client
	maestroClientCancelFunc context.CancelFunc
	serviceProviderClusters []*api.ServiceProviderCluster
}

func cancelBackupMaestroClientsInIndex(index map[string]*backupShardServiceProviderClusters) {
	for _, shardToSPCs := range index {
		shardToSPCs.maestroClientCancelFunc()
	}
}

// SyncOnce lists all backup ManifestWorks across all provision shards and deletes any
// that are not referenced by a ServiceProviderCluster's BackupScheduleManifestWorkName.
func (c *deleteOrphanedBackupManifestWorks) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned backup ManifestWorks")

	allServiceProviderClusters, err := c.getAllServiceProviderClusters(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get all ServiceProviderClusters: %w", err))
	}
	logger.Info(fmt.Sprintf("Found %d ServiceProviderClusters", len(allServiceProviderClusters)))

	logger.Info("Building Provision Shard to ServiceProviderClusters index")
	index, err := c.buildProvisionShardToServiceProviderClustersIndex(ctx, allServiceProviderClusters)
	defer cancelBackupMaestroClientsInIndex(index)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to build Provision Shard to ServiceProviderClusters index: %w", err))
	}
	logger.Info(fmt.Sprintf("Built Provision Shard to ServiceProviderClusters index with %d keys", len(index)))

	logger.Info("Ensuring orphaned backup ManifestWorks are deleted")
	err = c.ensureOrphanedBackupManifestWorksAreDeleted(ctx, index)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to ensure orphaned backup ManifestWorks are deleted: %w", err))
	}
	logger.Info("End of orphaned backup ManifestWorks sync")

	return nil
}

func (c *deleteOrphanedBackupManifestWorks) getAllServiceProviderClusters(ctx context.Context) ([]*api.ServiceProviderCluster, error) {
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

func (c *deleteOrphanedBackupManifestWorks) buildProvisionShardToServiceProviderClustersIndex(
	ctx context.Context,
	allServiceProviderClusters []*api.ServiceProviderCluster,
) (map[string]*backupShardServiceProviderClusters, error) {
	index := map[string]*backupShardServiceProviderClusters{}

	provisionShardsIterator := c.clusterServiceClient.ListProvisionShards()
	for provisionShard := range provisionShardsIterator.Items(ctx) {
		maestroClientCtx, cancel := context.WithCancel(ctx)
		maestroClient, err := c.createMaestroClientFromProvisionShard(maestroClientCtx, provisionShard)
		if err != nil {
			cancel()
			return index, utils.TrackError(fmt.Errorf("failed to create Maestro client: %w", err))
		}
		index[provisionShard.ID()] = &backupShardServiceProviderClusters{
			maestroClient:           maestroClient,
			maestroClientCancelFunc: cancel,
			serviceProviderClusters: []*api.ServiceProviderCluster{},
		}
	}

	for _, spc := range allServiceProviderClusters {
		clusterResourceID := spc.ResourceID.Parent
		cluster, err := c.cosmosClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
		if err != nil {
			return index, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
		}
		clusterCSShard, err := c.clusterServiceClient.GetClusterProvisionShard(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
		if err != nil {
			return index, utils.TrackError(fmt.Errorf("failed to get Cluster Provision Shard: %w", err))
		}

		entry, ok := index[clusterCSShard.ID()]
		if !ok {
			return index, utils.TrackError(fmt.Errorf("provision shard %s for cluster %s is not present in provision shards index", clusterCSShard.ID(), cluster.Name))
		}
		entry.serviceProviderClusters = append(entry.serviceProviderClusters, spc)
	}

	return index, nil
}

func (c *deleteOrphanedBackupManifestWorks) ensureOrphanedBackupManifestWorksAreDeleted(
	ctx context.Context,
	index map[string]*backupShardServiceProviderClusters,
) error {
	logger := utils.LoggerFromContext(ctx)
	var syncErrors []error

	for csShardID, shardToSPCs := range index {
		logger = logger.WithValues("csProvisionShardID", csShardID)
		ctx = utils.ContextWithLogger(ctx, logger)
		logger.Info("processing cluster service provision shard %s with %d ServiceProviderClusters", csShardID, len(shardToSPCs.serviceProviderClusters))
		maestroClient := shardToSPCs.maestroClient

		listOptions := metav1.ListOptions{
			Limit:         400,
			Continue:      "",
			LabelSelector: fmt.Sprintf("%s=%s", backupScheduleManagedByK8sLabelKey, backupScheduleManagedByK8sLabelValue),
		}
		for {
			maestroBundles, err := maestroClient.List(ctx, listOptions)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to list Maestro Bundles for shard %s: %w", csShardID, err))
			}

			for _, maestroBundle := range maestroBundles.Items {
				// Double-check the label even though Maestro should have filtered
				if maestroBundle.Labels[backupScheduleManagedByK8sLabelKey] != backupScheduleManagedByK8sLabelValue {
					continue
				}

				found := slices.ContainsFunc(shardToSPCs.serviceProviderClusters, func(spc *api.ServiceProviderCluster) bool {
					return spc.Status.BackupScheduleManifestWorkName == maestroBundle.Name
				})
				if found {
					continue
				}

				logger.Info("Deleting orphaned backup ManifestWork", "maestroBundleMetadataName", maestroBundle.Name, "maestroConsumerName", maestroBundle.Namespace, "maestroBundleID", maestroBundle.UID)
				err := maestroClient.Delete(ctx, maestroBundle.Name, metav1.DeleteOptions{})
				if err != nil {
					syncErrors = append(syncErrors, utils.TrackError(fmt.Errorf("failed to delete backup ManifestWork: %w", err)))
				} else {
					logger.Info("Deleted orphaned backup ManifestWork", "maestroBundleMetadataName", maestroBundle.Name, "maestroConsumerName", maestroBundle.Namespace, "maestroBundleID", maestroBundle.UID)
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

func (c *deleteOrphanedBackupManifestWorks) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for range threadiness {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedBackupManifestWorks) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *deleteOrphanedBackupManifestWorks) processNextWorkItem(ctx context.Context) bool {
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

func (c *deleteOrphanedBackupManifestWorks) createMaestroClientFromProvisionShard(
	ctx context.Context, provisionShard *arohcpv1alpha1.ProvisionShard,
) (maestro.Client, error) {
	provisionShardMaestroConsumerName := provisionShard.MaestroConfig().ConsumerName()
	provisionShardMaestroRESTAPIEndpoint := provisionShard.MaestroConfig().RestApiConfig().Url()
	provisionShardMaestroGRPCAPIEndpoint := provisionShard.MaestroConfig().GrpcApiConfig().Url()
	maestroSourceID := maestro.GenerateMaestroSourceID(c.maestroSourceEnvironmentIdentifier, provisionShard.ID())

	return c.maestroClientBuilder.NewClient(ctx, provisionShardMaestroRESTAPIEndpoint, provisionShardMaestroGRPCAPIEndpoint, provisionShardMaestroConsumerName, maestroSourceID)
}
