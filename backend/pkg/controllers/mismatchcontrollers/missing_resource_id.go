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

package mismatchcontrollers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const MissingResourceIDControllerName = "MissingResourceID"

type missingResourceIDController struct {
	name string

	resourcesDBClient database.ResourcesDBClient

	queue workqueue.TypedRateLimitingInterface[string]
}

func NewMissingResourceIDController(
	resourcesDBClient database.ResourcesDBClient,
) controllerutils.Controller {
	c := &missingResourceIDController{
		name:              MissingResourceIDControllerName,
		resourcesDBClient: resourcesDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: MissingResourceIDControllerName,
			},
		),
	}

	return c
}

// shouldDelete returns true if a document without a resourceID should be deleted.
func shouldDelete(doc *database.TypedDocument) bool {
	resourceType := strings.ToLower(doc.ResourceType)
	if len(resourceType) == 0 {
		return false
	}

	switch {
	case strings.Contains(resourceType, "controller"):
		return true
	case strings.Contains(resourceType, "operation"):
		return true
	}

	return false
}

func (c *missingResourceIDController) sweep(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	allDocs, err := database.ListAll[database.TypedDocument](ctx, 100, c.resourcesDBClient.ListMissingResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("listing documents missing resourceID: %w", err))
	}

	logger.Info("found documents missing resourceID", "count", len(allDocs))

	errs := []error{}
	for _, doc := range allDocs {
		docLogger := logger.WithValues(
			utils.LogValues{}.
				AddCosmosResourceID(doc.CosmosResourceID)...)
		docLogger = docLogger.WithValues(
			"cosmosID", doc.ID,
			"partitionKey", doc.PartitionKey,
			"resourceType", doc.ResourceType,
		)

		docLogger.Info("document missing resourceID",
			"snapshotType", "cosmos-missing-resource-id",
			"content", doc,
		)

		if shouldDelete(doc) {
			docLogger.Info("deleting document missing resourceID")
			if err := deleteByItemID(ctx, c.resourcesDBClient, doc.PartitionKey, doc.ID); err != nil {
				docLogger.Error(err, "unable to delete document missing resourceID")
				errs = append(errs, utils.TrackError(fmt.Errorf("unable to delete %v: %w", doc.ID, err)))
			}
		}
	}

	return errors.Join(errs...)
}

func deleteByItemID(ctx context.Context, resourcesDBClient database.ResourcesDBClient, partitionKey, cosmosID string) error {
	subscriptionResourceID, err := arm.ToSubscriptionResourceID(partitionKey)
	if err != nil {
		return utils.TrackError(fmt.Errorf("parsing partition key %q as subscription ID: %w", partitionKey, err))
	}
	crud, err := resourcesDBClient.UntypedCRUD(*subscriptionResourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	return crud.DeleteByCosmosID(ctx, partitionKey, cosmosID)
}

func (c *missingResourceIDController) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	panic("not implemented")
}

func (c *missingResourceIDController) SyncOnce(ctx context.Context, keyObj any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.sweep(ctx)
	if syncErr != nil {
		logger.Error(syncErr, "sweep for documents missing resourceID had errors")
	}

	return nil // never requeue
}

func (c *missingResourceIDController) queueSweep(ctx context.Context) {
	c.queue.Add("default")
}

func (c *missingResourceIDController) Run(ctx context.Context, threadiness int) {
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

	go wait.JitterUntilWithContext(ctx, c.queueSweep, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *missingResourceIDController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *missingResourceIDController) processNextWorkItem(ctx context.Context) bool {
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
