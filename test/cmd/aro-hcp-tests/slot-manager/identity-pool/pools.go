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

package identitypool

import (
	"context"
	"fmt"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

// subscriptionIDResolverFunc resolves a human-readable Azure subscription name
// to its subscription ID. The production implementation wraps
// framework.GetSubscriptionID; tests inject a stub.
type subscriptionIDResolverFunc func(ctx context.Context, name string) (string, error)

type identityPool struct {
	Environment        string
	Region             string
	ProvisioningRegion string
	SubscriptionName   string
	SubscriptionID     string
	Slots              []slots.ExpandedSlot
}

// loadIdentityPools loads pools for the given environment. When
// subscriptionFilter is non-empty, only pools whose subscription_name matches
// one of the filter values are included (regardless of identity_provisioning).
// When subscriptionFilter is empty, pools with identity_provisioning: unmanaged
// are skipped.
func loadIdentityPools(ctx context.Context, catalogPath, environment string, subscriptionFilter []string, resolveSubscriptionID subscriptionIDResolverFunc) ([]identityPool, error) {
	catalog, err := slots.LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}

	environmentConfig, found := catalog.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}

	filterSet := make(map[string]struct{}, len(subscriptionFilter))
	for _, name := range subscriptionFilter {
		filterSet[name] = struct{}{}
	}

	resolvedIDs := map[string]string{}
	pools := make([]identityPool, 0, len(environmentConfig.Pools))
	for _, pool := range environmentConfig.Pools {
		if len(filterSet) > 0 {
			if _, match := filterSet[pool.SubscriptionName]; !match {
				continue
			}
		} else if pool.IsUnmanaged() {
			continue
		}

		subscriptionID, found := resolvedIDs[pool.SubscriptionName]
		if !found {
			subscriptionID, err = resolveSubscriptionID(ctx, pool.SubscriptionName)
			if err != nil {
				return nil, fmt.Errorf("failed getting subscription ID for %q: %w", pool.SubscriptionName, err)
			}
			resolvedIDs[pool.SubscriptionName] = subscriptionID
		}

		pools = append(pools, identityPool{
			Environment:        environment,
			Region:             pool.Region,
			ProvisioningRegion: pool.EffectiveIdentityProvisioningRegion(),
			SubscriptionName:   pool.SubscriptionName,
			SubscriptionID:     subscriptionID,
			Slots:              slots.ExpandSlotsForPool(environment, pool),
		})
	}

	return pools, nil
}
