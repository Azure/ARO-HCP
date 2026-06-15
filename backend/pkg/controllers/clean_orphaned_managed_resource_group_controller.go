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

package controllers

import (
	"context"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// resourceGroupListPageSize is the number of resource groups to fetch per page
	// when listing resource groups in a subscription
	resourceGroupListPageSize int32 = 100
)

type cleanOrphanedManagedResourceGroup struct {
	name string

	subscriptionLister    listers.SubscriptionLister
	resourcesDBClient     database.ResourcesDBClient
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewCleanOrphanedManagedResourceGroupController periodically looks for managed resource groups
// that are not referenced by any HCPOpenShiftCluster in the database and deletes them.
func NewCleanOrphanedManagedResourceGroupController(
	subscriptionLister listers.SubscriptionLister,
	resourcesDBClient database.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) controllerutils.Controller {
	c := &cleanOrphanedManagedResourceGroup{
		name:                  "CleanOrphanedManagedResourceGroup",
		subscriptionLister:    subscriptionLister,
		resourcesDBClient:     resourcesDBClient,
		azureFPAClientBuilder: azureFPAClientBuilder,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "CleanOrphanedManagedResourceGroup",
			},
		),
	}

	return c
}

// SyncOnce implements the main sync logic for the controller.
func (c *cleanOrphanedManagedResourceGroup) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned managed resource groups")

	// Get all subscriptions
	subscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Retrieved subscriptions", "count", len(subscriptions))

	for _, subscription := range subscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID

		rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(
			*subscription.Properties.TenantId,
			subscriptionID,
		)
		if err != nil {
			return utils.TrackError(err)
		}

		clusterResourceIDs := make(map[string]struct{})
		allHCPClusters, err := c.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
		if err != nil {
			return utils.TrackError(err)
		}

		for _, cluster := range allHCPClusters.Items(ctx) {
			if cluster == nil || cluster.ID == nil {
				continue
			}
			clusterResourceIDs[strings.ToLower(cluster.ID.String())] = struct{}{}
		}

		if err := allHCPClusters.GetError(); err != nil {
			return utils.TrackError(err)
		}

		logger.Info("Found HCP clusters in subscription", "subscriptionID", subscriptionID, "count", len(clusterResourceIDs))

		resourceGroupsPager := rgClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
			Top: ptr.To(resourceGroupListPageSize),
		})
		for resourceGroupsPager.More() {
			resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
			if err != nil {
				return utils.TrackError(err)
			}

			for _, rg := range resourceGroupPage.Value {
				if rg == nil || rg.Name == nil {
					continue
				}

				if rg.ManagedBy == nil {
					continue
				}

				managedByResourceID := strings.ToLower(*rg.ManagedBy)

				if _, exists := clusterResourceIDs[managedByResourceID]; exists {
					// Cluster exists, this is not an orphaned resource group
					continue
				}

				logger.Info("Found orphaned managed resource group",
					"subscriptionID", subscriptionID,
					"resourceGroup", *rg.Name,
					"managedBy", *rg.ManagedBy)

				// TODO: Delete the orphaned managed resource group
			}
		}
	}

	logger.Info("End of orphaned managed resource groups sync")
	return nil
}

func (c *cleanOrphanedManagedResourceGroup) Run(ctx context.Context, threadiness int) {
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

	// We run this periodically enqueuing an arbitrary item named "doWork" to trigger the sync.
	go wait.JitterUntilWithContext(ctx, func(ctx context.Context) { c.queue.Add("doWork") }, 10*time.Minute, 0.1, true)

	logger.Info("Started workers")

	// wait until we're told to stop
	<-ctx.Done()
	logger.Info("Shutting down")
}

func (c *cleanOrphanedManagedResourceGroup) runWorker(ctx context.Context) {
	defer utilruntime.HandleCrash()
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *cleanOrphanedManagedResourceGroup) processNextWorkItem(ctx context.Context) bool {
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
