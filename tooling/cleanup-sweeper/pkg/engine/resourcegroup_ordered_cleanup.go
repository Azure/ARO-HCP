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

package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armlocks"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"

	"github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/runner"
	armsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/arm"
	dnssteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/dns"
	kvsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/keyvault"
	netsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/network"
	rgsteps "github.com/Azure/ARO-HCP/tooling/cleanup-sweeper/pkg/engine/steps/resourcegroup"
)

const (
	maxRetries                = 3
	dnsMaxRetries             = 3 // DNS zones need retries due to eventual consistency (matches bash script)
	cosmosMaxRetries          = 3 // Cosmos DB operations need retries due to eventual consistency and operation locks
	privateEndpointMaxRetries = 5 // Private endpoints may fail due to parent resources being deleted
	vnetLinkMaxRetries        = 3 // VNet links can have timing issues
)

type WorkflowOptions struct {
	Wait        bool
	DryRun      bool
	Parallelism int
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
func ResourceGroupOrderedCleanupWorkflow(
	ctx context.Context,
	resourceGroupName string,
	subscriptionID string,
	credential azcore.TokenCredential,
	opts WorkflowOptions,
) (*runner.Engine, error) {
	rgClient, err := armresources.NewResourceGroupsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource groups client: %w", err)
	}

	_, err = rgClient.Get(ctx, resourceGroupName, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			runner.LoggerFromContext(ctx).Info(
				"Resource group not found; skipping ordered cleanup workflow",
				"resourceGroup", resourceGroupName,
				"subscriptionID", subscriptionID,
			)
			return &runner.Engine{
				Parallelism: opts.Parallelism,
				DryRun:      opts.DryRun,
				Wait:        opts.Wait,
			}, nil
		}
		return nil, fmt.Errorf("failed to get resource group %q: %w", resourceGroupName, err)
	}

	resourcesClient, err := armresources.NewClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create resources client: %w", err)
	}
	locksClient, err := armlocks.NewManagementLocksClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create management locks client: %w", err)
	}
	providersClient, err := armresources.NewProvidersClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create providers client: %w", err)
	}

	clientFactory, err := armnetwork.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create network client factory: %w", err)
	}
	nspClient := clientFactory.NewSecurityPerimetersClient()
	vaultsClient, err := armkeyvault.NewVaultsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create vaults client: %w", err)
	}
	subsClient, err := armsubscriptions.NewClient(credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
	}

	return &runner.Engine{
		Parallelism: opts.Parallelism,
		DryRun:      opts.DryRun,
		Wait:        opts.Wait,
		PostRunFn:   resourceGroupSummaryFn(resourcesClient, resourceGroupName, opts),
		Steps: []runner.Step{
			netsteps.NewNSPForceDeleteStep(netsteps.NSPForceDeleteStepConfig{
				ResourceGroupName: resourceGroupName,
				ResourcesClient:   resourcesClient,
				LocksClient:       locksClient,
				NSPClient:         nspClient,
				Name:              "Delete network security perimeters",
				Retries:           maxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateEndpoints/privateDnsZoneGroups"}},
				Name:              "Delete private DNS zone groups",
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateEndpointConnections"}},
				Name:              "Delete private endpoint connections",
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateEndpoints"}},
				Name:              "Delete private endpoints",
				Retries:           privateEndpointMaxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateDnsZones/virtualNetworkLinks"}},
				Name:              "Delete private DNS zone virtual network links",
				Retries:           vnetLinkMaxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateLinkServices"}},
				Name:              "Delete private link services",
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/privateDnsZones"}},
				Name:              "Delete private DNS zones",
				Retries:           dnsMaxRetries,
				ContinueOnError:   true,
				Verify: func(ctx context.Context) error {
					return dnssteps.VerifyPrivateDNSZonesDeleted(ctx, resourcesClient, resourceGroupName)
				},
			}),
			dnssteps.NewDeleteNSDelegationRecordsStep(dnssteps.DeleteNSDelegationRecordsStepConfig{
				ResourceGroupName: resourceGroupName,
				Credential:        credential,
				ResourcesClient:   resourcesClient,
				SubsClient:        subsClient,
				Name:              "Delete parent NS delegations",
				Retries:           1,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/dnszones"}},
				Name:              "Delete public DNS zones",
				Retries:           dnsMaxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector: armsteps.ResourceSelector{ExcludedResourceTypes: []string{
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
					// Cosmos DB handled in its own step with extra retries
					"Microsoft.DocumentDB/databaseAccounts",
				}},
				Name:            "Delete non-networking resources",
				Retries:         1,
				ContinueOnError: true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.DocumentDB/databaseAccounts"}},
				Name:              "Delete Cosmos DB accounts",
				Retries:           cosmosMaxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/publicIPAddresses"}},
				Name:              "Delete public IP addresses",
				Retries:           3,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Insights/dataCollectionRules"}},
				Name:              "Delete data collection rules",
				Retries:           maxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Insights/dataCollectionEndpoints"}},
				Name:              "Delete data collection endpoints",
				Retries:           maxRetries,
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/virtualNetworks"}},
				Name:              "Delete virtual networks",
				ContinueOnError:   true,
			}),
			armsteps.NewDeletionStep(armsteps.DeletionStepConfig{
				ResourceGroupName: resourceGroupName,
				Client:            resourcesClient,
				LocksClient:       locksClient,
				ProvidersClient:   providersClient,
				Selector:          armsteps.ResourceSelector{IncludedResourceTypes: []string{"Microsoft.Network/networkSecurityGroups"}},
				Name:              "Delete network security groups",
				ContinueOnError:   true,
			}),
			kvsteps.NewPurgeDeletedStep(kvsteps.PurgeDeletedStepConfig{
				ResourceGroupName: resourceGroupName,
				VaultsClient:      vaultsClient,
				Retries:           maxRetries,
				ContinueOnError:   true,
			}),
			rgsteps.NewDeleteStep(rgsteps.DeleteStepConfig{
				ResourceGroupName: resourceGroupName,
				RGClient:          rgClient,
				Retries:           5,
				ContinueOnError:   true,
			}),
		},
	}, nil
}

