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

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// resourceGroupListPageSize is the number of resource groups to fetch per page
// when listing resource groups in a subscription
const resourceGroupListPageSize int32 = 100

// managedResourceGroupInfo holds information about a managed resource group
type managedResourceGroupInfo struct {
	subscriptionID string
	tenantID       string
	name           string
	managedBy      string
}

type cleanOrphanedClusterManagedResourceGroup struct {
	name     string
	location string

	subscriptionLister    listers.SubscriptionLister
	resourcesDBClient     database.ResourcesDBClient
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder

	// queue is where incoming work is placed to de-dup and to allow "easy"
	// rate limited requeues on errors
	queue workqueue.TypedRateLimitingInterface[string]
}

// NewCleanOrphanedClusterManagedResourceGroupController periodically looks for managed resource groups
// that are not referenced by any HCPOpenShiftCluster in the database and cleans them up.
func NewCleanOrphanedClusterManagedResourceGroupController(
	location string,
	subscriptionLister listers.SubscriptionLister,
	resourcesDBClient database.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) controllerutils.Controller {
	c := &cleanOrphanedClusterManagedResourceGroup{
		name:                  "CleanOrphanedClusterManagedResourceGroup",
		location:              location,
		subscriptionLister:    subscriptionLister,
		resourcesDBClient:     resourcesDBClient,
		azureFPAClientBuilder: azureFPAClientBuilder,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{
				Name: "CleanOrphanedClusterManagedResourceGroup",
			},
		),
	}

	return c
}

// SyncOnce implements the main sync logic for the controller.
func (c *cleanOrphanedClusterManagedResourceGroup) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned cluster managed resource groups")

	// Get all subscriptions
	subscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Retrieved subscriptions", "count", len(subscriptions))

	// List all managed resource groups
	managedResourceGroups := make(map[string]*managedResourceGroupInfo)
	for _, subscription := range subscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		tenantID := *subscription.Properties.TenantId

		rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, subscriptionID)
		if err != nil {
			return utils.TrackError(err)
		}

		resourceGroupsPager := rgClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
			Top: ptr.To(resourceGroupListPageSize),
		})
		for resourceGroupsPager.More() {
			resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
			if err != nil {
				return utils.TrackError(err)
			}

			for _, rg := range resourceGroupPage.Value {
				if rg == nil || rg.Name == nil || rg.ManagedBy == nil || rg.Location == nil {
					continue
				}

				// Only process resource groups in our location
				if !strings.EqualFold(*rg.Location, c.location) {
					continue
				}

				parsedID, err := azcorearm.ParseResourceID(*rg.ManagedBy)
				if err != nil {
					// Skip resource groups with invalid ManagedBy resource IDs
					continue
				}

				// Only track HCP-managed resource groups
				if !(strings.EqualFold(parsedID.ResourceType.String(), api.ClusterResourceType.String())) {
					continue
				}

				key := subscriptionID + "/" + *rg.Name
				managedResourceGroups[key] = &managedResourceGroupInfo{
					subscriptionID: subscriptionID,
					tenantID:       tenantID,
					name:           *rg.Name,
					managedBy:      *rg.ManagedBy,
				}
			}
		}
	}
	logger.Info("Found cluster managed resource groups", "count", len(managedResourceGroups))

	// clusterResourceIDs is a map of cluster resource IDs (in lowercase) to empty struct for fast lookup
	clusterResourceIDs := make(map[string]struct{})
	for _, subscription := range subscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID

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
	}
	logger.Info("Found HCP clusters", "count", len(clusterResourceIDs))

	// Identify orphaned managed resource groups
	for key, mrg := range managedResourceGroups {
		managedByResourceID := strings.ToLower(mrg.managedBy)

		if _, exists := clusterResourceIDs[managedByResourceID]; exists {
			// Cluster exists, this is not an orphaned resource group
			continue
		}

		logger.Info("Found orphaned cluster managed resource group",
			"key", key,
			"subscriptionID", mrg.subscriptionID,
			"resourceGroup", mrg.name,
			"managedBy", mrg.managedBy)

		// TODO: Clean up the orphaned cluster managed resource group
	}

	logger.Info("End of orphaned cluster managed resource groups sync")
	return nil
}

func (c *cleanOrphanedClusterManagedResourceGroup) Run(ctx context.Context, threadiness int) {
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

func (c *cleanOrphanedClusterManagedResourceGroup) runWorker(ctx context.Context) {
	defer utilruntime.HandleCrash()
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem deals with one item off the queue.  It returns false
// when it's time to quit.
func (c *cleanOrphanedClusterManagedResourceGroup) processNextWorkItem(ctx context.Context) bool {
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
