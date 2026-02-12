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

package mismatchcontrollers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedCosmosResources struct {
	name string

	subscriptionLister listers.SubscriptionLister
	cosmosClient       database.DBClient

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewDeleteOrphanedCosmosResourcesController periodically looks for cosmos objs that don't have an owning cluster and deletes them.
func NewDeleteOrphanedCosmosResourcesController(cosmosClient database.DBClient, subscriptionLister listers.SubscriptionLister) controllerutils.Controller {
	c := &deleteOrphanedCosmosResources{
		name:               "DeleteOrphanedCosmosResources",
		subscriptionLister: subscriptionLister,
		cosmosClient:       cosmosClient,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "DeleteOrphanedCosmosResources",
			},
		),
	}

	return c
}

func (c *deleteOrphanedCosmosResources) synchronizeSubscription(ctx context.Context, subscription string) error {
	logger := utils.LoggerFromContext(ctx)

	subscriptionResourceID, err := arm.ToSubscriptionResourceID(subscription)
	if err != nil {
		return utils.TrackError(err)
	}
	untypedSubscriptionCRUD, err := c.cosmosClient.UntypedCRUD(*subscriptionResourceID)
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
	// while the number of items is large, but we can paginate through
	allSubscriptionResourceIDs := map[string]*azcorearm.ResourceID{}
	for _, subscriptionResource := range subscriptionResourceIterator.Items(ctx) {
		allSubscriptionResourceIDs[strings.ToLower(subscriptionResource.ResourceID.String())] = subscriptionResource.ResourceID
	}
	if err := subscriptionResourceIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	// longer strings are first, so we're guaranteed to see children before parents when we iterate
	resourceIDStrings := sets.KeySet(allSubscriptionResourceIDs).UnsortedList()
	slices.SortFunc(resourceIDStrings, func(a, b string) int {
		if len(a) > len(b) {
			return -1
		}
		if len(b) > len(a) {
			return 1
		}
		return strings.Compare(a, b)
	})

	// at this point we have every resourceID under the subscription that is under a resourcegroup
	resourceGroupPrefix := subscriptionResourceID.String() + "/resourcegroups/"
	for _, currResourceIDString := range resourceIDStrings {
		currResourceID := allSubscriptionResourceIDs[currResourceIDString]
		switch {
		case strings.EqualFold(currResourceID.ResourceType.String(), api.ClusterResourceType.String()):
			// clusters have an owning cluster by definition (themselves)
			continue
		case !strings.HasPrefix(strings.ToLower(currResourceIDString), strings.ToLower(resourceGroupPrefix)):
			// skip anything outside a resourcegroup (operations for instance).  These have TTLs and logically need to live past clusters.
			// For instance, a DNSReservation must exist for a week after the cluster using it is gone to avoid unexpected reuse.
			continue
		case !strings.EqualFold(currResourceID.ResourceType.Namespace, api.ProviderNamespace):
			// any resources outside our namespace we shouldn't delete. Subscriptions exist outside our namespace for instance.
			continue
		}

		localLogger := logger.WithValues(
			utils.LogValues{}.
				AddCosmosResourceID(currResourceIDString).
				AddLogValuesForResourceID(currResourceID)...)
		ctxWithLocalLogger := utils.ContextWithLogger(ctx, localLogger) // setting so that other calls down the chain will show correctly in kusto for the delete

		if currResourceID.Parent == nil {
			// this is an unexpected state, so we'll log it and hope it is rare.
			localLogger.Error(nil, "cosmos resource has no parent", "cosmosResourceID", currResourceIDString)
			continue
		}
		_, parentExists := allSubscriptionResourceIDs[strings.ToLower(currResourceID.Parent.String())]
		if !parentExists {
			localLogger.Info("deleting orphaned cosmos resource")
			if err := untypedSubscriptionCRUD.Delete(ctxWithLocalLogger, currResourceID); err != nil {
				localLogger.Error(err, "unable to delete orphaned cosmos resource") // logged here so we a log line with a filterable context.
				errs = append(errs, utils.TrackError(fmt.Errorf("unable to delete %v in %v: %w", currResourceIDString, currResourceID.Parent.String(), err)))
			}
		}
	}

	return errors.Join(errs...)
}

func (c *deleteOrphanedCosmosResources) SyncOnce(ctx context.Context, subscription any) error {
	logger := utils.LoggerFromContext(ctx)

	syncErr := c.synchronizeSubscription(ctx, subscription.(string)) // we'll handle this is a moment.
	if syncErr != nil {
		logger.Error(syncErr, "unable to synchronize all clusters")
	}

	return utils.TrackError(syncErr)
}

func (c *deleteOrphanedCosmosResources) queueAllSubscriptions(ctx context.Context) {
	logger := utils.LoggerFromContext(ctx)

	allSubscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		logger.Error(err, "unable to list subscriptions")
	}
	for _, subscription := range allSubscriptions {
		c.queue.Add(subscription.ResourceID.SubscriptionID)
	}
}

func (c *deleteOrphanedCosmosResources) Run(ctx context.Context, threadiness int) {
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

	go wait.JitterUntilWithContext(ctx, c.queueAllSubscriptions, 60*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *deleteOrphanedCosmosResources) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *deleteOrphanedCosmosResources) processNextWorkItem(ctx context.Context) bool {
	ref, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(ref)

	logger := utils.LoggerFromContext(ctx)
	logger = logger.WithValues(utils.LogValues{}.AddSubscriptionID(ref)...)
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
