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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

const (
	pollInterval  = 10 * time.Second
	maxRetries    = 3
	dnsMaxRetries = 3
)

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
	dryRun            bool
}

// deletionStats tracks the outcome of resource deletion operations
type deletionStats struct {
	deleted int
	skipped int
	failed  int
}

// execute performs ordered resource deletion following the delete.sh logic
func (d *resourceGroupDeleter) execute(ctx context.Context) error {
	// Create resources client
	resourcesClient, err := armresources.NewClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resources client: %w", err)
	}

	if d.dryRun {
		d.logger.Info("Starting ordered resource deletion (DRY-RUN MODE)")
	} else {
		d.logger.Info("Starting ordered resource deletion")
	}

	// Check if resource group exists
	rgClient, err := armresources.NewResourceGroupsClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource groups client: %w", err)
	}

	_, err = rgClient.Get(ctx, d.resourceGroupName, nil)
	if err != nil {
		d.logger.Info("Resource group does not exist, skipping deletion")
		return nil
	}

	// In dry-run mode, list all resources first
	if d.dryRun {
		if err := d.listAllResources(ctx, resourcesClient); err != nil {
			d.logger.Error(err, "Failed to list resources")
		}
	}

	// Step 1: Delete NSP (Network Security Perimeters)
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/networkSecurityPerimeters", "NSPs", 1); err != nil {
		return fmt.Errorf("failed to delete NSPs: %w", err)
	}

	// Step 2: Delete private networking components in order
	privateNetworkingSteps := []deletionStep{
		{resourceType: "Microsoft.Network/privateEndpoints/privateDnsZoneGroups", description: "private DNS zone groups", retries: 1},
		{resourceType: "Microsoft.Network/privateEndpointConnections", description: "private endpoint connections", retries: 1},
		{resourceType: "Microsoft.Network/privateEndpoints", description: "private endpoints", retries: 1},
		{resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks", description: "private DNS zone virtual network links", retries: 1},
		{resourceType: "Microsoft.Network/privateLinkServices", description: "private link services", retries: 1},
		{resourceType: "Microsoft.Network/privateDnsZones", description: "private DNS zones", retries: dnsMaxRetries},
	}

	for _, step := range privateNetworkingSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 3: Delete public DNS zones with delegation cleanup
	if err := d.deletePublicDNSZones(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete public DNS zones: %w", err)
	}

	// Step 4: Delete application and infrastructure resources (excluding VNETs/NSGs/DCRs/DCEs/Container Instances)
	if err := d.deleteNonNetworkingResources(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete application resources: %w", err)
	}

	// Step 5: Delete monitoring resources
	monitoringSteps := []deletionStep{
		{resourceType: "Microsoft.Insights/dataCollectionRules", description: "data collection rules", retries: maxRetries},
		{resourceType: "Microsoft.Insights/dataCollectionEndpoints", description: "data collection endpoints", retries: maxRetries},
	}

	for _, step := range monitoringSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 6: Delete VNETs and NSGs
	coreNetworkingSteps := []deletionStep{
		{resourceType: "Microsoft.Network/virtualNetworks", description: "virtual networks", retries: 1},
		{resourceType: "Microsoft.Network/networkSecurityGroups", description: "network security groups", retries: 1},
	}

	for _, step := range coreNetworkingSteps {
		if err := d.deleteResourcesByType(ctx, resourcesClient, step.resourceType, step.description, step.retries); err != nil {
			return fmt.Errorf("failed to delete %s: %w", step.description, err)
		}
	}

	// Step 7: Delete the resource group itself
	if d.dryRun {
		d.logger.Info("[DRY RUN] Would delete resource group")
	} else {
		d.logger.Info("Step: Deleting resource group")
		poller, err := rgClient.BeginDelete(ctx, d.resourceGroupName, nil)
		if err != nil {
			return fmt.Errorf("failed to begin resource group deletion: %w", err)
		}

		_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: pollInterval,
		})
		if err != nil {
			return fmt.Errorf("failed to delete resource group: %w", err)
		}

		d.logger.Info("Resource group deleted successfully")
	}

	// Step 8: Purge soft-deleted Key Vaults
	if !d.dryRun {
		if err := d.purgeSoftDeletedKeyVaults(ctx); err != nil {
			// Log but don't fail - purging is best effort
			d.logger.Error(err, "Failed to purge soft-deleted Key Vaults")
		}
	}

	// Final summary with statistics
	if err := d.logFinalSummary(ctx, resourcesClient); err != nil {
		d.logger.Error(err, "Failed to generate final summary")
	}

	return nil
}

