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

package slots

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type AssetKind string
type Allocation string

const (
	KindE2EIdentities            AssetKind  = "e2e_identities"
	KindInfrastructureIdentities AssetKind  = "infrastructure_identities"
	AllocationDedicated          Allocation = "dedicated"
	AllocationLeased             Allocation = "leased"
)

type LeasedAsset struct {
	Allocation   Allocation `yaml:"allocation"`
	AssetPool    string     `yaml:"asset_pool"`
	UnitsPerSlot int        `yaml:"units_per_slot,omitempty"`
}

func (a *LeasedAsset) UnmarshalYAML(node *yaml.Node) error {
	type wire LeasedAsset
	value := wire{UnitsPerSlot: 1}
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.UnitsPerSlot <= 0 {
		return fmt.Errorf("units_per_slot must be positive")
	}
	*a = LeasedAsset(value)
	return nil
}

type AssetRequirement struct {
	Kind         AssetKind  `yaml:"kind"`
	Allocation   Allocation `yaml:"allocation"`
	AssetPool    string     `yaml:"asset_pool,omitempty"`
	UnitsPerSlot int        `yaml:"units_per_slot,omitempty"`
}

type InfrastructureIdentitiesConfig struct {
	ProvisioningRegion string   `yaml:"provisioning_region,omitempty"`
	IdentityNames      []string `yaml:"identity_names,omitempty"`
}

type AssetPool struct {
	Name                     string                          `yaml:"name"`
	Kind                     AssetKind                       `yaml:"kind"`
	Provisioning             string                          `yaml:"provisioning,omitempty"`
	ResourceType             string                          `yaml:"boskos_resource_type"`
	ResourceNamePrefix       string                          `yaml:"resource_name_prefix"`
	InfrastructureIdentities *InfrastructureIdentitiesConfig `yaml:"infrastructure_identities,omitempty"`
}

// AssetInventory is derived from the whole catalog, never a filtered command selection.
type AssetInventory struct {
	Pool             AssetPool
	Capacity         int
	SubscriptionName string
}

type ResolvedInfrastructureIdentities struct {
	Allocation     Allocation `yaml:"allocation"`
	ResourceGroups []string   `yaml:"resource_groups"`
	Identities     []string   `yaml:"identities"`
}

func (p Pool) Requirements() []AssetRequirement {
	var requirements []AssetRequirement
	if p.SlotAssets.E2EIdentities != nil || p.IdentityContainerCount > 0 {
		requirements = append(requirements, AssetRequirement{Kind: KindE2EIdentities, Allocation: AllocationDedicated, UnitsPerSlot: 1})
	}
	if asset := p.SlotAssets.InfrastructureIdentities; asset != nil {
		units := asset.UnitsPerSlot
		if units == 0 {
			units = 1
		}
		requirements = append(requirements, AssetRequirement{Kind: KindInfrastructureIdentities, Allocation: asset.Allocation, AssetPool: asset.AssetPool, UnitsPerSlot: units})
	}
	return requirements
}

func (s ExpandedSlot) ValidateResolvedAssets() error {
	for _, requirement := range s.Requirements {
		switch requirement.Kind {
		case KindE2EIdentities:
			if s.Assets.E2EIdentities == nil || len(s.Assets.E2EIdentities.ResourceGroups) == 0 {
				return fmt.Errorf("unresolved demanded asset %q", requirement.Kind)
			}
		case KindInfrastructureIdentities:
			asset := s.Assets.InfrastructureIdentities
			if asset == nil || len(asset.ResourceGroups) != requirement.UnitsPerSlot || len(asset.Identities) == 0 {
				return fmt.Errorf("unresolved demanded asset %q", requirement.Kind)
			}
		default:
			return fmt.Errorf("unknown demanded asset %q", requirement.Kind)
		}
	}
	return nil
}

func (s ExpandedSlot) RequiresInfrastructureSubscription() bool {
	for _, requirement := range s.Requirements {
		if requirement.Kind == KindInfrastructureIdentities {
			return true
		}
	}
	return false
}

func (a AssetInventory) ResourceName(index int) string {
	return fmt.Sprintf("%s-%02d", a.Pool.ResourceNamePrefix, index)
}

func (a AssetInventory) Contains(name string) bool {
	// Compare canonical formatting, not just a prefix (or a permissive integer parse).
	suffix, found := strings.CutPrefix(name, a.Pool.ResourceNamePrefix+"-")
	if !found {
		return false
	}
	index, err := strconv.Atoi(suffix)
	if err != nil {
		return false
	}
	return index >= 0 && index < a.Capacity && name == a.ResourceName(index)
}

