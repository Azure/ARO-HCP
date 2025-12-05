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

package cleanup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

const (
	pollInterval  = 10 * time.Second
	maxRetries    = 3
	dnsMaxRetries = 3
)

// Resource types excluded from bulk application resource deletion
// These are handled in specific deletion steps due to dependency ordering
var excludedFromBulkDeletion = []string{
	// Networking resources
	"Microsoft.Network/networkSecurityPerimeters",
	"Microsoft.Network/privateEndpoints/privateDnsZoneGroups",
	"Microsoft.Network/privateEndpointConnections",
	"Microsoft.Network/privateEndpoints",
	"Microsoft.Network/privateDnsZones/virtualNetworkLinks",
	"Microsoft.Network/privateLinkServices",
	"Microsoft.Network/privateDnsZones",
	"Microsoft.Network/dnszones",
	"Microsoft.Network/virtualNetworks",
	"Microsoft.Network/networkSecurityGroups",
	// Monitoring resources
	"Microsoft.Insights/dataCollectionRules",
	"Microsoft.Insights/dataCollectionEndpoints",
	// Container instances (excluded to avoid disruption)
	"Microsoft.ContainerInstance/containerGroups",
}

// deletionStep defines a resource type to be deleted with its retry configuration
type deletionStep struct {
	resourceType string
	description  string
	retries      int
}

// resourceGroupDeleter handles ordered deletion of resources in a resource group
type resourceGroupDeleter struct {
	resourceGroupName string
	subscriptionID    string
	credential        azcore.TokenCredential
	logger            logr.Logger
	wait              bool
}

// execute performs ordered resource deletion following the delete.sh logic
func (d *resourceGroupDeleter) execute(ctx context.Context) error {
	// Create resources client
	resourcesClient, err := armresources.NewClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resources client: %w", err)
	}

	// Check if resource group exists
	rgClient, err := armresources.NewResourceGroupsClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource groups client: %w", err)
	}

	_, err = rgClient.Get(ctx, d.resourceGroupName, nil)
	if err != nil {
		return nil
	}

	// Define deletion steps in dependency order
	deletionSteps := []deletionStep{
		// Step 1: Network Security Perimeters (no dependencies)
		{resourceType: "Microsoft.Network/networkSecurityPerimeters", description: "network security perimeters", retries: 1},

		// Step 2: Private networking components (in dependency order)
		{resourceType: "Microsoft.Network/privateEndpoints/privateDnsZoneGroups", description: "private DNS zone groups", retries: 1},
		{resourceType: "Microsoft.Network/privateEndpointConnections", description: "private endpoint connections", retries: 1},
		{resourceType: "Microsoft.Network/privateEndpoints", description: "private endpoints", retries: 1},
		{resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks", description: "private DNS zone virtual network links", retries: 1},
		{resourceType: "Microsoft.Network/privateLinkServices", description: "private link services", retries: 1},
		{resourceType: "Microsoft.Network/privateDnsZones", description: "private DNS zones", retries: dnsMaxRetries},

		// Step 3: Public DNS zones
		{resourceType: "Microsoft.Network/dnszones", description: "public DNS zones", retries: 1},
	}

	// Execute initial deletion steps (networking dependencies)
	for _, step := range deletionSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 4: Delete application and infrastructure resources (VMs, storage, databases, etc.)
	// This happens after DNS/networking dependencies but before monitoring and core networking
	if err := d.deleteNonNetworkingResources(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete application resources: %w", err)
	}

	// Step 5: Delete monitoring resources (after applications since they may monitor them)
	monitoringSteps := []deletionStep{
		{resourceType: "Microsoft.Insights/dataCollectionRules", description: "data collection rules", retries: maxRetries},
		{resourceType: "Microsoft.Insights/dataCollectionEndpoints", description: "data collection endpoints", retries: maxRetries},
	}
	for _, step := range monitoringSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 6: Delete core networking (VNETs, NSGs) last since applications depend on them
	networkingSteps := []deletionStep{
		{resourceType: "Microsoft.Network/virtualNetworks", description: "virtual networks", retries: 1},
		{resourceType: "Microsoft.Network/networkSecurityGroups", description: "network security groups", retries: 1},
	}
	for _, step := range networkingSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 7: Delete the resource group itself
	poller, err := rgClient.BeginDelete(ctx, d.resourceGroupName, nil)
	if err != nil {
		return fmt.Errorf("failed to begin resource group deletion: %w", err)
	}

	if d.wait {
		_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: pollInterval,
		})
		if err != nil {
			return fmt.Errorf("failed to delete resource group: %w", err)
		}
	}

	// Step 8: Purge soft-deleted Key Vaults (only if waiting for completion)
	if d.wait {
		if err := d.purgeSoftDeletedKeyVaults(ctx); err != nil {
			// Log but don't fail - purging is best effort
			d.logger.Error(err, "Failed to purge soft-deleted Key Vaults")
		}
	}

	// Final summary with statistics (only if waiting)
	if d.wait {
		if err := d.logFinalSummary(ctx, resourcesClient); err != nil {
			d.logger.Error(err, "Failed to generate final summary")
		}
	}

	return nil
}

