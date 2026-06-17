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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// resourceGroupListPageSize is the number of resource groups to fetch per page
// when listing resource groups in a subscription
const resourceGroupListPageSize int32 = 100

var (
	orphanedMRGsFound = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "aro_hcp_orphaned_managed_resource_groups_found_total",
			Help: "Total number of orphaned cluster managed resource groups found",
		},
		[]string{"location"},
	)

	orphanedMRGsDeletionFailed = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "aro_hcp_orphaned_managed_resource_groups_deletion_failed_total",
			Help: "Total number of orphaned cluster managed resource groups where deletion failed",
		},
		[]string{"location"},
	)
)

type cleanOrphanedClusterManagedResourceGroup struct {
	location              string
	cooldownChecker       controllerutil.CooldownChecker
	resourcesDBClient     database.ResourcesDBClient
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

// NewCleanOrphanedClusterManagedResourceGroupController periodically looks for managed resource groups
// that are not referenced by any HCPOpenShiftCluster in the database and cleans them up.
func NewCleanOrphanedClusterManagedResourceGroupController(
	location string,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &cleanOrphanedClusterManagedResourceGroup{
		location:              location,
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:     resourcesDBClient,
		azureFPAClientBuilder: azureFPAClientBuilder,
	}

	return controllerutils.NewSubscriptionWatchingController(
		"CleanOrphanedClusterManagedResourceGroup",
		backendInformers,
		10*time.Minute,
		syncer,
	)
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
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
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
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
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
func (c *cleanOrphanedClusterManagedResourceGroup) listClusterResourceIDsForSubscription(ctx context.Context, subscriptionID string) (map[string]struct{}, error) {
	clusterResourceIDs := make(map[string]struct{})

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

// SyncOnce implements the main sync logic for the controller for a single subscription.
func (c *cleanOrphanedClusterManagedResourceGroup) SyncOnce(ctx context.Context, key controllerutils.SubscriptionKey) error {
	logger := utils.LoggerFromContext(ctx)
	subscriptionID := key.SubscriptionID

	logger.Info("Syncing orphaned cluster managed resource groups for subscription",
		"subscriptionID", subscriptionID)

	// Load the subscription to get tenantID
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, subscriptionID)
	if err != nil {
		logger.Error(err, "Failed to get subscription from database",
			"subscriptionID", subscriptionID)
		return utils.TrackError(err)
	}

	tenantID := *subscription.Properties.TenantId

	rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, subscriptionID)
	if err != nil {
		logger.Error(err, "Failed to create resource groups client",
			"subscriptionID", subscriptionID)
		return utils.TrackError(err)
	}

	managedResourceGroups, err := c.listManagedResourceGroupsForSubscription(ctx, rgClient)
	if err != nil {
		logger.Error(err, "Failed to list managed resource groups for subscription",
			"subscriptionID", subscriptionID)
		return utils.TrackError(err)
	}

	clusterResourceIDs, err := c.listClusterResourceIDsForSubscription(ctx, subscriptionID)
	if err != nil {
		logger.Error(err, "Failed to list cluster resource IDs for subscription",
			"subscriptionID", subscriptionID)
		return utils.TrackError(err)
	}

	// Identify and clean up orphaned managed resource groups for this subscription
	var errs []error
	for resourceGroupName, managedBy := range managedResourceGroups {
		managedByResourceID := strings.ToLower(managedBy)

		if _, exists := clusterResourceIDs[managedByResourceID]; exists {
			// Cluster exists, this is not an orphaned resource group
			continue
		}

		// Found an orphaned managed resource group
		orphanedMRGsFound.WithLabelValues(c.location).Inc()

		err = c.deleteOrphanedManagedResourceGroup(ctx, rgClient, subscriptionID, resourceGroupName, managedBy)
		if err != nil {
			errs = append(errs, err)
		}
	}

	logger.Info("Completed sync for subscription",
		"subscriptionID", subscriptionID)

	if len(errs) > 0 {
		return utils.TrackError(errors.Join(errs...))
	}

	return nil
}

func (c *cleanOrphanedClusterManagedResourceGroup) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
