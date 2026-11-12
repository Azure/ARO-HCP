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

package infrastructureidentities

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type poolClients struct {
	resourceGroups *armresources.ResourceGroupsClient
	identities     *armmsi.UserAssignedIdentitiesClient
}

func azurePoolClients(subscriptionID string, credential azcore.TokenCredential) (*poolClients, error) {
	resourceGroups, err := armresources.NewResourceGroupsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating resource groups client: %w", err)
	}
	msiFactory, err := armmsi.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating managed identity client factory: %w", err)
	}
	return &poolClients{
		resourceGroups: resourceGroups,
		identities:     msiFactory.NewUserAssignedIdentitiesClient(),
	}, nil
}

func applyInventories(ctx context.Context, credential azcore.TokenCredential, inventories []resolvedInventory, topology Topology, newClients func(string, azcore.TokenCredential) (*poolClients, error)) error {
	logger := logr.FromContextOrDiscard(ctx)
	for _, inventory := range inventories {
		region, err := provisioningRegion(inventory.AssetPool)
		if err != nil {
			return err
		}
		clients, err := newClients(inventory.SubscriptionID, credential)
		if err != nil {
			return fmt.Errorf("asset pool %q: %w", inventory.AssetPool.Name, err)
		}
		logger.Info("applying infrastructure identity pool",
			"assetPool", inventory.AssetPool.Name,
			"subscriptionName", inventory.SubscriptionName,
			"subscriptionID", inventory.SubscriptionID,
			"provisioningRegion", region,
			"capacity", inventory.Capacity,
		)
		if err := applyInventory(ctx, clients, inventory, topology, region); err != nil {
			return err
		}
	}
	return nil
}

func applyInventory(ctx context.Context, clients *poolClients, inventory resolvedInventory, topology Topology, region string) error {
	for _, leaseName := range expectedBundleNames(inventory.AssetInventory) {
		groups, err := topology.AzureResourceGroups(leaseName)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if err := applyResourceGroup(ctx, clients, region, leaseName, group); err != nil {
				return fmt.Errorf("asset pool %q bundle %q: %w", inventory.AssetPool.Name, leaseName, err)
			}
		}
	}
	return nil
}

func applyResourceGroup(ctx context.Context, clients *poolClients, region, leaseName string, group azureResourceGroup) error {
	_, err := clients.resourceGroups.CreateOrUpdate(ctx, group.Name, armresources.ResourceGroup{
		Location: to.Ptr(region),
		Tags:     resourceGroupTags(leaseName, group),
	}, nil)
	if err != nil {
		return fmt.Errorf("failed creating resource group %q: %w", group.Name, err)
	}
	for _, identityName := range group.IdentityNames {
		_, err := clients.identities.CreateOrUpdate(ctx, group.Name, identityName, armmsi.Identity{
			Location: to.Ptr(region),
		}, nil)
		if err != nil {
			return fmt.Errorf("failed creating identity %q in resource group %q: %w", identityName, group.Name, err)
		}
	}
	return nil
}

func resourceGroupTags(leaseName string, group azureResourceGroup) map[string]*string {
	tags := map[string]*string{
		"purpose": to.Ptr("aro-hcp-infrastructure-identity-pool"),
		"persist": to.Ptr("true"),
		"bundle":  to.Ptr(leaseName),
	}
	switch {
	case group.ServiceCluster:
		tags["role"] = to.Ptr("service")
	default:
		tags["role"] = to.Ptr("management")
		tags["stamp"] = to.Ptr(strconv.Itoa(group.Stamp))
	}
	return tags
}

func expectedBundleNames(inventory slots.AssetInventory) []string {
	names := make([]string, inventory.Capacity)
	for index := 0; index < inventory.Capacity; index++ {
		names[index] = inventory.ResourceName(index)
	}
	return names
}