// deleteResourcesByType deletes all resources of a given type
func (d *resourceGroupDeleter) deleteResourcesByType(ctx context.Context, client *armresources.Client, resourceType, description string, retries int) error {
	d.logger.Info("Step: Deleting resources", "type", resourceType, "description", description)

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
		d.logger.Info("No resources found", "type", resourceType)
		return nil
	}

	d.logger.Info("Found resources to delete", "count", len(resources), "type", resourceType)

	// Delete each resource with retries
	stats := &deletionStats{}

	for _, resource := range resources {
		if resource.ID == nil || resource.Name == nil {
			continue
		}

		resourceName := *resource.Name
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(ctx, resourceID) {
			d.logger.Info("Skipping locked resource", "name", resourceName, "type", resourceType)
			stats.skipped++
			continue
		}

		if d.dryRun {
			d.logger.Info("[DRY RUN] Would delete resource", "name", resourceName, "type", resourceType)
			stats.deleted++
		} else {
			if err := d.deleteResourceWithRetries(ctx, client, resourceID, resourceName, resourceType, retries); err != nil {
				d.logger.Error(err, "Failed to delete resource after retries", "name", resourceName, "type", resourceType)
				stats.failed++
				// Continue with other resources even if one fails
			} else {
				stats.deleted++
			}
		}
	}

	d.logger.Info("Deletion summary",
		"type", resourceType,
		"deleted", stats.deleted,
		"skipped", stats.skipped,
		"failed", stats.failed)

	return nil
}

// deleteNonNetworkingResources deletes all resources except VNETs, NSGs, DCRs, DCEs, and Container Instances
func (d *resourceGroupDeleter) deleteNonNetworkingResources(ctx context.Context, client *armresources.Client) error {
	d.logger.Info("Step: Deleting application and infrastructure resources")

	// List all resources
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)

	excludedTypes := []string{
		"Microsoft.Network/virtualNetworks",
		"Microsoft.Network/networkSecurityGroups",
		"Microsoft.Insights/dataCollectionRules",
		"Microsoft.Insights/dataCollectionEndpoints",
		"Microsoft.ContainerInstance/containerGroups",
	}

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources: %w", err)
		}
		for _, resource := range page.Value {
			if resource.ID != nil && resource.Type != nil && resource.Name != nil {
				// Skip excluded types
				excluded := false
				for _, excludedType := range excludedTypes {
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
		d.logger.Info("No application resources found to delete")
		return nil
	}

	d.logger.Info("Found application resources to delete", "count", len(resources))

	// Delete each resource
	stats := &deletionStats{}

	for _, resource := range resources {
		resourceName := *resource.Name
		resourceType := *resource.Type
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(ctx, resourceID) {
			d.logger.Info("Skipping locked resource", "name", resourceName, "type", resourceType)
			stats.skipped++
			continue
		}

		if d.dryRun {
			d.logger.Info("[DRY RUN] Would delete resource", "name", resourceName, "type", resourceType)
			stats.deleted++
		} else {
			if err := d.deleteResourceWithRetries(ctx, client, resourceID, resourceName, resourceType, 1); err != nil {
				d.logger.Error(err, "Failed to delete resource", "name", resourceName, "type", resourceType)
				stats.failed++
				// Continue with other resources
			} else {
				stats.deleted++
			}
		}
	}

	d.logger.Info("Application resources deletion summary",
		"deleted", stats.deleted,
		"skipped", stats.skipped,
		"failed", stats.failed)

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
			d.logger.Info("Retrying resource deletion", "name", resourceName, "type", resourceType, "attempt", attempt, "maxRetries", maxRetries)
			time.Sleep(10 * time.Second)
		}

		// Begin delete with API version
		poller, err := client.BeginDeleteByID(ctx, resourceID, apiVersion, nil)
		if err != nil {
			lastErr = err
			continue
		}

		// Wait for completion
		_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
			Frequency: pollInterval,
		})
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				d.logger.Info("Deletion attempt failed, will retry", "name", resourceName, "type", resourceType, "attempt", attempt)
				continue
			}
		} else {
			d.logger.Info("Successfully deleted resource", "name", resourceName, "type", resourceType)
			return nil
		}
	}

	return fmt.Errorf("failed to delete resource after %d attempts: %w", maxRetries, lastErr)
}

