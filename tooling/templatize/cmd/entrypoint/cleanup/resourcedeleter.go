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
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

const (
	pollInterval              = 10 * time.Second
	maxRetries                = 3
	dnsMaxRetries             = 3 // DNS zones need retries due to eventual consistency (matches bash script)
	cosmosMaxRetries          = 3 // Cosmos DB operations need retries (matches bash script)
	privateEndpointMaxRetries = 5 // Private endpoints may fail due to parent resources being deleted
	vnetLinkMaxRetries        = 3 // VNet links can have timing issues
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
	"Microsoft.Network/publicIPAddresses", // Deleted after AKS to avoid load balancer attachment conflicts
	// Monitoring resources
	"Microsoft.Insights/dataCollectionRules",
	"Microsoft.Insights/dataCollectionEndpoints",
	// Container instances (excluded to avoid disruption)
	"Microsoft.ContainerInstance/containerGroups",
}

// resourceGroupDeleter handles ordered deletion of resources in a resource group
type resourceGroupDeleter struct {
	resourceGroupName string
	subscriptionID    string
	credential        azcore.TokenCredential
	logger            logr.Logger
	wait              bool
	dryRun            bool
	stats             deletionStats
	apiVersionCache   map[string]string // Cache for provider API versions
}

type deletionStats struct {
	totalResourcesDeleted int
	failedDeletions       int
}

