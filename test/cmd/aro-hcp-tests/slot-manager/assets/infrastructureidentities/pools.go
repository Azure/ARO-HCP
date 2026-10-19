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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/assets"
	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type resolvedInventory struct {
	slots.AssetInventory
	SubscriptionID string
}

func (h *Handler) resolveInventories(ctx context.Context, request assets.PoolRequest) (azcore.TokenCredential, []resolvedInventory, error) {
	credential, resolveSubscriptionID, err := h.poolDependencies()
	if err != nil {
		return nil, nil, err
	}
	selected := selectInfrastructureInventories(request)
	if len(selected) == 0 {
		return nil, nil, fmt.Errorf("no infrastructure identity pools matched environment %q", request.Environment)
	}
	if err := validateInventoriesHaveSubscriptions(selected); err != nil {
		return nil, nil, err
	}
	inventories, err := resolveInventorySubscriptions(ctx, selected, resolveSubscriptionID)
	if err != nil {
		return nil, nil, err
	}
	return credential, inventories, nil
}

func selectInfrastructureInventories(request assets.PoolRequest) []slots.AssetInventory {
	selected := make([]slots.AssetInventory, 0, len(request.AssetInventories))
	for _, inventory := range request.AssetInventories {
		if !includeInventory(inventory, request.IncludeUnmanaged) {
			continue
		}
		selected = append(selected, inventory)
	}
	return selected
}

func includeInventory(inventory slots.AssetInventory, includeUnmanaged bool) bool {
	switch {
	case inventory.AssetPool.Kind != slots.KindInfrastructureIdentities:
		return false
	case inventory.AssetPool.Provisioning != slots.IdentityProvisioningUnmanaged:
		return true
	default:
		return includeUnmanaged
	}
}

func validateInventoriesHaveSubscriptions(inventories []slots.AssetInventory) error {
	for _, inventory := range inventories {
		if strings.TrimSpace(inventory.SubscriptionName) != "" {
			continue
		}
		return fmt.Errorf("asset pool %q has empty infrastructure subscription", inventory.AssetPool.Name)
	}
	return nil
}

func resolveInventorySubscriptions(ctx context.Context, selected []slots.AssetInventory, resolveSubscriptionID subscriptionIDResolverFunc) ([]resolvedInventory, error) {
	inventories := make([]resolvedInventory, 0, len(selected))
	resolvedIDs := map[string]string{}
	for _, inventory := range selected {
		subscriptionName := strings.TrimSpace(inventory.SubscriptionName)
		subscriptionID, found := resolvedIDs[subscriptionName]
		if found {
			inventories = append(inventories, resolvedInventory{AssetInventory: inventory, SubscriptionID: subscriptionID})
			continue
		}
		subscriptionID, err := resolveSubscriptionID(ctx, subscriptionName)
		if err != nil {
			return nil, fmt.Errorf("failed getting subscription ID for %q: %w", subscriptionName, err)
		}
		resolvedIDs[subscriptionName] = subscriptionID
		inventories = append(inventories, resolvedInventory{AssetInventory: inventory, SubscriptionID: subscriptionID})
	}
	return inventories, nil
}

func provisioningRegion(pool slots.AssetPool) (string, error) {
	if pool.InfrastructureIdentities == nil || strings.TrimSpace(pool.InfrastructureIdentities.ProvisioningRegion) == "" {
		return "", fmt.Errorf("asset pool %q requires infrastructure_identities.provisioning_region", pool.Name)
	}
	return strings.TrimSpace(pool.InfrastructureIdentities.ProvisioningRegion), nil
}