// getAPIVersionForResourceType returns the API version for a given resource type
// This uses commonly stable API versions for different resource providers
func (d *resourceGroupDeleter) getAPIVersionForResourceType(resourceType string) string {
	// Map of resource provider namespaces to their stable API versions
	// Updated with latest stable versions as of 2025-11-18
	apiVersions := map[string]string{
		"Microsoft.Network/virtualNetworks":                       "2025-05-01",
		"Microsoft.Network/networkSecurityGroups":                 "2025-05-01",
		"Microsoft.Network/privateEndpoints":                      "2025-05-01",
		"Microsoft.Network/privateLinkServices":                   "2025-05-01",
		"Microsoft.Network/privateDnsZones":                       "2024-06-01",
		"Microsoft.Network/dnszones":                              "2018-05-01",
		"Microsoft.Network/networkSecurityPerimeters":             "2025-05-01",
		"Microsoft.Network/privateEndpointConnections":            "2025-05-01",
		"Microsoft.Network/privateEndpoints/privateDnsZoneGroups": "2025-05-01",
		"Microsoft.Network/privateDnsZones/virtualNetworkLinks":   "2020-06-01",
		"Microsoft.Insights/dataCollectionRules":                  "2024-03-11",
		"Microsoft.Insights/dataCollectionEndpoints":              "2024-03-11",
		"Microsoft.ContainerService/managedClusters":              "2025-10-01",
		"Microsoft.Compute/virtualMachines":                       "2025-04-01",
		"Microsoft.Storage/storageAccounts":                       "2025-06-01",
	}

	// Check if we have a specific API version for this resource type
	if version, ok := apiVersions[resourceType]; ok {
		return version
	}

	// Extract the provider namespace (e.g., "Microsoft.Network" from "Microsoft.Network/virtualNetworks")
	parts := strings.Split(resourceType, "/")
	if len(parts) >= 2 {
		providerNamespace := parts[0]

		// Default API versions for common providers
		// Updated with latest stable versions as of 2025-11-18
		providerDefaults := map[string]string{
			"Microsoft.Network":          "2025-05-01",
			"Microsoft.Compute":          "2025-04-01",
			"Microsoft.Storage":          "2025-06-01",
			"Microsoft.Insights":         "2024-03-11",
			"Microsoft.ContainerService": "2025-10-01",
			"Microsoft.KeyVault":         "2025-05-01",
			"Microsoft.Authorization":    "2022-04-01",
		}

		if version, ok := providerDefaults[providerNamespace]; ok {
			return version
		}
	}

	// Default fallback - use a recent stable version
	// Most Azure resources support this or will redirect to the latest
	return "2023-04-01"
}

