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
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterDelete struct {
	name string

	clock                       utilsclock.PassiveClock
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

// NewOperationClusterDeleteController periodically lists all clusters and for each out when the cluster was deleted and its state.
func NewOperationClusterDeleteController(
	azureLocation string,
	activeOperationScanInterval time.Duration,
	subscriptionLister listers.SubscriptionLister,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) Controller {
	c := &operationClusterDelete{
		name:                        "OperationClusterDelete",
		clock:                       utilsclock.RealClock{},
		azureLocation:               azureLocation,
		activeOperationScanInterval: activeOperationScanInterval,
		subscriptionLister:          subscriptionLister,
		cosmosClient:                cosmosClient,
		clusterServiceClient:        clusterServiceClient,
		notificationClient:          notificationClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[OperationKey](),
			workqueue.TypedRateLimitingQueueConfig[OperationKey]{
				Name: "operation-cluster-delete",
			},
		),
	}

	return c
}

func (c *operationClusterDelete) synchronizeOperation(ctx context.Context, key OperationKey) error {
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
	var ocmGetClusterError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetClusterError) && ocmGetClusterError.Status() == http.StatusNotFound {
		logger.Info("cluster was deleted")

		// Update the Cosmos DB billing document with a deletion timestamp.
		// Do this before calling setDeleteOperationAsCompleted so that in
		// case of error the backend will retry by virtue of the operation
		// document still having a non-terminal status.
		err = c.markBillingDocumentDeleted(ctx, operation.ExternalID)
		if err != nil {
			return utils.TrackError(err)
		}

		err = oldoperationscanner.SetDeleteOperationAsCompleted(ctx, c.cosmosClient, operation, PostAsyncNotification(c.notificationClient))
		if err != nil {
			logger.Error("Failed to handle a completed deletion", "error", err)
		}
	}
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := oldoperationscanner.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = database.UpdateOperationStatus(ctx, c.cosmosClient, operation, newOperationStatus, newOperationError, PostAsyncNotification(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// markBillingDocumentDeleted patches a Cosmos DB document in the Billing
// container to add a deletion timestamp.
func (c *operationClusterDelete) markBillingDocumentDeleted(ctx context.Context, clusterResourceID *azcorearm.ResourceID) error {
	logger := utils.LoggerFromContext(ctx)

	var patchOperations database.BillingDocumentPatchOperations
	patchOperations.SetDeletionTime(c.clock.Now())
	err := c.cosmosClient.PatchBillingDoc(ctx, clusterResourceID, patchOperations)
	if err == nil {
		logger.Info("Updated billing for cluster deletion")
	} else if database.IsResponseError(err, http.StatusNotFound) {
		// Log the error but proceed with normal processing.
		logger.Info("No billing document found")
		err = nil
	}

	return err
}

func (c *operationClusterDelete) SyncOnce(ctx context.Context, keyObj any) error {
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

func (c *operationClusterDelete) queueAllActiveOperations(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error("unable to list subscriptions", "error", err)
	}
	for _, subscription := range allSubscriptions {
		allActiveOperations := c.cosmosClient.Operations(subscription.ResourceID.SubscriptionID).ListActiveOperations(nil)

		for _, activeOperation := range allActiveOperations.Items(ctx) {
			if activeOperation.Request != database.OperationRequestDelete {
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
func (c *operationClusterDelete) Run(ctx context.Context, threadiness int) {
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
func (c *operationClusterDelete) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem check do_nothing.go for doc details.
func (c *operationClusterDelete) processNextWorkItem(ctx context.Context) bool {
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
