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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
)

const minCoresPerTier = 4

// tierNameRegex constrains a tier's symbolic name to a leading lowercase letter
// followed by up to four lowercase alphanumerics. The 5-char cap keeps pool
// names (<name><zone><hash6>) within the AKS 12-char agent pool name limit.
var tierNameRegex = regexp.MustCompile(`^[a-z][a-z0-9]{0,4}$`)

// dFamilyPriority and eFamilyPriority are the standard family fallback orders
// for system/infra pools (D-series) and worker pools (E-series), from newest
// to oldest generation.
var (
	dFamilyPriority = []VMFamily{"StandardDdsv7Family", "StandardDdsv6Family", "standardDDSv5Family", "standardDDSv4Family", "standardDSv3Family"}
	eFamilyPriority = []VMFamily{"StandardEdsv7Family", "StandardEdsv6Family", "standardEDSv5Family", "standardEDSv4Family", "standardESv3Family"}
)

// PoolClass defines the role, scheduling, and AKS networking policy shared by tiers.
// Role is a capacity-grouping label, independent of the provider settings.
type PoolClass struct {
	Role          PoolRole
	AgentPoolMode armcontainerservice.AgentPoolMode
	EnableSwift   bool
	// AttachSecondaryNICs attaches the SKU's maximum secondary NIC count.
	// It requires EnableSwift; Swift itself does not require attachments.
	AttachSecondaryNICs bool
	// Labels holds extra node labels. RoleLabel is derived from Role and must
	// not be set here.
	Labels map[string]string
	Taints []string
}