func (c *Catalog) AssetInventories() ([]AssetInventory, error) {
	inventory := make([]AssetInventory, len(c.AssetPools))
	indexes := map[string]int{}
	for i, pool := range c.AssetPools {
		if _, found := indexes[pool.Name]; found {
			return nil, fmt.Errorf("duplicate asset pool %q", pool.Name)
		}
		indexes[pool.Name] = i
		inventory[i].Pool = pool
	}
	for _, environment := range c.EnvironmentNames() {
		for _, pool := range c.Environments[environment].Pools {
			for _, requirement := range pool.Requirements() {
				if requirement.Allocation != AllocationLeased {
					continue
				}
				i, found := indexes[requirement.AssetPool]
				if !found {
					return nil, fmt.Errorf("pool %s/%s references missing asset pool %q", environment, pool.Name, requirement.AssetPool)
				}
				asset := &inventory[i]
				if requirement.Kind != asset.Pool.Kind {
					return nil, fmt.Errorf("asset pool %q kind mismatch: %q demanded, %q declared", asset.Pool.Name, requirement.Kind, asset.Pool.Kind)
				}
				// Subscription ownership is part of the typed schema, not command orchestration.
				subscription := c.Environments[environment].DeploymentEnvironment.InfrastructureSubscription
				if strings.TrimSpace(subscription) == "" {
					return nil, fmt.Errorf("environment %q has empty deployment_environment.infrastructure_subscription for pool %q", environment, pool.Name)
				}
				if asset.SubscriptionName != "" && asset.SubscriptionName != subscription {
					return nil, fmt.Errorf("asset pool %q has incompatible consumer subscriptions %q and %q", asset.Pool.Name, asset.SubscriptionName, subscription)
				}
				asset.SubscriptionName = subscription
				if requirement.UnitsPerSlot <= 0 || pool.SlotCount <= 0 || pool.SlotCount > (math.MaxInt-asset.Capacity)/requirement.UnitsPerSlot {
					return nil, fmt.Errorf("asset pool %q capacity is invalid or overflows int", asset.Pool.Name)
				}
				asset.Capacity += pool.SlotCount * requirement.UnitsPerSlot
			}
		}
	}
	for _, asset := range inventory {
		if asset.Capacity == 0 {
			return nil, fmt.Errorf("unused asset pool %q", asset.Pool.Name)
		}
	}
	return inventory, nil
}

var inventoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func (c *Catalog) validateAssetPools(resourceTypes map[string]string) error {
	if c.Version == 1 {
		if len(c.AssetPools) > 0 {
			return fmt.Errorf("version 1 catalog must not declare asset_pools")
		}
		return nil
	}
	prefixes := map[string]string{}
	dedicatedGroups := map[string]string{}
	for _, environment := range c.EnvironmentNames() {
		for _, pool := range c.Environments[environment].Pools {
			if !inventoryName.MatchString(pool.ResourceType) {
				return fmt.Errorf("invalid resource type %q", pool.ResourceType)
			}
			prefixes[pool.ResourceType] = environment + "/" + pool.Name
			if asset := pool.SlotAssets.E2EIdentities; asset != nil {
				// Both suffixes are decimal indices without hyphens, so removing
				// the final two segments uniquely recovers the pool prefix.
				key := strings.ToLower(pool.E2ESubscriptionName() + "/" + asset.ResourceGroupPrefix)
				if previous, found := dedicatedGroups[key]; found {
					return fmt.Errorf("incompatible dedicated resource groups in pools %s and %s/%s", previous, environment, pool.Name)
				}
				dedicatedGroups[key] = environment + "/" + pool.Name
			}
		}
	}
	for i := range c.AssetPools {
		pool := &c.AssetPools[i]
		if !inventoryName.MatchString(pool.Name) || !inventoryName.MatchString(pool.ResourceType) || !inventoryName.MatchString(pool.ResourceNamePrefix) {
			return fmt.Errorf("asset pool %q requires valid name, boskos_resource_type and resource_name_prefix", pool.Name)
		}
		if pool.Kind != KindInfrastructureIdentities {
			return fmt.Errorf("asset pool %q has unknown or unsupported leased kind %q", pool.Name, pool.Kind)
		}
		if config := pool.InfrastructureIdentities; config != nil {
			names := map[string]bool{}
			for _, name := range config.IdentityNames {
				key := strings.ToLower(name)
				if !inventoryName.MatchString(name) || names[key] {
					return fmt.Errorf("asset pool %q has invalid or duplicate identity name %q", pool.Name, name)
				}
				names[key] = true
			}
		}
		if pool.Provisioning == "" {
			pool.Provisioning = AssetProvisioningManaged
		}
		if pool.Provisioning != AssetProvisioningManaged && pool.Provisioning != IdentityProvisioningUnmanaged {
			return fmt.Errorf("asset pool %q has invalid provisioning %q", pool.Name, pool.Provisioning)
		}
		if previous, found := resourceTypes[pool.ResourceType]; found {
			return fmt.Errorf("resource type %q is declared by both %s and asset pool %s", pool.ResourceType, previous, pool.Name)
		}
		resourceTypes[pool.ResourceType] = pool.Name
		if previous, found := prefixes[pool.ResourceNamePrefix]; found {
			return fmt.Errorf("duplicate resource names: prefix %q used by %s and asset pool %s", pool.ResourceNamePrefix, previous, pool.Name)
		}
		prefixes[pool.ResourceNamePrefix] = pool.Name
	}
	_, err := c.AssetInventories()
	return err
}

func rejectV2LegacyFields(data []byte) error {
	var wire struct {
		Environments map[string]struct {
			Pools []map[string]yaml.Node `yaml:"pools"`
		} `yaml:"environments"`
	}
	if err := yaml.Unmarshal(data, &wire); err != nil {
		return err
	}
	for environment, value := range wire.Environments {
		for _, pool := range value.Pools {
			for _, obsolete := range []string{"subscription_name", "identity_provisioning", "identity_provisioning_region", "identity_container_prefix", "identity_container_count"} {
				if _, found := pool[obsolete]; found {
					return fmt.Errorf("environment %q v2 pool must not declare obsolete field %q", environment, obsolete)
				}
			}
		}
	}
	return nil
}
