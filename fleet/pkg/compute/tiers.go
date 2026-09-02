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

package compute

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

const minCoresPerTier = 4

// PoolRole identifies the operational role of a node pool tier.
type PoolRole string

const (
	PoolRoleSystem PoolRole = "system"
	PoolRoleInfra  PoolRole = "infra"
	PoolRoleWorker PoolRole = "worker"
)

const RoleLabel = "aro-hcp.azure.com/role"

const (
	TaintCriticalAddonsOnly = "CriticalAddonsOnly=true:NoSchedule"
	TaintInfra              = "infra=true:NoSchedule"
)

// dFamilyPriority and eFamilyPriority are the standard family fallback orders
// for system/infra pools (D-series) and worker pools (E-series), from newest
// to oldest generation.
var (
	dFamilyPriority = []VMFamily{"standardDDSv6Family", "standardDDSv5Family", "standardDDSv4Family", "standardDSv3Family"}
	eFamilyPriority = []VMFamily{"standardEDSv6Family", "standardEDSv5Family", "standardESv3Family"}
)

// PoolMode controls how a tier maps to AKS agent pools.
type PoolMode string

const (
	// PoolModePerZone creates one pool per availability zone, each pinned
	// to a single zone (capped by TierConfig.PoolCount). MaxNodes is the
	// autoscaler ceiling per pool.
	PoolModePerZone PoolMode = "PerZone"

	// PoolModeSpanZones creates a single pool covering all availability
	// zones. AKS manages zone placement. MaxNodes is the autoscaler
	// ceiling for the single pool.
	PoolModeSpanZones PoolMode = "SpanZones"
)

// TierConfig defines a single node pool tier — a desired VM size class with
// its own core count, disk size, node cap, and family preference list. Tiers
// are processed in order: tier 0 allocates from the shared quota pool first,
// tier 1 fills from the remainder, and so on.
type TierConfig struct {
	Role           PoolRole
	PoolMode       PoolMode
	Cores          int64
	OSDiskSizeGB   int32
	MaxNodes       int64
	FamilyPriority []VMFamily
	MaxPods        int32
	Labels         map[string]string
	Taints         []string
	EnableSwift    bool
	Required       bool
	// PoolCount caps the number of zonal pools for PoolModePerZone at the
	// first N availability zones (matching aks/pool.bicep's poolCount). Zero
	// means one pool per zone. Ignored for PoolModeSpanZones.
	PoolCount int
}

// Profile bundles tier configuration with the budget strategy that
// governs how vCPU budgets are determined for worker pool allocation.
type Profile struct {
	Tiers          []TierConfig
	BudgetStrategy BudgetStrategy
}

const (
	ProfileCI          = "ci"
	ProfileDevelopment = "development"
	ProfileIntegration = "integration"
	ProfileProduction  = "production"
)

