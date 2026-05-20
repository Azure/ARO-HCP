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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/fleet"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const controllerName = "ManagementClusterMigration"

var _ controllerutils.Controller = &managementClusterMigrationController{}

type managementClusterMigrationController struct {
	name string

	clusterServiceClient    ocm.ClusterServiceClientSpec
	fleetDBClient           database.FleetDBClient
	stampLister             dblisters.StampLister
	managementClusterLister dblisters.ManagementClusterLister

	resyncDuration time.Duration
	queue          workqueue.TypedRateLimitingInterface[string]
}

// NewManagementClusterMigrationController creates a controller that periodically lists
// all management clusters from Cluster Service and upserts them into CosmosDB.
func NewManagementClusterMigrationController(
	clusterServiceClient ocm.ClusterServiceClientSpec,
	fleetDBClient database.FleetDBClient,
	stampLister dblisters.StampLister,
	managementClusterLister dblisters.ManagementClusterLister,
) controllerutils.Controller {
	return &managementClusterMigrationController{
		name:                    controllerName,
		clusterServiceClient:    clusterServiceClient,
		fleetDBClient:           fleetDBClient,
		stampLister:             stampLister,
		managementClusterLister: managementClusterLister,
		resyncDuration:          30 * time.Minute,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: controllerName,
			},
		),
	}
}

func (c *managementClusterMigrationController) SyncOnce(ctx context.Context, _ any) error {
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
func (c *managementClusterMigrationController) syncAllManagementClusters(ctx context.Context) error {
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
func (c *managementClusterMigrationController) syncProvisionShard(ctx context.Context, csShard *arohcpv1alpha1.ProvisionShard) error {
	logger := utils.LoggerFromContext(ctx).WithValues("cs_shard_href", csShard.HREF(), "aks_resource_id", csShard.AzureShard().AksManagementClusterResourceId())

	convertedManagementCluster, err := ocm.ConvertCSManagementClusterToInternal(csShard)
	if err != nil {
		return fmt.Errorf("failed to convert management cluster: %w", err)
	}

	stampIdentifier := convertedManagementCluster.GetStampIdentifier()

	if err := c.ensureStamp(ctx, stampIdentifier); err != nil {
		return fmt.Errorf("stamp %s: %w", stampIdentifier, err)
	}

	managementClusterCRUD := c.fleetDBClient.Stamps().ManagementClusters(stampIdentifier)

	existing, err := c.managementClusterLister.Get(ctx, stampIdentifier)
	if err != nil && !database.IsNotFoundError(err) {
		return fmt.Errorf("management cluster %s: %w", stampIdentifier, err)
	}

	if database.IsNotFoundError(err) {
		// The lister cache may be stale (e.g. on startup). Check CosmosDB directly
		// before attempting to create.
		existing, err = managementClusterCRUD.Get(ctx, fleet.ManagementClusterResourceName)
		if err != nil && !database.IsNotFoundError(err) {
			return fmt.Errorf("management cluster %s: %w", stampIdentifier, err)
		}
	}

	if database.IsNotFoundError(err) {
		created, err := managementClusterCRUD.Create(ctx, convertedManagementCluster, nil)
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
		apimeta.SetStatusCondition(&managementClusterToWrite.Status.Conditions, cond)
	}
	if equality.Semantic.DeepEqual(existing, managementClusterToWrite) {
		logger.V(1).Info("management cluster unchanged, skipping update")
		return nil
	}
	if _, err = managementClusterCRUD.Replace(ctx, managementClusterToWrite, existing, nil); err != nil {
		return fmt.Errorf("management cluster %s: %w", existing.ResourceID, err)
	}
	logger.Info("updated management cluster")
	return nil
}

// ensureStamp upserts the Stamp record. Stamps synced from Cluster Service
// are auto-approved since the provision shard already exists.
func (c *managementClusterMigrationController) ensureStamp(ctx context.Context, stampIdentifier string) error {
	logger := utils.LoggerFromContext(ctx)

	existing, err := c.stampLister.Get(ctx, stampIdentifier)
	if err != nil && !database.IsNotFoundError(err) {
		return fmt.Errorf("stamp %s: %w", stampIdentifier, err)
	}

	approvedCondition := metav1.Condition{
		Type:               string(fleet.StampConditionApproved),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             string(fleet.StampConditionReasonAutoApproved),
		Message:            "Synced from Cluster Service provision shard",
	}

	stampsCRUD := c.fleetDBClient.Stamps()

	if database.IsNotFoundError(err) {
		// The lister cache may be stale (e.g. on startup). Check CosmosDB directly
		// before attempting to create.
		existing, err = stampsCRUD.Get(ctx, stampIdentifier)
		if err != nil && !database.IsNotFoundError(err) {
			return fmt.Errorf("stamp %s: %w", stampIdentifier, err)
		}
	}

	if database.IsNotFoundError(err) {
		stampResourceID, err := fleet.ToStampResourceID(stampIdentifier)
		if err != nil {
			return fmt.Errorf("invalid stamp identifier %q: %w", stampIdentifier, err)
		}
		stamp := &fleet.Stamp{
			CosmosMetadata: api.CosmosMetadata{
				ResourceID: stampResourceID,
			},
			ResourceID: stampResourceID,
		}
		apimeta.SetStatusCondition(&stamp.Status.Conditions, approvedCondition)

		created, err := stampsCRUD.Create(ctx, stamp, nil)
		if err != nil {
			return fmt.Errorf("stamp %s: %w", stampIdentifier, err)
		}
		logger.Info("created stamp", "stamp_identifier", stampIdentifier, "resource_id", created.CosmosMetadata.ResourceID)
		return nil
	}

	stampToWrite := existing.DeepCopy()
	apimeta.SetStatusCondition(&stampToWrite.Status.Conditions, approvedCondition)
	if equality.Semantic.DeepEqual(existing, stampToWrite) {
		logger.V(1).Info("stamp unchanged, skipping update")
		return nil
	}

	if _, err := stampsCRUD.Replace(ctx, stampToWrite, existing, nil); err != nil {
		return fmt.Errorf("stamp %s: %w", stampIdentifier, err)
	}
	logger.Info("updated stamp", "stamp_identifier", stampIdentifier)
	return nil
}

func (c *managementClusterMigrationController) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	ctx = utils.ContextWithControllerName(ctx, c.name)
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

func (c *managementClusterMigrationController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *managementClusterMigrationController) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = controllerutils.AddLoggerValues(logger, ref)
	ctx = utils.ContextWithLogger(ctx, logger)

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
