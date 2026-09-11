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

package deletion

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azruntime "github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// orphanedManagedResourceGroupController implements ManagedResourceGroupProcessor.
type orphanedManagedResourceGroupController struct {
	location              string
	resourcesDBClient     corecosmosstorage.ResourcesDBClient
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder
}

// NewOrphanedManagedResourceGroupController creates a controller that processes managed resource groups
// and deletes them if they are orphaned.
func NewOrphanedManagedResourceGroupController(
	location string,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	azureFPAClientBuilder azureclient.FirstPartyApplicationClientBuilder,
) *orphanedManagedResourceGroupController {
	return &orphanedManagedResourceGroupController{
		location:              location,
		resourcesDBClient:     resourcesDBClient,
		azureFPAClientBuilder: azureFPAClientBuilder,
	}
}

// needsWork determines if a managed resource group should be deleted.
// It returns true if the MRG is orphaned (cluster doesn't exist in Cosmos).
func (c *orphanedManagedResourceGroupController) needsWork(ctx context.Context, key ManagedResourceGroupKey) (bool, *coreapi.Subscription, error) {
	logger := utils.LoggerFromContext(ctx)

	// Load subscription from Cosmos - this provides race protection.
	// If subscription doesn't exist in Cosmos yet, we skip processing to avoid
	// deleting MRGs before cluster data has been fully synced.
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, key.SubscriptionID)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Subscription not found in Cosmos, skipping MRG (race protection)")
		return false, nil, nil
	}
	if err != nil {
		return false, nil, utils.TrackError(fmt.Errorf("failed to get subscription from database: %w", err))
	}

	// Parse the managedBy resource ID to extract cluster details
	managedByID, err := azcorearm.ParseResourceID(key.ManagedBy)
	if err != nil {
		logger.Error(err, "Failed to parse managedBy resource ID")
		return false, nil, nil // Invalid managedBy, skip
	}

	// Check if cluster exists in Cosmos
	_, err = c.resourcesDBClient.HCPClusters(
		managedByID.SubscriptionID,
		managedByID.ResourceGroupName,
	).Get(ctx, managedByID.Name)

	if cosmosstorageutils.IsNotFoundError(err) {
		// Cluster not found - this MRG is orphaned
		logger.Info("Cluster not found in Cosmos - MRG is orphaned",
			"clusterResourceID", key.ManagedBy)
		return true, subscription, nil
	}
	if err != nil {
		return false, nil, utils.TrackError(fmt.Errorf("failed to get cluster from database: %w", err))
	}

	// Cluster exists - MRG is not orphaned
	logger.V(1).Info("Cluster exists - MRG is not orphaned")
	return false, subscription, nil
}

// ProcessManagedResourceGroup implements ManagedResourceGroupProcessor.
// It checks if the MRG is orphaned and deletes it if necessary.
func (c *orphanedManagedResourceGroupController) ProcessManagedResourceGroup(ctx context.Context, key ManagedResourceGroupKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Check if we're in read-write mode (default is read-only for safety)
	readOnly := os.Getenv("CLEAN_ORPHANED_MANAGED_RESOURCE_GROUPS_MODE") != "readwrite"

	// Check if this MRG needs cleanup
	shouldCleanup, subscription, err := c.needsWork(ctx, key)
	if err != nil {
		return err
	}
	if !shouldCleanup {
		return nil
	}

	// MRG is orphaned - increment metric
	orphanedMRGsFound.WithLabelValues(c.location).Inc()

	// Get resource group client
	tenantID := *subscription.Properties.TenantId
	rgClient, err := c.azureFPAClientBuilder.ResourceGroupsClient(tenantID, key.SubscriptionID)
	if err != nil {
		logger.Error(err, "Failed to create resource groups client")
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
		return utils.TrackError(err)
	}

	return c.deleteOrphanedManagedResourceGroup(ctx, rgClient, key, readOnly)
}

// deleteOrphanedManagedResourceGroup attempts to delete an orphaned managed resource group.
func (c *orphanedManagedResourceGroupController) deleteOrphanedManagedResourceGroup(
	ctx context.Context,
	rgClient azureclient.ResourceGroupsClient,
	key ManagedResourceGroupKey,
	readOnly bool,
) error {
	logger := utils.LoggerFromContext(ctx)

	// Check current state
	rg, err := rgClient.Get(ctx, key.ResourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			logger.Info("Orphaned cluster managed resource group already deleted")
			return nil
		}
		logger.Error(err, "Failed to get resource group state")
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
		return err
	}

	// Check if already being deleted
	if rg.Properties != nil && rg.Properties.ProvisioningState != nil {
		provisioningState := *rg.Properties.ProvisioningState
		if provisioningState == "Deleting" {
			logger.Info("Orphaned cluster managed resource group deletion already in progress",
				"provisioningState", provisioningState)
			return nil
		}
	}

	if readOnly {
		logger.Info("Would delete orphaned cluster managed resource group (read-only mode, skipping actual deletion)")
		return nil
	}

	logger.Info("Initiating deletion of orphaned cluster managed resource group")

	// Initiate deletion
	poller, err := rgClient.BeginDelete(ctx, key.ResourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			logger.Info("Orphaned cluster managed resource group deleted before deletion could be initiated")
			return nil
		}

		logger.Error(err, "Failed to initiate deletion of orphaned cluster managed resource group")
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
		return err
	}

	logger.Info("Successfully initiated deletion of orphaned cluster managed resource group")

	return c.pollResourceGroupDeletion(ctx, key, poller)
}

// pollResourceGroupDeletion polls a resource group deletion to completion with timeout.
func (c *orphanedManagedResourceGroupController) pollResourceGroupDeletion(
	ctx context.Context,
	key ManagedResourceGroupKey,
	poller *azruntime.Poller[armresources.ResourceGroupsClientDeleteResponse],
) error {
	logger := utils.LoggerFromContext(ctx)
	const deletionPollTimeout = 15 * time.Minute

	pollCtx, cancel := context.WithTimeout(ctx, deletionPollTimeout)
	defer cancel()

	logger.Info("Polling resource group deletion")

	_, err := poller.PollUntilDone(pollCtx, nil)
	if err != nil {
		logger.Error(err, "Resource group deletion failed or timed out")
		orphanedMRGsDeletionFailed.WithLabelValues(c.location).Inc()
		return err
	}

	logger.Info("Resource group deletion completed successfully")
	return nil
}