// resourceGroupSummaryFn returns a PostRunFn that lists remaining resources in
// a resource group and logs a summary. Errors are informational only.
func resourceGroupSummaryFn(
	client *armresources.Client,
	resourceGroupName string,
	opts WorkflowOptions,
) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		logger := runner.LoggerFromContext(ctx)
		if opts.DryRun {
			logger.Info("Dry-run workflow complete; collecting final state")
		} else {
			logger.Info("Ordered cleanup workflow complete; collecting final state")
		}

		pager := client.NewListByResourceGroupPager(resourceGroupName, nil)
		var remaining []*armresources.GenericResourceExpanded
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if strings.Contains(err.Error(), "ResourceGroupNotFound") {
					logger.Info("Resource group has been deleted")
					return nil
				}
				logger.Info("Could not verify remaining resources", "error", err)
				return nil
			}
			remaining = append(remaining, page.Value...)
		}

		if len(remaining) == 0 {
			logger.Info("All resources have been deleted from the resource group")
			return nil
		}

		byType := make(map[string][]string)
		for _, res := range remaining {
			if res.Type != nil && res.Name != nil {
				byType[*res.Type] = append(byType[*res.Type], *res.Name)
			}
		}

		if opts.DryRun {
			logger.Info(fmt.Sprintf("Resource group cleanup preview completed. %d resources would be deleted", len(remaining)))
		} else {
			logger.Info(fmt.Sprintf("Resource group cleanup completed with %d resources remaining", len(remaining)))
			if !opts.Wait {
				logger.Info("Cleanup ran with wait=false, so asynchronous deletes may still be in progress")
			}
			for resType, names := range byType {
				logger.Info("Remaining resources",
					"type", resType,
					"count", len(names),
					"names", strings.Join(names, ", "),
				)
			}
		}
		return nil
	}
}