// execute performs ordered resource deletion following the delete.sh logic.
//
// Deletes all resources in a resource group except those with locks.
// Handles dependencies by deleting resources in the proper order:
//  1. Remove NSP associations first (with force deletion)
//  2. Delete private endpoints and DNS components (in dependency order):
//     a. Private DNS zone groups
//     b. Private endpoint connections
//     c. Private endpoints
//     d. Private DNS zone virtual network links
//     e. Private link services
//     f. Private DNS zones (with verification)
//  3. Delete public DNS zones and clean up NS delegation records
//  4. Delete application and infrastructure resources (VMs, DBs, Storage, AKS, etc.)
//     4b. Delete public IP addresses (after AKS clusters to avoid load balancer conflicts)
//  5. Delete monitoring resources (Data Collection Rules and Endpoints)
//  6. Delete core networking (Virtual Networks and Network Security Groups)
//  7. Purge soft-deleted Key Vaults
//  8. Attempt to delete the resource group itself (with retries and warnings)
//
// Note: Resource group deletion is attempted but may fail if resources remain.
// Warnings are logged instead of failing the entire cleanup.
func (d *resourceGroupDeleter) execute(ctx context.Context) error {
	// Dry-run header
	if d.dryRun {
		d.logInfo("DRY-RUN MODE - No actual deletions will be performed")
	}

	resourcesClient, err := armresources.NewClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resources client: %w", err)
	}

	rgClient, err := armresources.NewResourceGroupsClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource groups client: %w", err)
	}

	_, err = rgClient.Get(ctx, d.resourceGroupName, nil)
	if err != nil {
		// Resource group doesn't exist, nothing to do
		return nil
	}

	// Step 1: Remove NSP associations with force
	stepStart := time.Now()
	if err := d.deleteNetworkSecurityPerimetersWithForce(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete network security perimeters: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 1 (NSP deletion) completed in %v", time.Since(stepStart)))

	// Step 2: Delete private networking components in dependency order
	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateEndpoints/privateDnsZoneGroups", "private DNS zone groups", 1); err != nil {
		return fmt.Errorf("failed to delete private DNS zone groups: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 2a (private DNS zone groups) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateEndpointConnections", "private endpoint connections", 1); err != nil {
		return fmt.Errorf("failed to delete private endpoint connections: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 2b (private endpoint connections) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateEndpoints", "private endpoints", privateEndpointMaxRetries); err != nil {
		return fmt.Errorf("failed to delete private endpoints: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 2c (private endpoints) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateDnsZones/virtualNetworkLinks", "private DNS zone virtual network links", vnetLinkMaxRetries); err != nil {
		return fmt.Errorf("failed to delete private DNS zone virtual network links: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 2d (private DNS zone virtual network links) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateLinkServices", "private link services", 1); err != nil {
		return fmt.Errorf("failed to delete private link services: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 2e (private link services) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/privateDnsZones", "private DNS zones", dnsMaxRetries); err != nil {
		return fmt.Errorf("failed to delete private DNS zones: %w", err)
	}

	if err := d.verifyPrivateDNSZonesDeleted(ctx, resourcesClient); err != nil {
		d.logWarn(fmt.Sprintf("Some private DNS zones may still exist: %v", err))
	}
	d.logInfo(fmt.Sprintf("Step 2f (private DNS zones) completed in %v", time.Since(stepStart)))

	// Step 3: Delete public DNS zones and clean up NS delegation
	stepStart = time.Now()
	if err := d.deletePublicDNSZonesWithDelegation(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete public DNS zones: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 3 (public DNS zones) completed in %v", time.Since(stepStart)))

	// Step 4: Delete application and infrastructure resources
	stepStart = time.Now()
	if err := d.deleteNonNetworkingResources(ctx, resourcesClient); err != nil {
		return fmt.Errorf("failed to delete application resources: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 4 (application resources) completed in %v", time.Since(stepStart)))

	// Step 4b: Delete public IP addresses that were attached to AKS load balancers
	// This is done after AKS deletion to avoid "PublicIPAddressCannotBeDeleted" errors
	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/publicIPAddresses", "public IP addresses", 3); err != nil {
		// Non-fatal - resource group deletion will clean them up if they still fail
		d.logWarn(fmt.Sprintf("Some public IP addresses could not be deleted (will be cleaned up during RG deletion): %v", err))
	}
	d.logInfo(fmt.Sprintf("Step 4b (public IP addresses) completed in %v", time.Since(stepStart)))

	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Insights/dataCollectionRules", "data collection rules", maxRetries); err != nil {
		return fmt.Errorf("failed to delete data collection rules: %w", err)
	}
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Insights/dataCollectionEndpoints", "data collection endpoints", maxRetries); err != nil {
		return fmt.Errorf("failed to delete data collection endpoints: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 5 (monitoring resources) completed in %v", time.Since(stepStart)))

	// Step 6: Delete core networking infrastructure
	stepStart = time.Now()
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/virtualNetworks", "virtual networks", 1); err != nil {
		return fmt.Errorf("failed to delete virtual networks: %w", err)
	}
	if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/networkSecurityGroups", "network security groups", 1); err != nil {
		return fmt.Errorf("failed to delete network security groups: %w", err)
	}
	d.logInfo(fmt.Sprintf("Step 6 (core networking) completed in %v", time.Since(stepStart)))

	// Step 7: Purge soft-deleted Key Vaults
	stepStart = time.Now()
	if err := d.purgeSoftDeletedKeyVaults(ctx); err != nil {
		d.logError(err, "Failed to purge soft-deleted Key Vaults")
	}
	d.logInfo(fmt.Sprintf("Step 7 (Key Vault purge) completed in %v", time.Since(stepStart)))

	// Step 8: Attempt to delete the resource group with retries
	stepStart = time.Now()
	if err := d.deleteResourceGroupWithRetries(ctx); err != nil {
		d.logWarn(fmt.Sprintf("Could not delete resource group (this is non-fatal): %v", err))
	}
	d.logInfo(fmt.Sprintf("Step 8 (resource group deletion) completed in %v", time.Since(stepStart)))

	// Final summary - show what resources remain
	d.logFinalSummary(ctx, resourcesClient)

	return nil
}