// hasLocks checks if a resource has management locks
func (d *resourceGroupDeleter) hasLocks(ctx context.Context, resourceID string) bool {
	// Create management locks client
	locksClient, err := armlocks.NewManagementLocksClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		d.logger.Error(err, "Failed to create locks client, assuming no locks")
		return false
	}

	// Parse resource ID to extract components
	// Format: /subscriptions/{sub}/resourceGroups/{rg}/providers/{provider}/{type}/{name}
	parts := strings.Split(strings.Trim(resourceID, "/"), "/")
	if len(parts) < 8 {
		return false // Invalid resource ID format
	}

	resourceGroupName := parts[3]
	resourceProviderNamespace := parts[5]
	resourceType := parts[6]
	resourceName := parts[7]

	// Build parent resource path if exists
	parentResourcePath := ""
	if len(parts) > 8 {
		// For child resources, build the parent path
		for i := 7; i < len(parts)-2; i += 2 {
			if parentResourcePath != "" {
				parentResourcePath += "/"
			}
			parentResourcePath += parts[i] + "/" + parts[i+1]
		}
		resourceType = parts[len(parts)-2]
		resourceName = parts[len(parts)-1]
	}

	// List locks at resource level
	pager := locksClient.NewListAtResourceLevelPager(
		resourceGroupName,
		resourceProviderNamespace,
		parentResourcePath,
		resourceType,
		resourceName,
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

// deletePublicDNSZones handles deletion of public DNS zones with delegation cleanup
func (d *resourceGroupDeleter) deletePublicDNSZones(ctx context.Context, resourcesClient *armresources.Client) error {
	d.logger.Info("Step: Deleting public DNS zones with delegation cleanup")

	// List DNS zones in the resource group
	filter := "resourceType eq 'Microsoft.Network/dnszones'"
	pager := resourcesClient.NewListByResourceGroupPager(d.resourceGroupName, &armresources.ClientListByResourceGroupOptions{
		Filter: &filter,
	})

	var zones []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list DNS zones: %w", err)
		}
		zones = append(zones, page.Value...)
	}

	if len(zones) == 0 {
		d.logger.Info("No public DNS zones found")
		return nil
	}

	d.logger.Info("Found public DNS zones", "count", len(zones))

	// Get list of all subscriptions for cross-subscription delegation search
	subsClient, err := armsubscriptions.NewClient(d.credential, nil)
	if err != nil {
		d.logger.Error(err, "Failed to create subscriptions client, skipping delegation cleanup")
		// Continue with deletion even if we can't clean up delegations
	}

	var subscriptionIDs []string
	if subsClient != nil {
		subsPager := subsClient.NewListPager(nil)
		for subsPager.More() {
			page, err := subsPager.NextPage(ctx)
			if err != nil {
				d.logger.Error(err, "Failed to list subscriptions")
				break
			}
			for _, sub := range page.Value {
				if sub.SubscriptionID != nil {
					subscriptionIDs = append(subscriptionIDs, *sub.SubscriptionID)
				}
			}
		}
	}

	stats := &deletionStats{}

	for _, zone := range zones {
		if zone.Name == nil {
			continue
		}

		zoneName := *zone.Name
		d.logger.Info("Processing DNS zone", "name", zoneName)

		// Check if this is a subdomain (has parent zone)
		if strings.Count(zoneName, ".") >= 2 {
			// Extract parent domain
			parts := strings.SplitN(zoneName, ".", 2)
			if len(parts) == 2 {
				subdomainName := parts[0]
				parentDomain := parts[1]

				d.logger.Info("Zone appears to be subdomain, searching for parent", "subdomain", subdomainName, "parent", parentDomain)

				// Search for parent zone across subscriptions
				if err := d.removeNSDelegation(ctx, subscriptionIDs, parentDomain, subdomainName); err != nil {
					d.logger.Error(err, "Failed to remove NS delegation", "zone", zoneName)
					// Continue anyway
				}
			}
		}

		// Delete the DNS zone
		if zone.ID != nil {
			if err := d.deleteResourceWithRetries(ctx, resourcesClient, *zone.ID, zoneName, "Microsoft.Network/dnszones", 1); err != nil {
				d.logger.Error(err, "Failed to delete DNS zone", "name", zoneName)
				stats.failed++
			} else {
				stats.deleted++
			}
		}
	}

	d.logger.Info("DNS zones deletion summary", "deleted", stats.deleted, "failed", stats.failed)
	return nil
}

// removeNSDelegation removes NS delegation records from parent DNS zone
func (d *resourceGroupDeleter) removeNSDelegation(ctx context.Context, subscriptionIDs []string, parentDomain, subdomainName string) error {
	// Search for parent zone across all subscriptions
	for _, subID := range subscriptionIDs {
		dnsClient, err := armdns.NewZonesClient(subID, d.credential, nil)
		if err != nil {
			continue
		}

		// List zones and find matching parent
		pager := dnsClient.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				break
			}

			for _, zone := range page.Value {
				if zone.Name != nil && strings.EqualFold(*zone.Name, parentDomain) {
					// Found parent zone - try to delete NS record
					d.logger.Info("Found parent DNS zone", "parent", parentDomain, "subscription", subID)

					if zone.ID == nil {
						continue
					}

					// Extract resource group from zone ID
					parts := strings.Split(*zone.ID, "/")
					if len(parts) < 5 {
						continue
					}
					parentRG := parts[4]

					// Check if NS record exists
					recordsClient, err := armdns.NewRecordSetsClient(subID, d.credential, nil)
					if err != nil {
						continue
					}

					_, err = recordsClient.Get(ctx, parentRG, parentDomain, subdomainName, armdns.RecordTypeNS, nil)
					if err != nil {
						// NS record doesn't exist, nothing to delete
						d.logger.Info("No NS delegation found", "subdomain", subdomainName, "parent", parentDomain)
						return nil
					}

					// Delete the NS record
					d.logger.Info("Removing NS delegation", "subdomain", subdomainName, "parent", parentDomain)
					_, err = recordsClient.Delete(ctx, parentRG, parentDomain, subdomainName, armdns.RecordTypeNS, nil)
					if err != nil {
						return fmt.Errorf("failed to delete NS record: %w", err)
					}

					d.logger.Info("Successfully removed NS delegation", "subdomain", subdomainName, "parent", parentDomain)
					return nil
				}
			}
		}
	}

	d.logger.Info("Parent DNS zone not found in any subscription", "parent", parentDomain)
	return nil
}

