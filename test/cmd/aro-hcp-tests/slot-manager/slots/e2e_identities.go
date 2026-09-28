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
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

const defaultContainerIndexWidth = 2

type E2EIdentitiesAsset struct {
	Allocation          Allocation `yaml:"allocation"`
	Provisioning        string     `yaml:"provisioning,omitempty"`
	ProvisioningRegion  string     `yaml:"provisioning_region,omitempty"`
	ResourceGroupPrefix string     `yaml:"resource_group_prefix"`
	ResourceGroupCount  int        `yaml:"resource_group_count"`
}

type ResolvedE2EIdentitiesAsset struct {
	Allocation         Allocation `yaml:"allocation"`
	ProvisioningRegion string     `yaml:"provisioning_region"`
	ResourceGroups     []string   `yaml:"resource_groups"`
}

func normalizeE2EIdentities(environmentName string, pool *Pool) error {
	asset := pool.SlotAssets.E2EIdentities
	if asset == nil {
		return fmt.Errorf("environment %q pool %q must declare slot_assets.e2e_identities", environmentName, pool.Name)
	}
	asset.Provisioning = strings.TrimSpace(asset.Provisioning)
	asset.ProvisioningRegion = strings.TrimSpace(asset.ProvisioningRegion)
	asset.ResourceGroupPrefix = strings.TrimSpace(asset.ResourceGroupPrefix)
	if asset.Provisioning == "" {
		asset.Provisioning = AssetProvisioningManaged
	}
	switch {
	case asset.Allocation != AllocationDedicated:
		return fmt.Errorf("pool %q e2e_identities requires allocation dedicated", pool.Name)
	case asset.Provisioning != AssetProvisioningManaged && asset.Provisioning != IdentityProvisioningUnmanaged:
		return fmt.Errorf("environment %q pool %q has invalid slot_assets.e2e_identities.provisioning %q", environmentName, pool.Name, asset.Provisioning)
	case asset.ResourceGroupPrefix == "":
		return fmt.Errorf("environment %q pool %q has empty slot_assets.e2e_identities.resource_group_prefix", environmentName, pool.Name)
	case asset.ResourceGroupCount <= 0:
		return fmt.Errorf("environment %q pool %q has invalid slot_assets.e2e_identities.resource_group_count %d", environmentName, pool.Name, asset.ResourceGroupCount)
	case pool.RegionMode == RegionModeWeighted && asset.ProvisioningRegion == "":
		return fmt.Errorf("environment %q weighted pool %q must declare slot_assets.e2e_identities.provisioning_region", environmentName, pool.Name)
	case pool.SlotCount > math.MaxInt/asset.ResourceGroupCount:
		return fmt.Errorf("environment %q pool %q dedicated identity demand overflows int", environmentName, pool.Name)
	}

	return nil
}

func (p Pool) IsUnmanaged() bool {
	return p.SlotAssets.E2EIdentities != nil && p.SlotAssets.E2EIdentities.Provisioning == IdentityProvisioningUnmanaged
}

func (p Pool) EffectiveIdentityProvisioningRegion() string {
	if asset := p.SlotAssets.E2EIdentities; asset != nil && asset.ProvisioningRegion != "" {
		return asset.ProvisioningRegion
	}
	return p.Region
}

func (s ExpandedSlot) IdentityContainerNames() []string {
	if s.Assets.E2EIdentities != nil {
		return append([]string(nil), s.Assets.E2EIdentities.ResourceGroups...)
	}
	return nil
}

func identityContainerNames(prefix string, count int) []string {
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		names = append(names, fmt.Sprintf("%s-%0*d", prefix, defaultContainerIndexWidth, i))
	}
	return names
}

func (s ExpandedSlot) validateResolvedE2EIdentities(assetRequirement AssetRequirement) error {
	asset := s.Assets.E2EIdentities
	if asset == nil || len(asset.ResourceGroups) == 0 {
		return fmt.Errorf("unresolved demanded asset %q", assetRequirement.Kind)
	}
	if assetRequirement.Allocation != AllocationDedicated || asset.Allocation != AllocationDedicated {
		return fmt.Errorf("demanded asset %q requires dedicated allocation", assetRequirement.Kind)
	}
	if s.IdentityContainerCount <= 0 || len(asset.ResourceGroups) != s.IdentityContainerCount ||
		strings.TrimSpace(s.IdentityContainerPrefix) == "" ||
		!slices.Equal(asset.ResourceGroups, identityContainerNames(s.IdentityContainerPrefix, s.IdentityContainerCount)) {
		return fmt.Errorf("resolved demanded asset %q does not match dedicated identity containers", assetRequirement.Kind)
	}
	return nil
}

// https://learn.microsoft.com/azure/azure-resource-manager/management/resource-name-rules#microsoftresources
var identityResourceGroupName = regexp.MustCompile(`^[\p{L}\p{Nd}_().-]{0,89}[\p{L}\p{Nd}_()-]$`)

func (p Pool) validateE2EIdentityResourceGroups(environment string, dedicatedGroups map[string]string) error {
	if asset := p.SlotAssets.E2EIdentities; asset != nil {
		// Check the longest generated name without expanding the inventory.
		name := fmt.Sprintf("%s-%0*d-%0*d", asset.ResourceGroupPrefix, defaultSlotIndexWidth, p.SlotCount-1, defaultContainerIndexWidth, asset.ResourceGroupCount-1)
		if !identityResourceGroupName.MatchString(name) {
			return fmt.Errorf("environment %q pool %q has invalid identity resource group name %q from resource_group_prefix: must be at most 90 characters using letters, decimal digits, underscores, hyphens, periods or parentheses", environment, p.Name, name)
		}
		// Both suffixes are decimal indices without hyphens, so removing
		// the final two segments uniquely recovers the pool prefix.
		key := strings.ToLower(p.Subscriptions.E2E + "/" + asset.ResourceGroupPrefix)
		if previous, found := dedicatedGroups[key]; found {
			return fmt.Errorf("incompatible dedicated resource groups in pools %s and %s/%s", previous, environment, p.Name)
		}
		dedicatedGroups[key] = environment + "/" + p.Name
	}
	return nil
}