// deleteResourcesByType deletes all resources of a given type in parallel
func (d *resourceGroupDeleter) deleteResourcesByType(ctx context.Context, client *armresources.Client, resourceType, description string, retries int) error {
	// List resources of this type
	resources, err := d.listResourcesByType(ctx, client, resourceType)
	if err != nil {
		return err
	}

	if len(resources) == 0 {
		return nil
	}

	if d.dryRun {
		d.logInfo(fmt.Sprintf("Would delete %d %s", len(resources), description))
		return nil
	}

	d.logInfo(fmt.Sprintf("Deleting %d %s...", len(resources), description))

	// Delete all resources in parallel within this type
	group, groupCtx := errgroup.WithContext(ctx)

	for _, resource := range resources {
		if resource.ID == nil || resource.Name == nil {
			continue
		}

		resourceName := *resource.Name
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(groupCtx, resourceID) {
			d.logWarn(fmt.Sprintf("Skipping locked resource: %s", resourceName))
			continue
		}

		// Launch deletion in parallel
		group.Go(func() error {
			// Use more retries for Cosmos DB which can have transient operation locks
			actualRetries := retries
			if strings.EqualFold(resourceType, "Microsoft.DocumentDB/databaseAccounts") {
				actualRetries = cosmosMaxRetries
			}
			if err := d.deleteResourceWithRetries(groupCtx, client, resourceID, resourceName, resourceType, actualRetries); err != nil {

				d.logWarn(fmt.Sprintf("Failed to delete %v: %v", resourceName, err))
				d.stats.failedDeletions++
				return nil
			}
			d.stats.totalResourcesDeleted++
			return nil
		})
	}

	// Wait for all deletions to complete
	if err := group.Wait(); err != nil {
		return err
	}

	return nil
}

// deleteNetworkSecurityPerimetersWithForce deletes NSPs with forceDeletion flag
func (d *resourceGroupDeleter) deleteNetworkSecurityPerimetersWithForce(ctx context.Context, resourcesClient *armresources.Client) error {
	nsps, err := d.listResourcesByType(ctx, resourcesClient, "Microsoft.Network/networkSecurityPerimeters")
	if err != nil {
		return err
	}

	if len(nsps) == 0 {
		return nil
	}

	if d.dryRun {
		d.logInfo(fmt.Sprintf("Would delete %d network security perimeters", len(nsps)))
		return nil
	}

	d.logInfo(fmt.Sprintf("Deleting %d network security perimeters...", len(nsps)))

	clientFactory, err := armnetwork.NewClientFactory(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create network client factory: %w", err)
	}

	nspClient := clientFactory.NewSecurityPerimetersClient()
	group, groupCtx := errgroup.WithContext(ctx)

	for _, nsp := range nsps {
		if nsp.Name == nil || nsp.ID == nil {
			continue
		}

		nspName := *nsp.Name
		nspID := *nsp.ID

		if d.hasLocks(groupCtx, nspID) {
			d.logWarn(fmt.Sprintf("Skipping locked NSP: %s", nspName))
			continue
		}

		group.Go(func() error {
			err := d.executeWithRetries(groupCtx, maxRetries, func(attempt int) error {
				poller, err := nspClient.BeginDelete(groupCtx, d.resourceGroupName, nspName, &armnetwork.SecurityPerimetersClientBeginDeleteOptions{
					ForceDeletion: to.Ptr(true),
				})
				if err != nil {
					return err
				}

				if d.wait {
					return pollUntilDone(groupCtx, poller)
				}
				return nil
			})

			if err != nil {
				d.logWarn(fmt.Sprintf("Failed to delete NSP: %s: %v", nspName, err))
				d.stats.failedDeletions++
			} else if d.wait {
				d.stats.totalResourcesDeleted++
			}
			return nil
		})
	}

	return group.Wait()
}

