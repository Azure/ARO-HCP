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

package e2eidentities

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

// subscriptionIDResolverFunc resolves a human-readable Azure subscription name
// to its subscription ID. The production implementation wraps
// framework.GetSubscriptionID; tests inject a stub.
type subscriptionIDResolverFunc func(ctx context.Context, name string) (string, error)

type identityPool struct {
	Environment             string
	Region                  string
	ProvisioningRegion      string
	SubscriptionName        string
	SubscriptionID          string
	IdentityContainerPrefix string
	Slots                   []slots.ExpandedSlot
}

// resolveIdentityPools resolves pools for the given environment. When
// subscriptionFilter is non-empty, only pools whose E2E subscription matches
// one of the filter values are included, regardless of provisioning policy.
// Without a filter, pools with unmanaged E2E identity provisioning are skipped
// (slot_assets.e2e_identities.provisioning).
func resolveIdentityPools(ctx context.Context, environment string, catalogPools []slots.Pool, subscriptionFilter []string, resolveSubscriptionID subscriptionIDResolverFunc) ([]identityPool, error) {
	filter := sets.New[string]()
	for _, name := range subscriptionFilter {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		filter.Insert(name)
	}
	resolvedIDs := map[string]string{}
	pools := make([]identityPool, 0, len(catalogPools))
	for _, pool := range catalogPools {
		if !includePool(pool, filter) {
			continue
		}
		subscriptionName := pool.Subscriptions.E2E
		subscriptionID, found := resolvedIDs[subscriptionName]
		if !found {
			resolvedSubscriptionID, err := resolveSubscriptionID(ctx, subscriptionName)
			if err != nil {
				return nil, fmt.Errorf("failed getting subscription ID for %q: %w", subscriptionName, err)
			}
			subscriptionID = resolvedSubscriptionID
			resolvedIDs[subscriptionName] = subscriptionID
		}
		pools = append(pools, identityPool{
			Environment:             environment,
			Region:                  pool.Region,
			ProvisioningRegion:      pool.EffectiveIdentityProvisioningRegion(),
			SubscriptionName:        subscriptionName,
			SubscriptionID:          subscriptionID,
			IdentityContainerPrefix: pool.SlotAssets.E2EIdentities.ResourceGroupPrefix,
			Slots:                   slots.ExpandSlotsForPool(environment, pool),
		})
	}
	return pools, nil
}

func includePool(pool slots.Pool, filter sets.Set[string]) bool {
	switch {
	case pool.SlotAssets.E2EIdentities == nil:
		return false
	case filter.Len() > 0:
		return filter.Has(pool.Subscriptions.E2E)
	case pool.IsUnmanaged():
		return false
	default:
		return true
	}
}