var profiles = map[string]Profile{
	ProfileCI: {
		Tiers: []TierConfig{
			{
				Role:           PoolRoleSystem,
				PoolMode:       PoolModeSpanZones,
				Cores:          4,
				OSDiskSizeGB:   32,
				MaxNodes:       3,
				FamilyPriority: dFamilyPriority,
				MaxPods:        100,
				Labels:         map[string]string{RoleLabel: string(PoolRoleSystem)},
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Role:           PoolRoleInfra,
				PoolMode:       PoolModePerZone,
				Cores:          4,
				OSDiskSizeGB:   32,
				MaxNodes:       1,
				FamilyPriority: dFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleInfra)},
				Taints:         []string{TaintInfra},
				PoolCount:      2,
				Required:       true,
			},
			{
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          16,
				OSDiskSizeGB:   100,
				MaxNodes:       5,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleWorker)},
				EnableSwift:    true,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	ProfileDevelopment: {
		Tiers: []TierConfig{
			{
				Role:           PoolRoleSystem,
				PoolMode:       PoolModeSpanZones,
				Cores:          4,
				OSDiskSizeGB:   32,
				MaxNodes:       4,
				FamilyPriority: dFamilyPriority,
				MaxPods:        100,
				Labels:         map[string]string{RoleLabel: string(PoolRoleSystem)},
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Role:           PoolRoleInfra,
				PoolMode:       PoolModePerZone,
				Cores:          4,
				OSDiskSizeGB:   64,
				MaxNodes:       1,
				FamilyPriority: dFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleInfra)},
				Taints:         []string{TaintInfra},
				PoolCount:      2,
				Required:       true,
			},
			{
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          4,
				OSDiskSizeGB:   100,
				MaxNodes:       2,
				FamilyPriority: dFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleWorker)},
				EnableSwift:    true,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	ProfileIntegration: {
		Tiers: []TierConfig{
			{
				Role:           PoolRoleSystem,
				PoolMode:       PoolModeSpanZones,
				Cores:          8,
				OSDiskSizeGB:   128,
				MaxNodes:       4,
				FamilyPriority: eFamilyPriority,
				MaxPods:        100,
				Labels:         map[string]string{RoleLabel: string(PoolRoleSystem)},
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Role:           PoolRoleInfra,
				PoolMode:       PoolModePerZone,
				Cores:          32,
				OSDiskSizeGB:   128,
				MaxNodes:       1,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleInfra)},
				Taints:         []string{TaintInfra},
				PoolCount:      2,
				Required:       true,
			},
			{
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          16,
				OSDiskSizeGB:   256,
				MaxNodes:       4,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleWorker)},
				EnableSwift:    true,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	},
	ProfileProduction: {
		Tiers: []TierConfig{
			{
				Role:           PoolRoleSystem,
				PoolMode:       PoolModeSpanZones,
				Cores:          8,
				OSDiskSizeGB:   128,
				MaxNodes:       4,
				FamilyPriority: eFamilyPriority,
				MaxPods:        100,
				Labels:         map[string]string{RoleLabel: string(PoolRoleSystem)},
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Role:           PoolRoleInfra,
				PoolMode:       PoolModePerZone,
				Cores:          32,
				OSDiskSizeGB:   128,
				MaxNodes:       1,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleInfra)},
				Taints:         []string{TaintInfra},
				PoolCount:      2,
				Required:       true,
			},
			{
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          16,
				OSDiskSizeGB:   256,
				MaxNodes:       4,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleWorker)},
				EnableSwift:    true,
			},
			{
				Role:           PoolRoleWorker,
				PoolMode:       PoolModePerZone,
				Cores:          32,
				OSDiskSizeGB:   512,
				MaxNodes:       7,
				FamilyPriority: eFamilyPriority,
				MaxPods:        225,
				Labels:         map[string]string{RoleLabel: string(PoolRoleWorker)},
				EnableSwift:    true,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	},
}

// ValidateProfile checks a profile's tiers for minimum core counts and
// duplicate families within a tier's FamilyPriority.
func ValidateProfile(profile Profile) error {
	for i, tier := range profile.Tiers {
		if tier.Cores < minCoresPerTier {
			return fmt.Errorf("tier %d: cores %d below minimum %d (smaller VMs lack multi-NIC support for Swift)", i, tier.Cores, minCoresPerTier)
		}
		seen := sets.New[VMFamily]()
		for _, family := range tier.FamilyPriority {
			if seen.Has(family) {
				return fmt.Errorf("tier %d: family %q appears more than once in FamilyPriority", i, family)
			}
			seen.Insert(family)
		}
	}
	return nil
}

// TierFamilies returns the deduplicated set of VM families referenced across
// all tiers.
func TierFamilies(tiers []TierConfig) sets.Set[VMFamily] {
	families := sets.New[VMFamily]()
	for _, tier := range tiers {
		families.Insert(tier.FamilyPriority...)
	}
	return families
}

// LookupProfile returns the worker pool profile for the given profile name.
func LookupProfile(name string) (Profile, bool) {
	profile, ok := profiles[name]
	return profile, ok
}

// ValidProfileNames returns the sorted list of known profile names.
func ValidProfileNames() []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