// deleteResourceGroupWithRetries attempts to delete the resource group with retries
// Returns error only after all retries are exhausted, but does not fail the cleanup
func (d *resourceGroupDeleter) deleteResourceGroupWithRetries(ctx context.Context) error {
	const maxRetries = 5
	const retryDelay = 30 * time.Second

	if d.dryRun {
		d.logInfo("Would attempt to delete resource group")
		return nil
	}

	rgClient, err := armresources.NewResourceGroupsClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create resource groups client: %w", err)
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.logInfo(fmt.Sprintf("Attempting to delete resource group (attempt %d/%d)...", attempt, maxRetries))

		poller, err := rgClient.BeginDelete(ctx, d.resourceGroupName, nil)
		if err != nil {
			// Check if it's a 404 - resource group already deleted
			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
				d.logInfo("Resource group already deleted")
				return nil
			}

			if attempt < maxRetries {
				d.logWarn(fmt.Sprintf("Failed to start resource group deletion (attempt %d/%d): %v. Retrying in %v...", attempt, maxRetries, err, retryDelay))
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("failed to start resource group deletion after %d attempts: %w", maxRetries, err)
		}

		if d.wait {
			d.logInfo("Waiting for resource group deletion to complete...")
			if err := pollUntilDone(ctx, poller); err != nil {
				if attempt < maxRetries {
					d.logWarn(fmt.Sprintf("Resource group deletion failed (attempt %d/%d): %v. Retrying in %v...", attempt, maxRetries, err, retryDelay))
					time.Sleep(retryDelay)
					continue
				}
				return fmt.Errorf("resource group deletion failed after %d attempts: %w", maxRetries, err)
			}
			d.logInfo("Resource group deleted successfully")
			return nil
		}

		// If not waiting, consider it a success if we started the deletion
		d.logInfo("Resource group deletion initiated (not waiting for completion)")
		return nil
	}

	return fmt.Errorf("unexpected: should not reach here")
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

	if d.dryRun {
		d.logInfo(fmt.Sprintf("Would delete %d application resources", len(resources)))
		return nil
	}

	d.logInfo(fmt.Sprintf("Deleting %d application resources...", len(resources)))

	// Delete all resources in parallel within this category
	group, groupCtx := errgroup.WithContext(ctx)

	for _, resource := range resources {
		resourceName := *resource.Name
		resourceType := *resource.Type
		resourceID := *resource.ID

		// Check for locks
		if d.hasLocks(groupCtx, resourceID) {
			d.logWarn(fmt.Sprintf("Skipping locked resource: %s", resourceName))
			continue
		}

		// Launch deletion in parallel
		group.Go(func() error {
			if err := d.deleteResourceWithRetries(groupCtx, client, resourceID, resourceName, resourceType, 1); err != nil {
				d.logWarn(fmt.Sprintf("Failed to delete %s: %v", resourceName, err))
				d.stats.failedDeletions++
				return nil
			}
			d.stats.totalResourcesDeleted++
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
	// Safety check: never delete in dry-run mode
	if d.dryRun {
		return nil
	}

	// Extract API version from resource type
	// Azure SDK requires the API version to delete resources
	apiVersion, err := d.getAPIVersionForResourceType(resourceType)
	if err != nil {
		return fmt.Errorf("failed to get API version for %s: %w", resourceType, err)
	}

	return d.executeWithRetries(ctx, maxRetries, func(attempt int) error {
		// Begin delete with API version
		poller, err := client.BeginDeleteByID(ctx, resourceID, apiVersion, nil)
		if err != nil {
			return err
		}

		if d.wait {
			return pollUntilDone(ctx, poller)
		}
		return nil
	})
}

// getAPIVersionForResourceType returns the API version for a given resource type
// Uses dynamic discovery via Azure Resource Manager Providers API for production reliability
func (d *resourceGroupDeleter) getAPIVersionForResourceType(resourceType string) (string, error) {
	// Extract provider namespace and resource type components
	var providerNamespace, resourceTypeName string
	if idx := strings.Index(resourceType, "/"); idx > 0 {
		providerNamespace = resourceType[:idx]
		resourceTypeName = resourceType[idx+1:]
	} else {
		return "", fmt.Errorf("invalid resource type format: %s", resourceType)
	}

	// Check cache first to avoid repeated API calls
	if d.apiVersionCache == nil {
		d.apiVersionCache = make(map[string]string)
	}

	cacheKey := resourceType
	if cached, ok := d.apiVersionCache[cacheKey]; ok {
		return cached, nil
	}

	// Try dynamic discovery from Azure RM
	ctx := context.Background()
	providersClient, err := armresources.NewProvidersClient(d.subscriptionID, d.credential, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create providers client: %w", err)
	}

	provider, err := providersClient.Get(ctx, providerNamespace, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get provider metadata for %s: %w", providerNamespace, err)
	}

	// Find the specific resource type and get its latest stable API version
	if provider.ResourceTypes != nil {
		for _, rt := range provider.ResourceTypes {
			if rt.ResourceType != nil && *rt.ResourceType == resourceTypeName {
				if rt.APIVersions != nil && len(rt.APIVersions) > 0 {
					// Azure returns versions in descending order (latest first)
					// Prefer stable versions over preview versions
					for _, version := range rt.APIVersions {
						if version != nil && !strings.Contains(*version, "preview") {
							apiVersion := *version
							d.apiVersionCache[cacheKey] = apiVersion
							return apiVersion, nil
						}
					}
					// If only preview versions available, use the latest one
					if rt.APIVersions[0] != nil {
						apiVersion := *rt.APIVersions[0]
						d.apiVersionCache[cacheKey] = apiVersion
						return apiVersion, nil
					}
				}
			}
		}
	}

	// Resource type not found in provider metadata
	return "", fmt.Errorf("resource type %s not found in provider %s metadata", resourceTypeName, providerNamespace)
}

// hasLocks checks if a resource has management locks
func (d *resourceGroupDeleter) hasLocks(ctx context.Context, resourceID string) bool {
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
	if d.dryRun {
		d.logInfo("Would purge soft-deleted Key Vaults (in dry-run mode)")
		return nil
	}

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
		d.logInfo("No soft-deleted Key Vaults found")
		return nil
	}

	// Filter vaults that belong to our resource group
	var vaultsToProcess []*armkeyvault.DeletedVault
	for _, vault := range deletedVaults {
		if vault.Properties != nil && vault.Properties.VaultID != nil {
			vaultID := *vault.Properties.VaultID
			if strings.Contains(vaultID, fmt.Sprintf("/resourceGroups/%s/", d.resourceGroupName)) {
				vaultsToProcess = append(vaultsToProcess, vault)
			}
		}
	}

	if len(vaultsToProcess) == 0 {
		return nil
	}

	d.logInfo(fmt.Sprintf("Purging %d soft-deleted Key Vaults...", len(vaultsToProcess)))

	// Purge all vaults in parallel
	group, groupCtx := errgroup.WithContext(ctx)

	for _, vault := range vaultsToProcess {
		if vault.Name == nil || vault.Properties == nil || vault.Properties.Location == nil {
			continue
		}

		vaultName := *vault.Name
		location := *vault.Properties.Location

		// Launch purge in parallel
		group.Go(func() error {
			err := d.executeWithRetries(groupCtx, maxRetries, func(attempt int) error {
				poller, err := vaultsClient.BeginPurgeDeleted(groupCtx, vaultName, location, nil)
				if err != nil {
					// Check if it's a 404 - vault already purged
					var respErr *azcore.ResponseError
					if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
						d.logInfo(fmt.Sprintf("Key Vault %s already purged", vaultName))
						return nil
					}
					return err
				}

				if d.wait {
					return pollUntilDone(groupCtx, poller)
				}
				return nil
			})

			if err != nil {
				d.logWarn(fmt.Sprintf("Failed to purge Key Vault: %s : Error: %v", vaultName, err))
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
func (d *resourceGroupDeleter) logFinalSummary(ctx context.Context, client *armresources.Client) {
	if d.dryRun {
		d.logInfo(fmt.Sprintf("Dry-run complete for: %s", d.resourceGroupName))
	} else {
		d.logInfo(fmt.Sprintf("Cleanup complete for: %s", d.resourceGroupName))
	}

	// Check what's left in the resource group
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, nil)
	remainingResources := []*armresources.GenericResourceExpanded{}

	// Count remaining resources
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			// If resource group doesn't exist (was deleted), that's success
			if strings.Contains(err.Error(), "ResourceGroupNotFound") {
				d.logInfo("All resources have been deleted from the resource group")
				return
			}
			d.logWarn(fmt.Sprintf("Could not verify remaining resources: %v", err))
			return
		}
		remainingResources = append(remainingResources, page.Value...)
	}

	remainingCount := len(remainingResources)

	if remainingCount == 0 {
		d.logInfo("All resources have been deleted from the resource group")
		return
	}

	// Group remaining resources by type for detailed reporting
	resourcesByType := make(map[string][]string)
	for _, res := range remainingResources {
		if res.Type != nil && res.Name != nil {
			resType := *res.Type
			resName := *res.Name
			resourcesByType[resType] = append(resourcesByType[resType], resName)
		}
	}

	// Log summary
	if d.dryRun {
		d.logInfo(fmt.Sprintf("Resource group cleanup preview completed. %d resources would be deleted", remainingCount))
	} else {
		d.logWarn(fmt.Sprintf("Resource group cleanup completed with %d resources remaining", remainingCount))
		d.logInfo("Remaining resources by type:")
		for resType, names := range resourcesByType {
			d.logInfo(fmt.Sprintf("  %s: %d resources (%s)", resType, len(names), strings.Join(names, ", ")))
		}
		if d.stats.failedDeletions > 0 {
			d.logInfo(fmt.Sprintf("Failed deletions: %d (may be due to dependencies, locks, or AKS-managed resources)", d.stats.failedDeletions))
		}
	}
}

// Logging helper methods with consistent formatting
func (d *resourceGroupDeleter) logInfo(message string) {
	d.logger.Info(fmt.Sprintf("✅ %s", message))
}

func (d *resourceGroupDeleter) logWarn(message string) {
	d.logger.Info(fmt.Sprintf("⚠️  %s", message))
}

func (d *resourceGroupDeleter) logError(err error, message string) {
	d.logger.Error(err, fmt.Sprintf("❌ %s", message))
}

// deletePublicDNSZonesWithDelegation deletes public DNS zones and cleans up NS records in parent zones
func (d *resourceGroupDeleter) deletePublicDNSZonesWithDelegation(ctx context.Context, resourcesClient *armresources.Client) error {
	// List all public DNS zones in this resource group
	dnsZones, err := d.listResourcesByType(ctx, resourcesClient, "Microsoft.Network/dnszones")
	if err != nil {
		return err
	}

	if len(dnsZones) == 0 {
		return nil
	}

	if d.dryRun {
		d.logInfo(fmt.Sprintf("Would delete %d public DNS zones and clean up delegation", len(dnsZones)))
		return nil
	}

	d.logInfo(fmt.Sprintf("Deleting %d public DNS zones...", len(dnsZones)))

	// For each DNS zone, find and delete NS records in parent zones across all subscriptions
	for _, zone := range dnsZones {
		if zone.Name == nil {
			continue
		}

		zoneName := *zone.Name

		// Extract parent zone name (e.g., "sub.example.com" -> "example.com")
		parts := strings.Split(zoneName, ".")
		if len(parts) > 2 {
			parentZone := strings.Join(parts[1:], ".")
			if err := d.deleteNSDelegationRecords(ctx, zoneName, parentZone); err != nil {
				d.logWarn(fmt.Sprintf("Failed to clean up NS records for %s: %v", zoneName, err))
			}
		}

		// Now delete the DNS zone itself
		if err := d.deleteResourcesByType(ctx, resourcesClient, "Microsoft.Network/dnszones", "public DNS zones", 1); err != nil {
			return err
		}
	}

	return nil
}

// deleteNSDelegationRecords searches for and deletes NS delegation records across all subscriptions
func (d *resourceGroupDeleter) deleteNSDelegationRecords(ctx context.Context, childZone, parentZone string) error {
	// Create a subscription client to list all subscriptions
	subsClient, err := armsubscriptions.NewClient(d.credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create subscriptions client: %w", err)
	}

	subsPager := subsClient.NewListPager(nil)
	for subsPager.More() {
		page, err := subsPager.NextPage(ctx)
		if err != nil {
			d.logWarn(fmt.Sprintf("Failed to list subscriptions: %v", err))
			continue
		}

		for _, sub := range page.Value {
			if sub.SubscriptionID == nil {
				continue
			}

			subID := *sub.SubscriptionID

			// Create DNS client for this subscription
			dnsClient, err := armdns.NewZonesClient(subID, d.credential, nil)
			if err != nil {
				continue
			}

			// List DNS zones in this subscription
			zonesPager := dnsClient.NewListPager(nil)
			for zonesPager.More() {
				zonePage, err := zonesPager.NextPage(ctx)
				if err != nil {
					continue
				}

				for _, zone := range zonePage.Value {
					if zone.Name == nil {
						continue
					}

					// Check if this is the parent zone we're looking for
					if strings.EqualFold(*zone.Name, parentZone) && zone.ID != nil {
						// Extract resource group from zone ID
						idParts := strings.Split(*zone.ID, "/")
						var rgName string
						for i, part := range idParts {
							if strings.EqualFold(part, "resourceGroups") && i+1 < len(idParts) {
								rgName = idParts[i+1]
								break
							}
						}

						if rgName != "" {
							// Try to delete NS record set
							if err := d.deleteNSRecordSet(ctx, subID, rgName, parentZone, childZone); err != nil {
								d.logWarn(fmt.Sprintf("Could not delete NS record for %s in %s: %v", childZone, parentZone, err))
							}
						}
					}
				}
			}
		}
	}

	return nil
}

// deleteNSRecordSet deletes an NS record set from a DNS zone
func (d *resourceGroupDeleter) deleteNSRecordSet(ctx context.Context, subscriptionID, resourceGroup, zoneName, recordSetName string) error {
	recordSetsClient, err := armdns.NewRecordSetsClient(subscriptionID, d.credential, nil)
	if err != nil {
		return err
	}

	// Extract just the subdomain part for the record set name
	parts := strings.Split(recordSetName, ".")
	if len(parts) > 0 {
		recordSetName = parts[0]
	}

	_, err = recordSetsClient.Delete(ctx, resourceGroup, zoneName, recordSetName, armdns.RecordTypeNS, nil)
	return err
}

// verifyPrivateDNSZonesDeleted ensures all private DNS zones are deleted
func (d *resourceGroupDeleter) verifyPrivateDNSZonesDeleted(ctx context.Context, client *armresources.Client) error {
	if d.dryRun {
		return nil
	}

	remaining, err := d.listResourcesByType(ctx, client, "Microsoft.Network/privateDnsZones")
	if err != nil {
		return fmt.Errorf("failed to verify: %w", err)
	}

	if len(remaining) > 0 {
		var names []string
		for _, zone := range remaining {
			if zone.Name != nil {
				names = append(names, *zone.Name)
			}
		}
		return fmt.Errorf("%d zones remaining: %s", len(remaining), strings.Join(names, ", "))
	}

	return nil
}

// listResourcesByType lists all resources of a given type in the resource group
func (d *resourceGroupDeleter) listResourcesByType(ctx context.Context, client *armresources.Client, resourceType string) ([]*armresources.GenericResourceExpanded, error) {
	filter := fmt.Sprintf("resourceType eq '%s'", resourceType)
	pager := client.NewListByResourceGroupPager(d.resourceGroupName, &armresources.ClientListByResourceGroupOptions{
		Filter: &filter,
	})

	var resources []*armresources.GenericResourceExpanded
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list resources of type %s: %w", resourceType, err)
		}
		resources = append(resources, page.Value...)
	}

	return resources, nil
}

// executeWithRetries executes an operation with exponential backoff retries
func (d *resourceGroupDeleter) executeWithRetries(ctx context.Context, maxAttempts int, operation func(attempt int) error) error {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// Linear backoff: 10 seconds between retries (matches bash script)
			time.Sleep(10 * time.Second)
		}

		err := operation(attempt)
		if err == nil {
			return nil
		}

		lastErr = err
	}

	return fmt.Errorf("operation failed after %d attempts: %w", maxAttempts, lastErr)
}

// pollUntilDone polls an Azure operation until completion
func pollUntilDone[T any](ctx context.Context, poller *runtime.Poller[T]) error {
	_, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: pollInterval,
	})
	return err
}
