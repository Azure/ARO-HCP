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
	"io"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets/azureutil"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type validationResult struct {
	SubscriptionName       string
	AssetPool              string
	ExpectedResourceGroups int
	ActualResourceGroups   int
	ExpectedIdentities     int
	ActualIdentities       int
	MissingResourceGroups  []string
	MissingIdentities      []string
	UnexpectedIdentities   []string
}

func (r validationResult) valid() bool {
	return len(r.MissingResourceGroups) == 0 && len(r.MissingIdentities) == 0 && len(r.UnexpectedIdentities) == 0
}

func validateInventories(ctx context.Context, credential azcore.TokenCredential, inventories []resolvedInventory, topology Topology, newClients func(string, azcore.TokenCredential) (*poolClients, error), out io.Writer) error {
	failed := 0
	for _, inventory := range inventories {
		clients, err := newClients(inventory.SubscriptionID, credential)
		if err != nil {
			return fmt.Errorf("asset pool %q: %w", inventory.AssetPool.Name, err)
		}
		result, err := compareInventory(ctx, clients, inventory.AssetInventory, topology)
		if err != nil {
			return err
		}
		result.SubscriptionName = inventory.SubscriptionName
		result.AssetPool = inventory.AssetPool.Name
		writeValidationResult(out, result)
		if result.valid() {
			continue
		}
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("infrastructure identity pool validation failed for %d pool(s)", failed)
	}
	return nil
}

func compareInventory(ctx context.Context, clients *poolClients, inventory slots.AssetInventory, topology Topology) (validationResult, error) {
	result := validationResult{}
	for _, leaseName := range expectedBundleNames(inventory) {
		groups, err := topology.AzureResourceGroups(leaseName)
		if err != nil {
			return validationResult{}, err
		}
		for _, group := range groups {
			if err := compareResourceGroup(ctx, clients, group, &result); err != nil {
				return validationResult{}, err
			}
		}
	}
	sort.Strings(result.MissingResourceGroups)
	sort.Strings(result.MissingIdentities)
	sort.Strings(result.UnexpectedIdentities)
	return result, nil
}

func compareResourceGroup(ctx context.Context, clients *poolClients, group azureResourceGroup, result *validationResult) error {
	result.ExpectedResourceGroups++
	result.ExpectedIdentities += len(group.IdentityNames)
	actual, exists, err := loadResourceGroupIdentities(ctx, clients, group.Name)
	if err != nil {
		return err
	}
	if !exists {
		result.MissingResourceGroups = append(result.MissingResourceGroups, group.Name)
		return nil
	}
	result.ActualResourceGroups++
	result.ActualIdentities += len(actual)
	reported, err := azureutil.UniqueReportedNames(group.Name, actual)
	if err != nil {
		return err
	}
	missing, unexpected := azureutil.IdentityNameDiff(azureutil.NormalizedNameSet(group.IdentityNames), reported)
	for _, name := range missing {
		result.MissingIdentities = append(result.MissingIdentities, group.Name+"/"+name)
	}
	for _, name := range unexpected {
		result.UnexpectedIdentities = append(result.UnexpectedIdentities, group.Name+"/"+name)
	}
	return nil
}

func loadResourceGroupIdentities(ctx context.Context, clients *poolClients, resourceGroup string) ([]string, bool, error) {
	_, err := clients.resourceGroups.Get(ctx, resourceGroup, nil)
	if azureutil.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed getting resource group %q: %w", resourceGroup, err)
	}
	var names []string
	pager := clients.identities.NewListByResourceGroupPager(resourceGroup, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("failed listing identities in resource group %q: %w", resourceGroup, err)
		}
		for _, identity := range page.Value {
			name, err := azureutil.ListedIdentityName(identity, resourceGroup)
			if err != nil {
				return nil, false, err
			}
			names = append(names, name)
		}
	}
	return names, true, nil
}

func writeValidationResult(out io.Writer, result validationResult) {
	fmt.Fprintf(out, "asset pool %q subscription %q:\n", result.AssetPool, result.SubscriptionName)
	fmt.Fprintf(out, "  resource groups: expected=%d actual=%d missing=%d\n",
		result.ExpectedResourceGroups, result.ActualResourceGroups, len(result.MissingResourceGroups))
	fmt.Fprintf(out, "  managed identities: expected=%d actual=%d missing=%d unexpected=%d\n",
		result.ExpectedIdentities, result.ActualIdentities, len(result.MissingIdentities), len(result.UnexpectedIdentities))
	for _, name := range result.MissingResourceGroups {
		fmt.Fprintf(out, "  missing resource group %s\n", name)
	}
	for _, name := range result.MissingIdentities {
		fmt.Fprintf(out, "  missing identity %s\n", name)
	}
	for _, name := range result.UnexpectedIdentities {
		fmt.Fprintf(out, "  unexpected identity %s\n", name)
	}
}
