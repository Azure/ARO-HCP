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
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedOperations struct {
	name string

	subscriptionLister listers.SubscriptionLister
	resourcesDBClient  database.ResourcesDBClient

	queue workqueue.TypedRateLimitingInterface[string]
}

// NewDeleteOrphanedOperationsController periodically scans the resources container under each subscription for
// operation documents that are missing the top-level resourceID. Such documents cannot be addressed by ARM
// resource path, so they are unreachable through the typed Operations CRUD; this controller logs the entire
// document and deletes it by partitionKey + cosmos document ID. We don't know how these arrive in the
// database, but the data dumper has already had to be made nil-safe to survive them (see DumpDataToLogger),
// and unreachable rows accrue Cosmos cost and pollute investigation.
func NewDeleteOrphanedOperationsController(resourcesDBClient database.ResourcesDBClient, subscriptionLister listers.SubscriptionLister) controllerutils.Controller {
	c := &deleteOrphanedOperations{
		name:               "DeleteOrphanedOperations",
		subscriptionLister: subscriptionLister,
		resourcesDBClient:  resourcesDBClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedOperations",
			},
		),
	}

	return c
}

func (c *deleteOrphanedOperations) synchronizeSubscription(ctx context.Context, subscription string) error {
	subscriptionResourceID, err := arm.ToSubscriptionResourceID(subscription)
	if err != nil {
		return utils.TrackError(err)
	}
	untypedSubscriptionCRUD, err := c.resourcesDBClient.UntypedCRUD(*subscriptionResourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	paginatedListOptions := &database.DBClientListResourceDocsOptions{
		PageSizeHint: ptr.To(int32(500)),
	}
	subscriptionResourceIterator, err := untypedSubscriptionCRUD.ListRecursive(ctx, paginatedListOptions)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	for _, doc := range subscriptionResourceIterator.Items(ctx) {
		if err := c.processDoc(ctx, untypedSubscriptionCRUD, doc); err != nil {
			errs = append(errs, err)
		}
	}
	if err := subscriptionResourceIterator.GetError(); err != nil {
		errs = append(errs, utils.TrackError(err))
	}

	return errors.Join(errs...)
}

// processDoc deletes an operation document when either:
//  1. its top-level resourceID is missing — an unreachable row that the typed Operations CRUD
//     cannot address, OR
//  2. its cosmosID is the legacy pipe-delimited form — a pre-migration row. The lenient convert
//     back-fills a derived ResourceID on read so the doc looks fine to callers, but the row
//     itself is stale and cheap to recreate; cleaning it up lets the frontend re-write under
//     the new UUID-cosmosID scheme.
//
// Non-operation documents are out of scope here; cluster/nodepool cleanup lives in
// deleteOrphanedCosmosResources.
func (c *deleteOrphanedOperations) processDoc(ctx context.Context, untypedCRUD database.UntypedResourceCRUD, doc *database.TypedDocument) error {
	if !strings.EqualFold(doc.ResourceType, api.OperationStatusResourceType.String()) {
		return nil
	}

	missingResourceID := doc.ResourceID == nil
	preMigrationCosmosID := strings.HasPrefix(doc.ID, "|")
	if !missingResourceID && !preMigrationCosmosID {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(
		utils.LogValues{}.
			AddSubscriptionID(doc.PartitionKey).
			AddCosmosResourceID(doc.ID).
			AddResourceType(doc.ResourceType)...)
	// Log fields individually rather than passing *doc: logr's reflective formatter calls
	// .String() on every field, which panics when doc.ResourceID is nil — the exact case we
	// hit on missingResourceID.
	logger.Error(nil, "deleting unreachable or pre-migration operation document",
		"cosmosID", doc.ID,
		"partitionKey", doc.PartitionKey,
		"resourceType", doc.ResourceType,
		"etag", doc.CosmosETag,
		"missingResourceID", missingResourceID,
		"preMigrationCosmosID", preMigrationCosmosID,
		"properties", string(doc.Properties),
	)

	if err := untypedCRUD.DeleteByCosmosID(ctx, doc.PartitionKey, doc.ID); err != nil {
		return utils.TrackError(fmt.Errorf("unable to delete orphaned operation %q in partition %q: %w", doc.ID, doc.PartitionKey, err))
	}
	return nil
}

func (c *deleteOrphanedOperations) SyncOnce(ctx context.Context, subscription any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.synchronizeSubscription(ctx, subscription.(string))
	if syncErr != nil {
		logger.Error(syncErr, "unable to synchronize orphaned operations for subscription")
	}

	return utils.TrackError(syncErr)
}

func (c *deleteOrphanedOperations) queueAllSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error(err, "unable to list subscriptions")
	}
	for _, subscription := range allSubscriptions {
		c.queue.Add(subscription.ResourceID.SubscriptionID)
	}
}

func (c *deleteOrphanedOperations) Run(ctx context.Context, threadiness int) {
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

	go wait.JitterUntilWithContext(ctx, c.queueAllSubscriptions, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedOperations) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *deleteOrphanedOperations) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddSubscriptionID(ref)...)
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