// deleteResourcesByType deletes all resources of a given type in parallel
func (d *resourceGroupDeleter) deleteResourcesByType(ctx context.Context, client *armresources.Client, resourceType, description string, retries int) error {
	// List resources of this type
	filter := fmt.Sprintf("resourceType eq '%s'", resourceType)
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, &armresources.ClientListByResourceGroupOptions{
		Filter: &filter,
	})

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources: %w", err)
		}
		resources = append(resources, page.Value...)
	}

	if len(resources) == 0 {
		return nil
	}

	d.logger.Info("Deleting resources", "type", description, "count", len(resources))

	// Delete all resources in parallel
	group, groupCtx := errgroup.WithContext(ctx)

	for _, resource := range resources {
		if resource.ID == nil || resource.Name == nil {
			continue
		}

		resourceName := *resource.Name
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(groupCtx, resourceID) {
			d.logger.Info("Skipping locked resource", "name", resourceName, "type", resourceType)
			continue
		}

		// Launch deletion in parallel
		group.Go(func() error {
			if err := d.deleteResourceWithRetries(groupCtx, client, resourceID, resourceName, resourceType, retries); err != nil {
				d.logger.Error(err, "Failed to delete resource", "name", resourceName, "type", resourceType)
				// Don't return error - continue with other resources
				return nil
			}
			return nil
		})
	}

	// Wait for all deletions to complete
	if err := group.Wait(); err != nil {
		return err
	}

	return nil
}

// deleteNonNetworkingResources deletes all application and infrastructure resources
// that aren't explicitly handled in the ordered deletion steps
func (d *resourceGroupDeleter) deleteNonNetworkingResources(ctx context.Context, client *armresources.Client) error {
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources: %w", err)
		}
		for _, resource := range page.Value {
			if resource.ID != nil && resource.Type != nil && resource.Name != nil {
				// Skip resources that are handled in explicit deletion steps
				excluded := false
				for _, excludedType := range excludedFromBulkDeletion {
					if strings.EqualFold(*resource.Type, excludedType) {
						excluded = true
						break
					}
				}
				if !excluded {
					resources = append(resources, resource)
				}
			}
		}
	}

	if len(resources) == 0 {
		return nil
	}

	d.logger.Info("Deleting application resources", "count", len(resources))

	// Delete all resources in parallel
	group, groupCtx := errgroup.WithContext(ctx)

	for _, resource := range resources {
		resourceName := *resource.Name
		resourceType := *resource.Type
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(groupCtx, resourceID) {
			d.logger.Info("Skipping locked resource", "name", resourceName, "type", resourceType)
			continue
		}

		// Launch deletion in parallel
		group.Go(func() error {
			if err := d.deleteResourceWithRetries(groupCtx, client, resourceID, resourceName, resourceType, 1); err != nil {
				d.logger.Error(err, "Failed to delete resource", "name", resourceName, "type", resourceType)
				// Don't return error - continue with other resources
				return nil
			}
			return nil
		})
	}

	// Wait for all deletions to complete
	if err := group.Wait(); err != nil {
		return err
	}

	return nil
}

// deleteResourceWithRetries attempts to delete a resource with retries
func (d *resourceGroupDeleter) deleteResourceWithRetries(ctx context.Context, client *armresources.Client, resourceID, resourceName, resourceType string, maxRetries int) error {
	var lastErr error

	// Extract API version from resource type
	// Azure SDK requires the API version to delete resources
	apiVersion := d.getAPIVersionForResourceType(resourceType)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(10 * time.Second)
		}

		// Begin delete with API version
		poller, err := client.BeginDeleteByID(ctx, resourceID, apiVersion, nil)
		if err != nil {
			lastErr = err
			continue
		}

		if d.wait {
			// Wait for completion
			_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
				Frequency: pollInterval,
			})
			if err != nil {
				lastErr = err
				if attempt < maxRetries {
					continue
				}
			} else {
				return nil
			}
		} else {
			// Don't wait - just start the deletion
			return nil
		}
	}

	return fmt.Errorf("failed to delete resource after %d attempts: %w", maxRetries, lastErr)
}

