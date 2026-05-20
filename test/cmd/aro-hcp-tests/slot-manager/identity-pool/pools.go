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
	"fmt"

	"github.com/Azure/ARO-HCP/test/cmd/aro-hcp-tests/slot-manager/slots"
)

type identityPool struct {
	Environment        string
	Region             string
	ProvisioningRegion string
	SubscriptionName   string
	SubscriptionID     string
	Slots              []slots.ExpandedSlot
}

func loadIdentityPools(catalogPath, environment string) ([]identityPool, error) {
	catalog, err := slots.LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}

	environmentConfig, found := catalog.Environments[environment]
	if !found {
		return nil, fmt.Errorf("unknown environment %q", environment)
	}

	pools := make([]identityPool, 0, len(environmentConfig.Pools))
	for _, pool := range environmentConfig.Pools {
		pools = append(pools, identityPool{
			Environment:        environment,
			Region:             pool.Region,
			ProvisioningRegion: pool.EffectiveIdentityProvisioningRegion(),
			SubscriptionName:   pool.SubscriptionName,
			Slots:              slots.ExpandSlotsForPool(environment, pool),
		})
	}

	return pools, nil
}
