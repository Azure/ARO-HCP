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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/listers"
	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterCreate struct {
	name string

	azureLocation               string
	activeOperationScanInterval time.Duration
	subscriptionLister          listers.SubscriptionLister
	cosmosClient                database.DBClient
	clusterServiceClient        ocm.ClusterServiceClientSpec
	notificationClient          *http.Client

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[OperationKey]
}

// NewOperationClusterCreateController periodically lists all clusters and for each out when the cluster was created and its state.
func NewOperationClusterCreateController(
	azureLocation string,
	activeOperationScanInterval time.Duration,
	subscriptionLister listers.SubscriptionLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) Controller {
	c := &operationClusterCreate{
		name:                        "OperationClusterCreate",
		azureLocation:               azureLocation,
		activeOperationScanInterval: activeOperationScanInterval,
		subscriptionLister:          subscriptionLister,
		cosmosClient:                cosmosClient,
		clusterServiceClient:        clusterServiceClient,
		notificationClient:          notificationClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[OperationKey](),
			workqueue.TypedRateLimitingQueueConfig[OperationKey]{
				Name: "operation-cluster-create",
			},
		),
	}

	return c
}

func (c *operationClusterCreate) synchronizeOperation(ctx context.Context, key OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}

	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, opError, err := oldoperationscanner.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status", "newStatus", newOperationStatus)

	// Create a Cosmos DB billing document if a Create operation is successful.
	// Do this before calling updateOperationStatus so that in case of error the
	// backend will retry by virtue of the operation document still having a non-
	// terminal status.
	if newOperationStatus == arm.ProvisioningStateSucceeded {
		cluster, err := c.cosmosClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}

		logger.Info("creating billing, interestingly not based on now")
		err = oldoperationscanner.CreateBillingDocument(
			ctx,
			c.cosmosClient,
			c.azureLocation,
			operation.ExternalID.ResourceGroupName,
			ptr.Deref(cluster.SystemData.CreatedAt, time.Time{}),
			operation)
		if err != nil {
			return utils.TrackError(err)
		}

	}

	logger.Info("updating status")
	err = database.UpdateOperationStatus(ctx, c.cosmosClient, operation, newOperationStatus, opError, PostAsyncNotification(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// PostAsyncNotification submits an POST request with status payload to the given URL.
func PostAsyncNotification(notificationClient *http.Client) database.PostAsyncNotificationFunc {
	return func(ctx context.Context, operation *api.Operation) error {
		return oldoperationscanner.PostAsyncNotification(ctx, notificationClient, operation)
	}
}

func (c *operationClusterCreate) SyncOnce(ctx context.Context, keyObj any) error {
	key := keyObj.(OperationKey)

	syncErr := c.synchronizeOperation(ctx, key) // we'll handle this is a moment.

	parentResourceID := key.GetParentResourceID()
	controllerWriteErr := writeController(
		ctx,
		c.cosmosClient.HCPClusters(key.SubscriptionID, parentResourceID.ResourceGroupName).Controllers(parentResourceID.Name),
		c.name,
		key.initialController,
		reportSyncError(syncErr),
	)

	return errors.Join(syncErr, controllerWriteErr)
}

func (c *operationClusterCreate) queueAllActiveOperations(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error("unable to list subscriptions", "error", err)
	}
	for _, subscription := range allSubscriptions {
		allActiveOperations := c.cosmosClient.Operations(subscription.ResourceID.SubscriptionID).ListActiveOperations(nil)

		for _, activeOperation := range allActiveOperations.Items(ctx) {
			if activeOperation.Request != database.OperationRequestCreate {
				continue
			}
			if activeOperation.ExternalID == nil || !strings.EqualFold(activeOperation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
				continue
			}
			c.queue.Add(OperationKey{
				SubscriptionID:   activeOperation.ExternalID.SubscriptionID,
				OperationName:    activeOperation.ResourceID.Name,
				ParentResourceID: activeOperation.ExternalID.String(),
			})
		}
		if err := allActiveOperations.GetError(); err != nil {
			logger.Error("unable to iterate over active operations", "error", err, "subscription_id", subscription.ResourceID.SubscriptionID)
		}
	}
}

// Run check do_nothing.go for basic doc details.
func (c *operationClusterCreate) Run(ctx context.Context, threadiness int) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	logger := utils.LoggerFromContext(ctx)
	logger.With("controller_name", c.name)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting")

	for i := 0; i < threadiness; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	go wait.JitterUntilWithContext(ctx, c.queueAllActiveOperations, c.activeOperationScanInterval, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

// runWorker check do_nothing.go for doc details.
func (c *operationClusterCreate) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem check do_nothing.go for doc details.
func (c *operationClusterCreate) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = ref.AddLoggerValues(logger)
	ctx = utils.ContextWithLogger(ctx, logger)

	err := c.SyncOnce(ctx, ref)
	if err == nil {
		c.queue.Forget(ref)
		return true
	}

	utilruntime.HandleErrorWithContext(ctx, err, "Error syncing; requeuing for later retry", "objectReference", ref)
	c.queue.AddRateLimited(ref)

	return true
}