// getAPIVersionForResourceType returns the API version for a given resource type
// This uses commonly stable API versions for different resource providers
func (d *resourceGroupDeleter) getAPIVersionForResourceType(resourceType string) string {
	// Map of specific resource types and provider namespace defaults
	// Format: "Microsoft.Provider/ResourceType" for specific resources
	//         "Microsoft.Provider" for provider-wide defaults
	// Updated with latest stable versions as of 2025-11-18
	apiVersions := map[string]string{
		// Specific resource types that need non-default versions
		"Microsoft.Network/privateDnsZones":                     "2024-06-01",
		"Microsoft.Network/dnszones":                            "2018-05-01", // Older but stable
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks": "2020-06-01",

		// Provider namespace defaults (catch-all for each provider)
		"Microsoft.Network":             "2025-05-01",
		"Microsoft.Compute":             "2025-04-01",
		"Microsoft.Storage":             "2025-06-01",
		"Microsoft.Insights":            "2024-03-11",
		"Microsoft.Monitor":             "2023-04-03",
		"Microsoft.AlertsManagement":    "2023-03-01",
		"Microsoft.OperationalInsights": "2023-09-01",
		"Microsoft.ManagedIdentity":     "2023-01-31",
		"Microsoft.DocumentDB":          "2024-05-15",
		"Microsoft.Kusto":               "2023-08-15",
		"Microsoft.EventGrid":           "2024-06-01-preview",
		"Microsoft.ContainerService":    "2025-10-01",
		"Microsoft.KeyVault":            "2025-05-01",
		"Microsoft.Authorization":       "2022-04-01",
	}

	// Try exact resource type match first
	if version, ok := apiVersions[resourceType]; ok {
		return version
	}

	// Try provider namespace match (e.g., "Microsoft.Network" from "Microsoft.Network/loadBalancers")
	if idx := strings.Index(resourceType, "/"); idx > 0 {
		providerNamespace := resourceType[:idx]
		if version, ok := apiVersions[providerNamespace]; ok {
			return version
		}
	}

	// Global fallback for unknown providers
	return "2023-04-01"
}

// hasLocks checks if a resource has management locks
func (d *resourceGroupDeleter) hasLocks(ctx context.Context, resourceID string) bool {
	// Create management locks client
	locksClient, err := armlocks.NewManagementLocksClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return false
	}

	// Parse resource ID using Azure SDK utility
	parsedID, err := azcorearm.ParseResourceID(resourceID)
	if err != nil {
		// Invalid resource ID format
		return false
	}

	// Build parent resource path for child resources
	parentResourcePath := ""
	if parsedID.Parent != nil {
		parentResourcePath = parsedID.Parent.String()
	}

	// List locks at resource level
	pager := locksClient.NewListAtResourceLevelPager(
		parsedID.ResourceGroupName,
		parsedID.ResourceType.Namespace,
		parentResourcePath,
		parsedID.ResourceType.Type,
		parsedID.Name,
		nil,
	)

	// If we find any locks, the resource is locked
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If we can't list locks, assume no locks
			return false
		}
		if len(page.Value) > 0 {
			return true
		}
	}

	return false
}

// purgeSoftDeletedKeyVaults purges soft-deleted Key Vaults in parallel
func (d *resourceGroupDeleter) purgeSoftDeletedKeyVaults(ctx context.Context) error {
	// Create Key Vault vaults client
	vaultsClient, err := armkeyvault.NewVaultsClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create vaults client: %w", err)
	}

	// List all deleted vaults in the subscription
	pager := vaultsClient.NewListDeletedPager(nil)

	var deletedVaults []*armkeyvault.DeletedVault
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list deleted vaults: %w", err)
		}
		deletedVaults = append(deletedVaults, page.Value...)
	}

	if len(deletedVaults) == 0 {
		return nil
	}

	d.logger.Info("Purging soft-deleted Key Vaults", "count", len(deletedVaults))

	// Purge all vaults in parallel
	group, groupCtx := errgroup.WithContext(ctx)

	for _, vault := range deletedVaults {
		if vault.Name == nil || vault.Properties == nil || vault.Properties.Location == nil {
			continue
		}

		vaultName := *vault.Name
		location := *vault.Properties.Location

		// Check if this vault belonged to our resource group
		// by checking if the vault ID contains our resource group name
		if vault.Properties.VaultID != nil {
			vaultID := *vault.Properties.VaultID
			if !strings.Contains(vaultID, fmt.Sprintf("/resourceGroups/%s/", d.resourceGroupName)) {
				// This vault is from a different resource group, skip it
				continue
			}
		}

		// Launch purge in parallel
		group.Go(func() error {
			// Purge with retries
			var lastErr error
			for attempt := 1; attempt <= maxRetries; attempt++ {
				if attempt > 1 {
					time.Sleep(10 * time.Second)
				}

				poller, err := vaultsClient.BeginPurgeDeleted(groupCtx, vaultName, location, nil)
				if err != nil {
					// Check if it's a 404 - vault already purged
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						return nil
					}
					lastErr = err
					continue
				}

				// Wait for purge to complete
				_, err = poller.PollUntilDone(groupCtx, &runtime.PollUntilDoneOptions{
					Frequency: pollInterval,
				})
				if err != nil {
					lastErr = err
					if attempt < maxRetries {
						continue
					}
				} else {
					return nil
				}
			}

			if lastErr != nil {
				d.logger.Error(lastErr, "Failed to purge Key Vault", "name", vaultName)
				// Don't return error - continue with other vaults
				return nil
			}
			return nil
		})
	}

	// Wait for all purges to complete
	if err := group.Wait(); err != nil {
		return err
	}

	return nil
}

// logFinalSummary logs final statistics about the cleanup operation
func (d *resourceGroupDeleter) logFinalSummary(ctx context.Context, client *armresources.Client) error {
	// Cleanup completed successfully - resource group and all resources deleted
	d.logger.Info("✓ Cleanup completed successfully",
		"resourceGroup", d.resourceGroupName,
		"status", "Resource group and all resources deleted")
	return nil
}
