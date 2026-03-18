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

package managementclustercontrollers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

const controllerName = "ManagementClusterSync"

var _ controllerutils.Controller = &managementClusterSyncController{}

type managementClusterSyncController struct {
	name string

	clusterServiceClient    ocm.ClusterServiceClientSpec
	managementClusterCRUD   database.ManagementClusterCRUD
	managementClusterLister listers.ManagementClusterLister

	resyncDuration time.Duration
	queue          workqueue.TypedRateLimitingInterface[string]
}

// NewManagementClusterSyncController creates a controller that periodically lists
// all management clusters from Cluster Service and upserts them into CosmosDB.
func NewManagementClusterSyncController(
	clusterServiceClient ocm.ClusterServiceClientSpec,
	managementClusterCRUD database.ManagementClusterCRUD,
	managementClusterLister listers.ManagementClusterLister,
) controllerutils.Controller {
	return &managementClusterSyncController{
		name:                    controllerName,
		clusterServiceClient:    clusterServiceClient,
		managementClusterCRUD:   managementClusterCRUD,
		managementClusterLister: managementClusterLister,
		resyncDuration:          10 * time.Minute,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: controllerName,
			},
		),
	}
}

func (c *managementClusterSyncController) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing management clusters from Cluster Service")

	return utils.TrackError(c.syncAllManagementClusters(ctx))
}

// syncAllManagementClusters lists all provision shards from Cluster Service and
// upserts them into Cosmos. Note: this is an additive sync only — management
// clusters removed from CS are not pruned from Cosmos. This is intentional:
// Cosmos is becoming the source of truth, and the admin API registration path
// will eventually replace this sync controller. Decommissioning a management
// cluster will be handled explicitly when the time comes.
func (c *managementClusterSyncController) syncAllManagementClusters(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	iter := c.clusterServiceClient.ListProvisionShards()
	var syncErrors []error

	for csShard := range iter.Items(ctx) {
		if err := c.syncProvisionShard(ctx, csShard); err != nil {
			syncErrors = append(syncErrors, err)
		}
	}

	if err := iter.GetError(); err != nil {
		logger.Error(err, "failed to list management clusters from Cluster Service")
		syncErrors = append(syncErrors, err)
	}

	return errors.Join(syncErrors...)
}

// syncProvisionShard converts a single CS provision shard and upserts it into Cosmos.
func (c *managementClusterSyncController) syncProvisionShard(ctx context.Context, csShard *arohcpv1alpha1.ProvisionShard) error {
	logger := utils.LoggerFromContext(ctx).WithValues("cs_shard_id", csShard.ID(), "aks_resource_id", csShard.AzureShard().AksManagementClusterResourceId())

	convertedManagementCluster, err := ocm.ConvertCSManagementClusterToInternal(csShard)
	if err != nil {
		return fmt.Errorf("failed to convert management cluster: %w", err)
	}

	existing, err := c.managementClusterLister.GetByCSProvisionShardID(ctx, csShard.ID())
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return fmt.Errorf("management cluster for shard %s: %w", csShard.ID(), err)
	}

	if database.IsResponseError(err, http.StatusNotFound) {
		if errs := validation.ValidateManagementClusterCreate(ctx, convertedManagementCluster); errs.ToAggregate() != nil {
			return fmt.Errorf("management cluster %s validation failed: %w", convertedManagementCluster.ResourceID, errs.ToAggregate())
		}
		created, err := c.managementClusterCRUD.Create(ctx, convertedManagementCluster, nil)
		if err != nil {
			return fmt.Errorf("management cluster %s: %w", convertedManagementCluster.ResourceID, err)
		}
		logger.Info("created management cluster", "resource_id", created.ResourceID)
		return nil
	}

	logger = logger.WithValues("resource_id", existing.ResourceID)
	managementClusterToWrite := existing.DeepCopy()

	// SchedulingPolicy is currently synced from Cluster Service provision shard
	// status. This is a temporary arrangement during the CS-to-Cosmos migration.
	// Once we populate management clusters via rollout pipelines and manage them
	// via Geneva Action, this controller will be removed.
	managementClusterToWrite.Spec.SchedulingPolicy = convertedManagementCluster.Spec.SchedulingPolicy
	for _, cond := range convertedManagementCluster.Status.Conditions {
		controllerutils.SetCondition(&managementClusterToWrite.Status.Conditions, cond)
	}
	if equality.Semantic.DeepEqual(existing, managementClusterToWrite) {
		logger.V(1).Info("management cluster unchanged, skipping update")
		return nil
	}
	if errs := validation.ValidateManagementClusterUpdate(ctx, managementClusterToWrite, existing); errs.ToAggregate() != nil {
		return fmt.Errorf("management cluster %s validation failed: %w", existing.ResourceID, errs.ToAggregate())
	}
	if _, err = c.managementClusterCRUD.Replace(ctx, managementClusterToWrite, nil); err != nil {
		return fmt.Errorf("management cluster %s: %w", existing.ResourceID, err)
	}
	logger.Info("updated management cluster")
	return nil
}

func (c *managementClusterSyncController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddControllerName(c.name)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	// We run this periodically enqueuing an arbitrary item named "doWork" to trigger the sync.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, c.resyncDuration, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *managementClusterSyncController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *managementClusterSyncController) processNextWorkItem(ctx context.Context) bool {
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