// purgeSoftDeletedKeyVaults purges soft-deleted Key Vaults
func (d *resourceGroupDeleter) purgeSoftDeletedKeyVaults(ctx context.Context) error {
	d.logger.Info("Step: Purging soft-deleted Key Vaults")

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
		d.logger.Info("No soft-deleted Key Vaults found")
		return nil
	}

	d.logger.Info("Found soft-deleted Key Vaults", "count", len(deletedVaults))

	stats := &deletionStats{}

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

		d.logger.Info("Purging soft-deleted Key Vault", "name", vaultName, "location", location)

		// Purge with retries
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if attempt > 1 {
				d.logger.Info("Retrying Key Vault purge", "name", vaultName, "attempt", attempt)
				time.Sleep(10 * time.Second)
			}

			poller, err := vaultsClient.BeginPurgeDeleted(ctx, vaultName, location, nil)
			if err != nil {
				// Check if it's a 404 - vault already purged
				var respErr *azcore.ResponseError
				if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
					d.logger.Info("Key Vault already purged", "name", vaultName)
					stats.deleted++
					break
				}
				lastErr = err
				continue
			}

			// Wait for purge to complete
			_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
				Frequency: pollInterval,
			})
			if err != nil {
				lastErr = err
				if attempt < maxRetries {
					continue
				}
			} else {
				d.logger.Info("Successfully purged Key Vault", "name", vaultName)
				stats.deleted++
				break
			}
		}

		if lastErr != nil {
			d.logger.Error(lastErr, "Failed to purge Key Vault after retries", "name", vaultName)
			stats.failed++
		}
	}

	d.logger.Info("Key Vault purging summary", "purged", stats.deleted, "failed", stats.failed)
	return nil
}

// listAllResources lists all resources in the resource group for dry-run mode
func (d *resourceGroupDeleter) listAllResources(ctx context.Context, client *armresources.Client) error {
	d.logger.Info("Listing all resources in resource group", "resourceGroup", d.resourceGroupName)

	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources: %w", err)
		}
		resources = append(resources, page.Value...)
	}

	if len(resources) == 0 {
		d.logger.Info("No resources found in resource group")
		return nil
	}

	d.logger.Info("Resources in resource group", "count", len(resources))

	// Group resources by type for cleaner output
	resourcesByType := make(map[string][]string)
	for _, resource := range resources {
		if resource.Type != nil && resource.Name != nil {
			resourceType := *resource.Type
			resourceName := *resource.Name
			resourcesByType[resourceType] = append(resourcesByType[resourceType], resourceName)
		}
	}

	// Log each resource type and its resources
	for resourceType, names := range resourcesByType {
		d.logger.Info("Resource type", "type", resourceType, "count", len(names), "resources", names)
	}

	return nil
}

// logFinalSummary logs final statistics about the cleanup operation
func (d *resourceGroupDeleter) logFinalSummary(ctx context.Context, client *armresources.Client) error {
	// If we deleted the resource group, there are no remaining resources
	if !d.dryRun {
		d.logger.Info("✓ Cleanup completed successfully",
			"resourceGroup", d.resourceGroupName,
			"status", "Resource group and all resources deleted")
		return nil
	}

	// In dry-run mode, count remaining resources and locked resources
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)

	totalCount := 0
	lockedCount := 0

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list resources for summary: %w", err)
		}

		for _, resource := range page.Value {
			if resource.ID == nil {
				continue
			}
			totalCount++

			// Check if resource has locks
			if d.hasLocks(ctx, *resource.ID) {
				lockedCount++
			}
		}
	}

	if totalCount == 0 {
		d.logger.Info("✓ All resources have been deleted from the resource group",
			"resourceGroup", d.resourceGroupName)
	} else if lockedCount > 0 {
		d.logger.Info("Resource group cleanup completed",
			"resourceGroup", d.resourceGroupName,
			"remainingResources", totalCount,
			"lockedResources", lockedCount,
			"status", "Some resources remain (including locked resources)")
	} else {
		d.logger.Info("Resource group cleanup completed",
			"resourceGroup", d.resourceGroupName,
			"remainingResources", totalCount,
			"status", "Some resources remain (may have failed to delete due to dependencies or errors)")
	}

	return nil
}