// Standard pool classes are copied into tier definitions. A tier selects a
// complete class rather than overriding its individual policy fields.
var (
	SystemPools = PoolClass{
		Role:          PoolRoleSystem,
		AgentPoolMode: armcontainerservice.AgentPoolModeSystem,
		Taints:        []string{TaintCriticalAddonsOnly},
		// Swift requires system pools to be swift enabled (pool annotations etc) ...
		EnableSwift: true,
		// ... but we don't schedule anything on the system pools that needs the NICs
		AttachSecondaryNICs: false,
	}
	InfraPools = PoolClass{
		Role:          PoolRoleInfra,
		AgentPoolMode: armcontainerservice.AgentPoolModeUser,
		Taints:        []string{TaintInfra},
	}
	WorkerPools = PoolClass{
		Role:                PoolRoleWorker,
		AgentPoolMode:       armcontainerservice.AgentPoolModeUser,
		EnableSwift:         true,
		AttachSecondaryNICs: true,
	}
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
	Class           PoolClass
	PoolMode        PoolMode
	Cores           int64
	OSDiskSizeGB    int32
	MaxNodes        int64
	InitialMinNodes int64
	FamilyPriority  []VMFamily
	MaxPods         int32
	Required        bool
	// PoolCount is the number of zones a PoolModePerZone tier spans and must be
	// >= 1 (clamped to the number of zones available). The tier uses the zones
	// allowing the most nodes per zone; every zone gets the same node count,
	// with one pool per family serving it. For PoolModeRegional it must be
	// exactly 1 — a single zoneless pool. Both constraints are enforced by
	// ValidateProfile.
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
	ProfileDevelopment: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Class:          SystemPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       3,
				OSDiskSizeGB:   32,
				MaxPods:        100,
				Required:       true,
			},
			{
				Name:           "inf",
				Class:          InfraPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       2,
				OSDiskSizeGB:   64,
				MaxPods:        225,
				Required:       true,
			},
			{
				Name:           "wrk",
				Class:          WorkerPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      3,
				MaxNodes:       6,
				OSDiskSizeGB:   100,
				MaxPods:        225,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	ProfileCI: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Class:          SystemPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       3,
				OSDiskSizeGB:   32,
				MaxPods:        100,
				Required:       true,
			},
			{
				Name:           "inf",
				Class:          InfraPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       3,
				OSDiskSizeGB:   64,
				MaxPods:        225,
				Required:       true,
			},
			{
				Name:            "wrk",
				Class:           WorkerPools,
				FamilyPriority:  eFamilyPriority,
				Cores:           16,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        6,
				InitialMinNodes: 5,
				OSDiskSizeGB:    512,
				MaxPods:         225,
			},
		},
		BudgetStrategy: UnlimitedBudget,
	},
	// ProfileIntegration keeps each management cluster within the capacity it
	// runs today: integration management clusters share one subscription's
	// quota, which leaves no room for the production tiers.
	ProfileIntegration: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Class:          SystemPools,
				FamilyPriority: eFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       2,
				OSDiskSizeGB:   128,
				MaxPods:        100,
				Required:       true,
			},
			{
				Name:           "inf",
				Class:          InfraPools,
				FamilyPriority: dFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       3,
				OSDiskSizeGB:   128,
				MaxPods:        225,
				Required:       true,
			},
			{
				Name:            "wrk",
				Class:           WorkerPools,
				FamilyPriority:  eFamilyPriority,
				Cores:           16,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        14,
				InitialMinNodes: 5,
				OSDiskSizeGB:    512,
				MaxPods:         225,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	},
	ProfileProduction: {
		Tiers: []TierConfig{
			{
				Name:           "sys",
				Class:          SystemPools,
				FamilyPriority: eFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModeRegional,
				PoolCount:      1,
				MaxNodes:       2,
				OSDiskSizeGB:   128,
				MaxPods:        100,
				Required:       true,
			},
			{
				Name:           "inf",
				Class:          InfraPools,
				FamilyPriority: eFamilyPriority,
				Cores:          4,
				PoolMode:       PoolModePerZone,
				PoolCount:      2,
				MaxNodes:       2,
				OSDiskSizeGB:   128,
				MaxPods:        225,
				Required:       true,
			},
			{
				Name:            "wrk16",
				Class:           WorkerPools,
				FamilyPriority:  eFamilyPriority,
				Cores:           16,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        20,
				InitialMinNodes: 5,
				OSDiskSizeGB:    400,
				MaxPods:         225,
			},
			{
				Name:            "wrk32",
				Class:           WorkerPools,
				FamilyPriority:  eFamilyPriority,
				Cores:           32,
				PoolMode:        PoolModePerZone,
				PoolCount:       3,
				MaxNodes:        3,
				InitialMinNodes: 1,
				OSDiskSizeGB:    512,
				MaxPods:         225,
			},
		},
		BudgetStrategy: SubscriptionQuotaBudget,
	},
}

// ValidateProfile checks each tier's identity, independent AKS and networking
// policies, core and node bounds, family priorities, and zonal pool count.
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
		if len(tier.Class.Role) == 0 {
			return fmt.Errorf("tier %d: role must not be empty", i)
		}
		switch tier.Class.AgentPoolMode {
		case armcontainerservice.AgentPoolModeUser, armcontainerservice.AgentPoolModeSystem:
		default:
			return fmt.Errorf("tier %d: unknown agent pool mode %q", i, tier.Class.AgentPoolMode)
		}
		if tier.Class.AttachSecondaryNICs && !tier.Class.EnableSwift {
			return fmt.Errorf("tier %d: attaching secondary NICs requires Swift", i)
		}

		if tier.Cores < minCoresPerTier {
			return fmt.Errorf("tier %d: cores %d below minimum %d (smaller VMs lack multi-NIC support for Swift)", i, tier.Cores, minCoresPerTier)
		}
		if tier.InitialMinNodes > tier.MaxNodes {
			return fmt.Errorf("tier %d: initialMinNodes %d exceeds maxNodes %d", i, tier.InitialMinNodes, tier.MaxNodes)
		}
		if _, ok := tier.Class.Labels[RoleLabel]; ok {
			return fmt.Errorf("tier %d: label %q must not be set in Class.Labels; it is derived from Class.Role", i, RoleLabel)
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
