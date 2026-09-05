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
	"regexp"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

const minCoresPerTier = 4

// tierNameRegex constrains a tier's symbolic name to a leading lowercase letter
// followed by up to four lowercase alphanumerics. The 5-char cap keeps pool
// names (<name><zone><hash6>) within the AKS 12-char agent pool name limit.
var tierNameRegex = regexp.MustCompile(`^[a-z][a-z0-9]{0,4}$`)

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
	dFamilyPriority = []VMFamily{"standardDDSv7Family", "standardDDSv6Family", "standardDDSv5Family", "standardDDSv4Family", "standardDSv3Family"}
	eFamilyPriority = []VMFamily{"standardEDSv7Family", "standardEDSv6Family", "standardEDSv5Family", "standardEDSv4Family", "standardESv3Family"}
)

// PoolMode controls how a tier maps to AKS agent pools.
type PoolMode string

const (
	// PoolModePerZone creates TierConfig.PoolCount pools, each pinned to a single
	// availability zone. MaxNodes is the autoscaler ceiling per pool.
	PoolModePerZone PoolMode = "PerZone"

	// PoolModeRegional creates a single pool with no availability zones set, so
	// AKS places its nodes anywhere in the region without zonal pinning. Because
	// it never requests a specific zone, it is the only mode that can use
	// zone-restricted SKUs. MaxNodes is the autoscaler ceiling for the single
	// pool.
	PoolModeRegional PoolMode = "Regional"
)

// TierConfig defines a single node pool tier — a desired VM size class with
// its own core count, disk size, node cap, and family preference list. Tiers
// are processed in order: tier 0 allocates from the shared quota pool first,
// tier 1 fills from the remainder, and so on.
type TierConfig struct {
	// Name is the tier's stable symbolic identifier. It is the leading segment
	// of every pool name derived from this tier (<Name><zone><hash>), so it
	// decouples a pool's identity from its mutable contents. It is a permanent
	// identifier: changing it renames — and therefore replaces — the tier's
	// pools. Must match ^[a-z][a-z0-9]{0,4}$ (1-5 chars) and be unique within a
	// profile; both are enforced by ValidateProfile.
	Name            string
	Role            PoolRole
	PoolMode        PoolMode
	Cores           int64
	OSDiskSizeGB    int32
	MaxNodes        int64
	InitialMinNodes int64
	FamilyPriority  []VMFamily
	MaxPods         int32
	// Labels holds extra node labels for the tier's pools. The role label
	// (RoleLabel) is derived from Role at desired-state build time and must
	// not be set here.
	Labels      map[string]string
	Taints      []string
	EnableSwift bool
	Required    bool
	// PoolCount is the number of pools the tier creates. For PoolModePerZone it
	// must be >= 1 and is the number of zonal pools (clamped to the number of
	// zones available); the pools land in the first PoolCount zones where the
	// chosen SKU is available. For PoolModeRegional it must be exactly 1 — a
	// single zoneless pool. Both constraints are enforced by ValidateProfile.
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
	ProfileProduction  = "production"
)

var profiles = map[string]Profile{
	ProfileDevelopment: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Role:           PoolRoleSystem,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       3,
				OSDiskSizeGB:   32,
				MaxPods:        100,
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Name:           "inf",
				Role:           PoolRoleInfra,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       2,
				OSDiskSizeGB:   64,
				MaxPods:        225,
				Taints:         []string{TaintInfra},
				Required:       true,
			},
			{
				Name:           "wrk",
				Role:           PoolRoleWorker,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      3,
				MaxNodes:       6,
				OSDiskSizeGB:   100,
				MaxPods:        225,
				EnableSwift:    true,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	ProfileCI: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Role:           PoolRoleSystem,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       3,
				OSDiskSizeGB:   32,
				MaxPods:        100,
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Name:           "inf",
				Role:           PoolRoleInfra,
				FamilyPriority: dFamilyPriority,
				Cores:          8,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       3,
				OSDiskSizeGB:   64,
				MaxPods:        225,
				Taints:         []string{TaintInfra},
				Required:       true,
			},
			{
				Name:            "wrk",
				Role:            PoolRoleWorker,
				FamilyPriority:  eFamilyPriority,
				Cores:           16,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        6,
				InitialMinNodes: 5,
				OSDiskSizeGB:    512,
				MaxPods:         225,
				EnableSwift:     true,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	ProfileProduction: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Role:           PoolRoleSystem,
				FamilyPriority: eFamilyPriority,
				Cores:          8,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       3,
				OSDiskSizeGB:   128,
				MaxPods:        100,
				Taints:         []string{TaintCriticalAddonsOnly},
				Required:       true,
			},
			{
				Name:           "inf",
				Role:           PoolRoleInfra,
				FamilyPriority: eFamilyPriority,
				Cores:          32,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       3,
				OSDiskSizeGB:   128,
				MaxPods:        225,
				Taints:         []string{TaintInfra},
				Required:       true,
			},
			{
				Name:            "wrk16",
				Role:            PoolRoleWorker,
				FamilyPriority:  eFamilyPriority,
				Cores:           16,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        19,
				InitialMinNodes: 5,
				OSDiskSizeGB:    512,
				MaxPods:         225,
				EnableSwift:     true,
			},
			{
				Name:            "wrk32",
				Role:            PoolRoleWorker,
				FamilyPriority:  eFamilyPriority,
				Cores:           32,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        4,
				InitialMinNodes: 1,
				OSDiskSizeGB:    512,
				MaxPods:         225,
				EnableSwift:     true,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	},
}

// ValidateProfile checks a profile's tiers for valid, unique symbolic names,
// minimum core counts, initialMinNodes within maxNodes, duplicate families
// within a tier's FamilyPriority, and a PoolCount valid for the tier's PoolMode.
func ValidateProfile(profile Profile) error {
	seenNames := sets.New[string]()
	for i, tier := range profile.Tiers {
		if !tierNameRegex.MatchString(tier.Name) {
			return fmt.Errorf("tier %d: name %q must match %s (1-5 chars, leading lowercase letter)", i, tier.Name, tierNameRegex.String())
		}
		if seenNames.Has(tier.Name) {
			return fmt.Errorf("tier %d: name %q is not unique within the profile", i, tier.Name)
		}
		seenNames.Insert(tier.Name)

		if tier.Cores < minCoresPerTier {
			return fmt.Errorf("tier %d: cores %d below minimum %d (smaller VMs lack multi-NIC support for Swift)", i, tier.Cores, minCoresPerTier)
		}
		if tier.InitialMinNodes > tier.MaxNodes {
			return fmt.Errorf("tier %d: initialMinNodes %d exceeds maxNodes %d", i, tier.InitialMinNodes, tier.MaxNodes)
		}
		if _, ok := tier.Labels[RoleLabel]; ok {
			return fmt.Errorf("tier %d: label %q must not be set in Labels; it is derived from Role", i, RoleLabel)
		}
		switch tier.PoolMode {
		case PoolModePerZone:
			if tier.PoolCount < 1 {
				return fmt.Errorf("tier %d: PoolModePerZone requires PoolCount >= 1, got %d", i, tier.PoolCount)
			}
		case PoolModeRegional:
			if tier.PoolCount != 1 {
				return fmt.Errorf("tier %d: PoolModeRegional requires PoolCount == 1, got %d", i, tier.PoolCount)
			}
		default:
			return fmt.Errorf("tier %d: unknown pool mode %q", i, tier.PoolMode)
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
