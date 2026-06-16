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
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// resourceGroupListPageSize is the number of resource groups to fetch per page
// when listing resource groups in a subscription
const resourceGroupListPageSize int32 = 100

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

// listManagedResourceGroupsForSubscription lists all HCP-managed resource groups in the controller's location
// for a single subscription and returns them as a map where:
// - key: resource group name
// - value: managedBy resource ID
func (c *cleanOrphanedClusterManagedResourceGroup) listManagedResourceGroupsForSubscription(ctx context.Context, rgClient azureclient.ResourceGroupsClient) (map[string]string, error) {
	managedResourceGroups := make(map[string]string)

	resourceGroupsPager := rgClient.NewListPager(&armresources.ResourceGroupsClientListOptions{
		Top: ptr.To(resourceGroupListPageSize),
	})
	for resourceGroupsPager.More() {
		resourceGroupPage, err := resourceGroupsPager.NextPage(ctx)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		for _, rg := range resourceGroupPage.Value {
			if rg.ManagedBy == nil {
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

			managedResourceGroups[*rg.Name] = *rg.ManagedBy
		}
	}

	return managedResourceGroups, nil
}

// deleteOrphanedManagedResourceGroup attempts to delete an orphaned managed resource group.
// It first checks the current state and only initiates deletion if the resource group exists
// and is not already being deleted.
func (c *cleanOrphanedClusterManagedResourceGroup) deleteOrphanedManagedResourceGroup(ctx context.Context, rgClient azureclient.ResourceGroupsClient, subscriptionID, resourceGroupName, managedBy string) error {
	logger := utils.LoggerFromContext(ctx)

	rg, err := rgClient.Get(ctx, resourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			// Resource group already deleted
			logger.Info("Orphaned cluster managed resource group already deleted",
				"subscriptionID", subscriptionID,
				"resourceGroup", resourceGroupName)
			return nil
		}
		logger.Error(err, "Failed to get resource group state",
			"subscriptionID", subscriptionID,
			"resourceGroup", resourceGroupName)
		return err
	}

	if rg.Properties != nil && rg.Properties.ProvisioningState != nil {
		provisioningState := *rg.Properties.ProvisioningState
		if provisioningState == "Deleting" {
			// Already being deleted, just log and return
			logger.Info("Orphaned cluster managed resource group deletion already in progress",
				"subscriptionID", subscriptionID,
				"resourceGroup", resourceGroupName,
				"provisioningState", provisioningState)
			return nil
		}
	}

	logger.Info("Initiating deletion of orphaned cluster managed resource group",
		"subscriptionID", subscriptionID,
		"resourceGroup", resourceGroupName,
		"managedBy", managedBy)

	_, err = rgClient.BeginDelete(ctx, resourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			// Resource group was deleted between Get and BeginDelete, this is fine
			logger.Info("Orphaned cluster managed resource group deleted before deletion could be initiated",
				"subscriptionID", subscriptionID,
				"resourceGroup", resourceGroupName)
			return nil
		}

		logger.Error(err, "Failed to initiate deletion of orphaned cluster managed resource group",
			"subscriptionID", subscriptionID,
			"resourceGroup", resourceGroupName,
			"managedBy", managedBy)
		return err
	}

	logger.Info("Successfully initiated deletion of orphaned cluster managed resource group",
		"subscriptionID", subscriptionID,
		"resourceGroup", resourceGroupName,
		"managedBy", managedBy)
	return nil
}

// listClusterResourceIDsForSubscription lists all HCP cluster resource IDs for a single subscription
// and returns them as a set (map with empty struct values) with lowercase keys.
func (c *cleanOrphanedClusterManagedResourceGroup) listClusterResourceIDsForSubscription(ctx context.Context, subscription *arm.Subscription) (map[string]struct{}, error) {
	clusterResourceIDs := make(map[string]struct{})
	subscriptionID := subscription.ResourceID.SubscriptionID

	allHCPClusters, err := c.resourcesDBClient.HCPClusters(subscriptionID, "").List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	if err := allHCPClusters.GetError(); err != nil {
		return nil, utils.TrackError(err)
	}

	for _, cluster := range allHCPClusters.Items(ctx) {
		clusterResourceIDs[strings.ToLower(cluster.ID.String())] = struct{}{}
	}

	return clusterResourceIDs, nil
}

// SyncOnce implements the main sync logic for the controller.
func (c *cleanOrphanedClusterManagedResourceGroup) SyncOnce(ctx context.Context, _ any) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing orphaned cluster managed resource groups")

	subscriptions, err := c.subscriptionLister.List(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Retrieved subscriptions", "count", len(subscriptions))

	// Process each subscription to identify orphaned managed resource groups
	// Collect errors but continue processing other subscriptions
	var errs []error
	for _, subscription := range subscriptions {
		subscriptionID := subscription.ResourceID.SubscriptionID
		tenantID := *subscription.Properties.TenantId

		rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, subscriptionID)
		if err != nil {
			logger.Error(err, "Failed to create resource groups client",
				"subscriptionID", subscriptionID)
			errs = append(errs, err)
			continue
		}

		managedResourceGroups, err := c.listManagedResourceGroupsForSubscription(ctx, rgClient)
		if err != nil {
			logger.Error(err, "Failed to list managed resource groups for subscription",
				"subscriptionID", subscriptionID)
			errs = append(errs, err)
			continue
		}

		clusterResourceIDs, err := c.listClusterResourceIDsForSubscription(ctx, subscription)
		if err != nil {
			logger.Error(err, "Failed to list cluster resource IDs for subscription",
				"subscriptionID", subscriptionID)
			errs = append(errs, err)
			continue
		}

		// Identify and clean up orphaned managed resource groups for this subscription
		for resourceGroupName, managedBy := range managedResourceGroups {
			managedByResourceID := strings.ToLower(managedBy)

			if _, exists := clusterResourceIDs[managedByResourceID]; exists {
				// Cluster exists, this is not an orphaned resource group
				continue
			}

			err = c.deleteOrphanedManagedResourceGroup(ctx, rgClient, subscriptionID, resourceGroupName, managedBy)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	logger.Info("End of orphaned cluster managed resource groups sync")

	if len(errs) > 0 {
		return utils.TrackError(errors.Join(errs...))
	}

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
