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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type deleteOrphanedCosmosResources struct {
	name string

	subscriptionLister      listers.SubscriptionLister
	managementClusterLister dblisters.ManagementClusterLister
	resourcesDBClient       database.ResourcesDBClient
	kubeApplierDBClients    database.KubeApplierDBClients

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewDeleteOrphanedCosmosResourcesController periodically looks for cosmos objs that don't have an
// owning cluster and deletes them. The sweep covers two storage layers:
//
//   - the resources container, which holds clusters/nodepools and their nested children;
//     orphans here are children whose parent resource has been removed.
//   - every kube-applier container in the configured KubeApplierDBClients (one container per
//     management cluster). *Desire documents nest in resourceID space under a cluster or
//     nodepool; we walk each MC's container and delete any desire whose parent isn't present
//     in this subscription's resource map.
func NewDeleteOrphanedCosmosResourcesController(
	resourcesDBClient database.ResourcesDBClient,
	kubeApplierDBClients database.KubeApplierDBClients,
	subscriptionLister listers.SubscriptionLister,
	managementClusterLister dblisters.ManagementClusterLister,
) controllerutils.Controller {
	c := &deleteOrphanedCosmosResources{
		name:                    "DeleteOrphanedCosmosResources",
		subscriptionLister:      subscriptionLister,
		managementClusterLister: managementClusterLister,
		resourcesDBClient:       resourcesDBClient,
		kubeApplierDBClients:    kubeApplierDBClients,
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
	untypedSubscriptionCRUD, err := c.resourcesDBClient.UntypedCRUD(*subscriptionResourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	// nil options → all-pages iterator. A positive PageSizeHint switches to a
	// single-page iterator with a continuation token we don't follow, which
	// would silently truncate the resource set and let orphan checks miss
	// live parents on later pages.
	subscriptionResourceIterator, err := untypedSubscriptionCRUD.ListRecursive(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	errs := []error{}
	// while the number of items is large, but we can paginate through
	allSubscriptionResourceIDs := map[string]*database.TypedDocument{}
	for _, subscriptionResource := range subscriptionResourceIterator.Items(ctx) {
		if subscriptionResource.ResourceID == nil {
			// n.b. our listers pass all data through a Cosmos -> internal representation mapping, which attempts to ensure
			// that document.resourceID is a comprehensible value - however:
			// - first, if/when we retire that, we don't want this controller to panic and blow up if we encounter a record where
			//   that has not happened, and we need to make sure that errant values are visible through logs
			// - during the migration period when records in the database exist with old pipe-delimited identifiers, or during any future
			//   migrations, we need to be deleting from cosmos with the raw partition and document ID
			localLogger := logger.WithValues(utils.LogValues{}.AddCosmosResourceID(subscriptionResource.CosmosResourceID))
			localLogger.Error(errors.New("cosmos document has no resource ID"), "found invalid document")
			continue
		}
		allSubscriptionResourceIDs[strings.ToLower(subscriptionResource.ResourceID.String())] = subscriptionResource
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
		currResource := allSubscriptionResourceIDs[currResourceIDString]
		switch {
		case strings.EqualFold(currResource.ResourceID.ResourceType.String(), api.ClusterResourceType.String()):
			// clusters have an owning cluster by definition (themselves)
			continue
		case !strings.HasPrefix(strings.ToLower(currResourceIDString), strings.ToLower(resourceGroupPrefix)):
			// skip anything outside a resourcegroup (operations for instance).  These have TTLs and logically need to live past clusters.
			// For instance, a DNSReservation must exist for a week after the cluster using it is gone to avoid unexpected reuse.
			continue
		case !strings.EqualFold(currResource.ResourceID.ResourceType.Namespace, api.ProviderNamespace):
			// any resources outside our namespace we shouldn't delete. Subscriptions exist outside our namespace for instance.
			continue
		}

		localLogger := logger.WithValues(
			utils.LogValues{}.
				AddCosmosResourceID(currResourceIDString).
				AddLogValuesForResourceID(currResource.ResourceID)...)
		ctxWithLocalLogger := utils.ContextWithLogger(ctx, localLogger) // setting so that other calls down the chain will show correctly in kusto for the delete

		if currResource.ResourceID.Parent == nil {
			// this is an unexpected state, so we'll log it and hope it is rare.
			localLogger.Error(nil, "cosmos resource has no parent", "cosmosResourceID", currResourceIDString)
			continue
		}
		_, parentExists := allSubscriptionResourceIDs[strings.ToLower(currResource.ResourceID.Parent.String())]
		if !parentExists {
			localLogger.Info("deleting orphaned cosmos resource by cosmos ID")
			if err := untypedSubscriptionCRUD.DeleteByCosmosID(ctxWithLocalLogger, currResource.PartitionKey, currResource.ID); err != nil {
				localLogger.Error(err, "unable to delete orphaned cosmos resource") // logged here so we emit a log line with a filterable context.
				errs = append(errs, utils.TrackError(fmt.Errorf("unable to delete %v in %v: %w", currResourceIDString, currResource.ResourceID.Parent.String(), err)))
			}
		}
	}

	if err := c.sweepOrphanedDesires(ctx, subscriptionResourceID, allSubscriptionResourceIDs); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// sweepOrphanedDesires walks every configured management cluster's kube-applier container
// for *Desire documents nested under this subscription and deletes any whose parent (the
// cluster or nodepool the *Desire is scoped to) is no longer present in
// allSubscriptionResourceIDs.
//
// In the per-management-cluster container model, the controller cannot use a single client
// to span all *Desires — it iterates the management clusters from the lister and looks up
// each one's KubeApplierDBClient. Each per-MC UntypedCRUD walks one container; the partition
// key is internal to the container, so deletion goes through DeleteByCosmosID using the
// listed row's partitionKey.
func (c *deleteOrphanedCosmosResources) sweepOrphanedDesires(
	ctx context.Context,
	subscriptionResourceID *azcorearm.ResourceID,
	allSubscriptionResourceIDs map[string]*database.TypedDocument,
) error {
	logger := utils.LoggerFromContext(ctx)

	managementClusters, err := c.managementClusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("listing management clusters for kube-applier sweep: %w", err))
	}

	errs := []error{}
	for _, mc := range managementClusters {
		mcResourceID := mc.ResourceID
		if mcResourceID == nil {
			mcResourceID = mc.CosmosMetadata.ResourceID
		}
		if mcResourceID == nil {
			continue
		}
		mcLogger := logger.WithValues("managementCluster", strings.ToLower(mcResourceID.String()))

		client := c.kubeApplierDBClients.For(ctx, mcResourceID)
		if client == nil {
			mcLogger.Error(nil, "no kube-applier client configured for management cluster; skipping")
			continue
		}

		desireCRUD, err := client.UntypedCRUD(*subscriptionResourceID)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}
		desireIterator, err := desireCRUD.ListRecursive(ctx, nil)
		if err != nil {
			errs = append(errs, utils.TrackError(err))
			continue
		}

		for _, desire := range desireIterator.Items(ctx) {
			if desire.ResourceID == nil {
				localLogger := mcLogger.WithValues(utils.LogValues{}.AddCosmosResourceID(desire.CosmosResourceID))
				localLogger.Error(errors.New("kube-applier document has no resource ID"), "deleting invalid document by cosmos ID")
				ctxWithLocalLogger := utils.ContextWithLogger(ctx, localLogger)
				if err := desireCRUD.DeleteByCosmosID(ctxWithLocalLogger, desire.PartitionKey, desire.ID); err != nil {
					localLogger.Error(err, "unable to delete invalid kube-applier desire")
					errs = append(errs, utils.TrackError(fmt.Errorf("unable to delete invalid desire %v: %w", desire.CosmosResourceID, err)))
				}
				continue
			}
			if desire.ResourceID.Parent == nil {
				localLogger := mcLogger.WithValues(utils.LogValues{}.AddCosmosResourceID(desire.ResourceID.String()))
				localLogger.Error(nil, "kube-applier desire has no parent in its resource ID")
				continue
			}

			desireResourceIDString := strings.ToLower(desire.ResourceID.String())
			localLogger := mcLogger.WithValues(
				utils.LogValues{}.
					AddCosmosResourceID(desireResourceIDString).
					AddLogValuesForResourceID(desire.ResourceID)...)
			ctxWithLocalLogger := utils.ContextWithLogger(ctx, localLogger)

			if _, parentExists := allSubscriptionResourceIDs[strings.ToLower(desire.ResourceID.Parent.String())]; parentExists {
				continue
			}

			localLogger.Info("deleting orphaned kube-applier desire by cosmos ID",
				"partitionKey", desire.PartitionKey)
			if err := desireCRUD.DeleteByCosmosID(ctxWithLocalLogger, desire.PartitionKey, desire.ID); err != nil {
				localLogger.Error(err, "unable to delete orphaned kube-applier desire")
				errs = append(errs, utils.TrackError(fmt.Errorf("unable to delete desire %v under missing parent %v: %w", desireResourceIDString, desire.ResourceID.Parent.String(), err)))
			}
		}
		if err := desireIterator.GetError(); err != nil {
			errs = append(errs, utils.TrackError(err))
		}
	}

	return errors.Join(errs...)
}

func (c *deleteOrphanedCosmosResources) QueueForInformers(resyncDuration time.Duration, notifiers ...controllerutils.Notifier) error {
	panic("not implemented")
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
