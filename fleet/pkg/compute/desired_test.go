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
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
)

func assertGolden(t *testing.T, got string) {
	t.Helper()
	golden := filepath.Join("testdata", t.Name()+".txt")

	if os.Getenv("UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(golden), 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file not found: %s (run with UPDATE_GOLDEN=1 to create)", golden)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("golden file mismatch (-want +got):\n%s", diff)
	}
}

// renderReport runs write against an aligning writer and returns the text.
// Each blank-line separated section aligns its columns on its own.
func renderReport(write func(w io.Writer)) string {
	var buf strings.Builder
	w := tabwriter.NewWriter(&buf, 0, 8, 2, ' ', 0)
	write(w)
	_ = w.Flush() // strings.Builder writes cannot fail.
	return buf.String()
}

// writeAllocationInputs renders what a desired-pool allocation starts from:
// zones, tiers, family quota limits, and SKUs.
func writeAllocationInputs(w io.Writer, zones []string, tiers []TierConfig, familyLimits map[VMFamily]int64, skuMetadata map[string]*skucache.SKUMetadata) {
	fmt.Fprintf(w, "zones: %s\n", strings.Join(zones, ","))

	fmt.Fprintln(w, "\ntiers:")
	if len(tiers) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, tier := range tiers {
		families := make([]string, 0, len(tier.FamilyPriority))
		for _, family := range tier.FamilyPriority {
			families = append(families, string(family))
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\tpools=%d\tcores=%d\tmaxNodes=%d\tinitialMinNodes=%d\tosDisk=%dGB\tmaxPods=%d\tswift=%t\trequired=%t\tfamilies=%s\n",
			tier.Name, tier.Role, tier.PoolMode, tier.PoolCount, tier.Cores, tier.MaxNodes, tier.InitialMinNodes,
			tier.OSDiskSizeGB, tier.MaxPods, tier.EnableSwift, tier.Required, orNone(strings.Join(families, ",")))
	}

	fmt.Fprintln(w, "\nquota limits (vCPUs):")
	if len(familyLimits) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, family := range slices.Sorted(maps.Keys(familyLimits)) {
		fmt.Fprintf(w, "  %s:\t%d\n", family, familyLimits[family])
	}

	fmt.Fprintln(w, "\nskus:")
	if len(skuMetadata) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, name := range slices.Sorted(maps.Keys(skuMetadata)) {
		sku := skuMetadata[name]
		fmt.Fprintf(w, "  %s\t%s\tcpu=%d\tmemory=%dGiB\tnics=%d\tephemeralOSDisk=%t\tephemeralDisk=%dGB\tzones=%s\n",
			name, sku.Family, sku.VCPUs, sku.MemoryBytes>>30, sku.SecondaryNICs,
			sku.EphemeralOSDiskSupported, sku.EphemeralDiskSizeGB, orNone(strings.Join(sku.Zones, ",")))
	}
}

// writeAllocationResult renders desired pools, allocation failures, and
// whether every tier reached its node target.
func writeAllocationResult(w io.Writer, pools []Pool, failures []AllocationFailure, fullyAllocated bool) {
	fmt.Fprintf(w, "\nfully allocated: %t\n", fullyAllocated)

	fmt.Fprintln(w, "\npools:")
	if len(pools) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, pool := range pools {
		zone := "regional"
		if len(pool.AvailabilityZones) > 0 {
			zone = strings.Join(pool.AvailabilityZones, ",")
		}
		labels := make([]string, 0, len(pool.Labels))
		for _, key := range slices.Sorted(maps.Keys(pool.Labels)) {
			labels = append(labels, key+"="+pool.Labels[key])
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\tcpu=%d\tmemory=%dGiB\tnics=%d\tzone=%s\tmin=%d\tmax=%d\tosDisk=%dGB\tmaxPods=%d\tswift=%t\tlabels=%s\ttaints=%s\n",
			pool.Name, pool.Role, pool.Spec.Size, pool.Spec.VCPUs, pool.Spec.MemoryBytes>>30, pool.Spec.SecondaryNICs,
			zone, pool.MinCount, pool.MaxCount, pool.OSDiskSizeGB, pool.MaxPods, pool.EnableSwift,
			orNone(strings.Join(labels, ",")), orNone(strings.Join(pool.Taints, ",")))
	}

	fmt.Fprintln(w, "\nfailures:")
	if len(failures) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, failure := range failures {
		fmt.Fprintf(w, "  %s\trequired=%t\t%s\n", failure.Reason, failure.Required, failure.Message)
	}
}

func orNone(value string) string {
	if len(value) == 0 {
		return "-"
	}
	return value
}

var (
	allZones = []string{"1", "2", "3"}

	e32dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1792,
		Zones:                    allZones,
	}

	e16dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E16ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    16,
		MemoryBytes:              memoryBytes("128Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      896,
		Zones:                    allZones,
	}

	e8dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E8ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    8,
		MemoryBytes:              memoryBytes("64Gi"),
		SecondaryNICs:            3,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      448,
		Zones:                    allZones,
	}

	d4dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_D4ds_v6",
		Family:                   "StandardDdsv6Family",
		VCPUs:                    4,
		MemoryBytes:              memoryBytes("16Gi"),
		SecondaryNICs:            1,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      200,
		Zones:                    allZones,
	}

	// e32dsv6Zone12 is a zone-restricted variant of e32dsv6, available only in
	// zones 1 and 2. It exercises per-mode zone eligibility.
	e32dsv6Zone12 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1792,
		Zones:                    []string{"1", "2"},
	}

	// e8dsv6SmallDisk is an 8-core EDSv6 SKU whose ephemeral disk (64 GB) is too
	// small to hold a 128 GB OS disk, so allocateTier skips it and falls through
	// to the next family.
	e8dsv6SmallDisk = &skucache.SKUMetadata{
		Name:                     "Standard_E8ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    8,
		MemoryBytes:              memoryBytes("64Gi"),
		SecondaryNICs:            3,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      64,
		Zones:                    allZones,
	}

	// d8dsv6 is an 8-core DDSv6 SKU with a disk large enough for a 128 GB OS
	// disk; it serves as the fallback family for e8dsv6SmallDisk.
	d8dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_D8ds_v6",
		Family:                   "StandardDdsv6Family",
		VCPUs:                    8,
		MemoryBytes:              memoryBytes("32Gi"),
		SecondaryNICs:            2,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      300,
		Zones:                    allZones,
	}

	// e32NoEphemeral is a 32-core EDSv6 SKU without ephemeral OS disk support, so
	// BuildEligibleSKUIndex excludes it and no family has an eligible SKU.
	e32NoEphemeral = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v6",
		Family:                   "StandardEdsv6Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: false,
		EphemeralDiskSizeGB:      0,
		Zones:                    allZones,
	}

	e32dsv5 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v5",
		Family:                   "standardEDSv5Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1200,
		Zones:                    allZones,
	}

	// e32dsv5Zone23 is a zone-restricted variant of e32dsv5, available only in
	// zones 2 and 3.
	e32dsv5Zone23 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v5",
		Family:                   "standardEDSv5Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1200,
		Zones:                    []string{"2", "3"},
	}

	// e32dsv5Zone1 is a zone-restricted variant of e32dsv5, available only in
	// zone 1.
	e32dsv5Zone1 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v5",
		Family:                   "standardEDSv5Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1200,
		Zones:                    []string{"1"},
	}

	// e32dsv5Zone234 is a variant of e32dsv5 for a four-zone region, available
	// in zones 2, 3, and 4.
	e32dsv5Zone234 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v5",
		Family:                   "standardEDSv5Family",
		VCPUs:                    32,
		MemoryBytes:              memoryBytes("256Gi"),
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1200,
		Zones:                    []string{"2", "3", "4"},
	}
)

func TestComputeDesiredPools(t *testing.T) {
	tests := []struct {
		name          string
		zones         []string // defaults to allZones
		tiers         []TierConfig
		familyBudgets map[VMFamily]int64
		skuMetadata   map[string]*skucache.SKUMetadata
	}{
		{
			name: "single tier single family",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			name: "insufficient quota",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 0},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			name: "regional insufficient quota",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, Cores: 8, OSDiskSizeGB: 128, MaxNodes: 3, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 100, PoolCount: 1},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 0},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E8ds_v6": e8dsv6},
		},
		{
			name: "multi role production like",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, Cores: 8, OSDiskSizeGB: 128, MaxNodes: 3, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 100, Taints: []string{TaintCriticalAddonsOnly}, PoolCount: 1},
				{Name: "inf", Role: PoolRoleInfra, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 128, MaxNodes: 1, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, Taints: []string{TaintInfra}, PoolCount: 3},
				{Name: "wrk16", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 16, OSDiskSizeGB: 256, MaxNodes: 2, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
				{Name: "wrk32", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 8, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 1000},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E16ds_v6": e16dsv6,
				"Standard_E8ds_v6":  e8dsv6,
			},
		},
		{
			name: "family fallback",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, Cores: 4, OSDiskSizeGB: 32, MaxNodes: 3, FamilyPriority: []VMFamily{"StandardEdsv6Family", "StandardDdsv6Family"}, MaxPods: 100, PoolCount: 1},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 0, "StandardDdsv6Family": 100},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_D4ds_v6": d4dsv6},
		},
		{
			name: "surge reservation",
			tiers: []TierConfig{
				{Name: "wrk16", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 16, OSDiskSizeGB: 256, MaxNodes: 2, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3},
				{Name: "wrk32", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 8, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 232},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E16ds_v6": e16dsv6,
			},
		},
		{
			name:          "no tiers",
			tiers:         []TierConfig{},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 100},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			name: "regional sets no zones and accepts zone restricted sku",
			tiers: []TierConfig{
				{Name: "ovfl", Role: PoolRoleWorker, PoolMode: PoolModeRegional, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 1, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6Zone12},
		},
		{
			name: "per zone poolcount two uses zone restricted sku",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 2, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6Zone12},
		},
		{
			name: "per zone poolcount three rejects zone restricted sku",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6Zone12},
		},
		{
			// family[0] (EDSv6) SKU has an ephemeral disk too small for the tier's
			// 128 GB OS disk, so it is skipped and allocation falls through to
			// family[1] (DDSv6), which is viable.
			name: "family fallback ephemeral disk too small",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, Cores: 8, OSDiskSizeGB: 128, MaxNodes: 3, FamilyPriority: []VMFamily{"StandardEdsv6Family", "StandardDdsv6Family"}, MaxPods: 100, PoolCount: 1},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 100, "StandardDdsv6Family": 100},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E8ds_v6": e8dsv6SmallDisk,
				"Standard_D8ds_v6": d8dsv6,
			},
		},
		{
			// Empty family priority list yields a NoEligibleFamily failure.
			name: "no eligible family",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{}, MaxPods: 225, PoolCount: 3},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			// The only family's SKU lacks ephemeral OS disk support, so the SKU
			// index excludes it and no family has an eligible SKU: NoEligibleSKU.
			name: "no eligible sku",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32NoEphemeral},
		},
		{
			// The only family's SKU supports an ephemeral OS disk that is too
			// small for the tier's 128 GB OS disk. Quota is ample, so the
			// failure is NoEligibleSKU, not InsufficientQuota.
			name: "ephemeral disk too small",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, Cores: 8, OSDiskSizeGB: 128, MaxNodes: 3, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 100, PoolCount: 1},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 10000},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E8ds_v6": e8dsv6SmallDisk},
		},
		{
			// After the 32-vCPU surge reservation EDSv6 has quota for 8 nodes and
			// EDSv5 for 5: 13 nodes, so 4 per zone. EDSv6 fills zones 1 and 2,
			// EDSv5 fills zone 3.
			name: "per zone fills zones in family priority order",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 312, "standardEDSv5Family": 192},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E32ds_v5": e32dsv5,
			},
		},
		{
			// EDSv6 has quota for 8 nodes and EDSv5 for 1: 9 nodes, so 3 per
			// zone. EDSv6 fills zones 1 and 2 and two nodes of zone 3, EDSv5
			// the last node of zone 3. The tier is short of its target of 4.
			name: "per zone family switch inside a zone",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 312, "standardEDSv5Family": 64},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E32ds_v5": e32dsv5,
			},
		},
		{
			// EDSv6 has quota for 8 nodes, 2 per zone. Quota for the remaining
			// 2 nodes cannot give every zone another node and stays unused.
			name: "per zone remainder short of every zone stays unused",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 312},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			// EDSv6 is offered in zones 1 and 2, EDSv5 in zones 2 and 3; each
			// has quota for 3 nodes after the surge reservation. Every zone pair
			// allows the full 2 nodes per zone, so the tie keeps the earliest
			// zones, 1 and 2. EDSv6 fills zone 1 and half of zone 2, EDSv5 the
			// rest of zone 2.
			name: "per zone tie keeps earliest zones",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 2, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 2, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 128, "standardEDSv5Family": 128},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6Zone12,
				"Standard_E32ds_v5": e32dsv5Zone23,
			},
		},
		{
			// EDSv6 is offered in zones 1 and 2 with quota for 2 nodes, EDSv5 in
			// zones 2 and 3 with quota for 8. Zone 1 can only get EDSv6's 2
			// nodes, so any pair with zone 1 allows 2 per zone; zones 2 and 3
			// allow 4. EDSv6 gives its 2 nodes to zone 2, EDSv5 the rest.
			name: "per zone picks zones allowing the most nodes",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 2, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 96, "standardEDSv5Family": 288},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6Zone12,
				"Standard_E32ds_v5": e32dsv5Zone23,
			},
		},
		{
			// EDSv6 is offered in zones 1 and 2 only, with quota for 8 nodes.
			// It fills zones 1 and 2; EDSv5, offered everywhere, fills zone 3.
			name: "per zone restricted preferred family fills the zones it is offered in",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 288, "standardEDSv5Family": 416},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6Zone12,
				"Standard_E32ds_v5": e32dsv5,
			},
		},
		{
			// EDSv6, offered everywhere, has quota for 2 nodes; EDSv5, offered
			// only in zone 1, for 2. Zones 2 and 3 can only get EDSv6, so every
			// zone gets 1 node. Zone 1 takes EDSv5 even though EDSv6 is
			// preferred: an EDSv6 node there would leave zone 3 empty.
			name: "per zone preferred family leaves a zone to a restricted family",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 2, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 96, "standardEDSv5Family": 96},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E32ds_v5": e32dsv5Zone1,
			},
		},
		{
			// EDSv6 is offered in zones 1 and 2, EDSv5 in zones 2 and 3, each
			// with quota for 2 nodes. Neither covers the 3 tier zones alone;
			// together they give every zone 1 node.
			name: "per zone families together cover zones none covers alone",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 1, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 96, "standardEDSv5Family": 96},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6Zone12,
				"Standard_E32ds_v5": e32dsv5Zone23,
			},
		},
		{
			// In a four-zone region EDSv6 is offered in zones 1-3 with quota for
			// 3 nodes and EDSv5 in zones 2-4 with quota for 12. Zone 1 can only
			// get EDSv6's 3 nodes, so zones 2-4 win with the full 4 per zone.
			name:  "per zone four zones picks best three",
			zones: []string{"1", "2", "3", "4"},
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family", "standardEDSv5Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 128, "standardEDSv5Family": 416},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E32ds_v5": e32dsv5Zone234,
			},
		},
		{
			// Quota covers 2 nodes after the surge reservation, fewer than the 3
			// tier zones, so no zone gets a node and the tier fails.
			name: "per zone quota for fewer nodes than zones",
			tiers: []TierConfig{
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 4, FamilyPriority: []VMFamily{"StandardEdsv6Family"}, MaxPods: 225, PoolCount: 3, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"StandardEdsv6Family": 96},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zones := test.zones
			if zones == nil {
				zones = allZones
			}
			skuIndex := BuildEligibleSKUIndex(test.skuMetadata)
			pools, failures, fullyAllocated := ComputeDesiredPools(logr.Discard(), test.tiers, zones, test.familyBudgets, skuIndex)

			assertGolden(t, renderReport(func(w io.Writer) {
				writeAllocationInputs(w, zones, test.tiers, test.familyBudgets, test.skuMetadata)
				writeAllocationResult(w, pools, failures, fullyAllocated)
			}))
		})
	}
}

// TestPoolName checks that every identity field, including role, changes the
// name. Exact hash values are covered by the desired-pool golden fixtures.
func TestPoolName(t *testing.T) {
	tests := []struct {
		name     string
		role     PoolRole
		vmSize   string
		disk     int32
		maxPods  int32
		swift    bool
		wantSame bool
	}{
		{name: "same identity", role: PoolRoleWorker, vmSize: "Standard_E16ds_v6", disk: 256, maxPods: 225, swift: true, wantSame: true},
		{name: "infra role", role: PoolRoleInfra, vmSize: "Standard_E16ds_v6", disk: 256, maxPods: 225, swift: true},
		{name: "system role", role: PoolRoleSystem, vmSize: "Standard_E16ds_v6", disk: 256, maxPods: 225, swift: true},
		{name: "VM size", role: PoolRoleWorker, vmSize: "Standard_E32ds_v6", disk: 256, maxPods: 225, swift: true},
		{name: "disk", role: PoolRoleWorker, vmSize: "Standard_E16ds_v6", disk: 512, maxPods: 225, swift: true},
		{name: "max pods", role: PoolRoleWorker, vmSize: "Standard_E16ds_v6", disk: 256, maxPods: 250, swift: true},
		{name: "Swift", role: PoolRoleWorker, vmSize: "Standard_E16ds_v6", disk: 256, maxPods: 225, swift: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := poolName("wrk16", PoolRoleWorker, "1", "Standard_E16ds_v6", 256, 225, true)
			got := poolName("wrk16", test.role, "1", test.vmSize, test.disk, test.maxPods, test.swift)
			require.Len(t, got, 12, "pool names must fit AKS's limit")
			require.Equal(t, "wrk161", got[:6])
			require.Equal(t, test.wantSame, base == got, "identity change must replace the pool")
		})
	}
}

// TestPoolName_SameSpecAcrossZonesSharesHashSuffix verifies that the hash
// portion of the name (used to detect identical pool specs) ignores zone, so
// per-zone pools of the same spec are recognized as the same spec.
func TestPoolName_SameSpecAcrossZonesSharesHashSuffix(t *testing.T) {
	zone1 := poolName("wrk", PoolRoleWorker, "1", "Standard_E16ds_v6", 100, 225, true)
	zone2 := poolName("wrk", PoolRoleWorker, "2", "Standard_E16ds_v6", 100, 225, true)

	assert.NotEqual(t, zone1, zone2, "names should differ by zone digit")
	assert.Equal(t, zone1[len(zone1)-6:], zone2[len(zone2)-6:], "hash suffix should be identical across zones for the same spec")
}

func TestSeedMinCount(t *testing.T) {
	tests := []struct {
		name     string
		minNodes int64
		maxCount int64
		want     int32
	}{
		{name: "zero means one", minNodes: 0, maxCount: 5, want: 1},
		{name: "one stays one", minNodes: 1, maxCount: 5, want: 1},
		{name: "within max preserved", minNodes: 3, maxCount: 5, want: 3},
		{name: "above max clamped", minNodes: 5, maxCount: 3, want: 3},
		{name: "equal to max", minNodes: 5, maxCount: 5, want: 5},
		{name: "clamped to single node", minNodes: 3, maxCount: 1, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, seedMinCount(tt.minNodes, tt.maxCount))
		})
	}
}

func TestComputeDesiredPoolsCompleteness(t *testing.T) {
	tests := []struct {
		name         string
		tiers        []TierConfig
		zones        []string
		limits       map[VMFamily]int64
		wantFull     bool
		wantNodes    int64
		wantFailures int
	}{
		{name: "no tiers", wantFailures: 1},
		{
			name:   "regional full",
			tiers:  []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			limits: map[VMFamily]int64{"a": 12}, wantFull: true, wantNodes: 2,
		},
		{
			name:   "regional partial without failure",
			tiers:  []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			limits: map[VMFamily]int64{"a": 8}, wantNodes: 1,
		},
		{
			name:   "regional zero allocation",
			tiers:  []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			limits: map[VMFamily]int64{"a": 4}, wantFailures: 1,
		},
		{
			name:  "zonal full",
			tiers: []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, PoolCount: 3, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			zones: []string{"1", "2", "3"}, limits: map[VMFamily]int64{"a": 28}, wantFull: true, wantNodes: 6,
		},
		{
			name:  "zonal partial without failure",
			tiers: []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, PoolCount: 3, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			zones: []string{"1", "2", "3"}, limits: map[VMFamily]int64{"a": 24}, wantNodes: 3,
		},
		{
			name:  "pool count clamped to configured zones",
			tiers: []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModePerZone, PoolCount: 3, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}}},
			zones: []string{"1", "2"}, limits: map[VMFamily]int64{"a": 20}, wantFull: true, wantNodes: 4,
		},
		{
			name:   "full across fallback families",
			tiers:  []TierConfig{{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 3, FamilyPriority: []VMFamily{"a", "b"}}},
			limits: map[VMFamily]int64{"a": 12, "b": 8}, wantFull: true, wantNodes: 3,
		},
		{
			name: "later optional tier partial",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}, Required: true},
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}},
			},
			limits: map[VMFamily]int64{"a": 16}, wantNodes: 3,
		},
		{
			name: "later optional tier fails",
			tiers: []TierConfig{
				{Name: "sys", Role: PoolRoleSystem, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}, Required: true},
				{Name: "wrk", Role: PoolRoleWorker, PoolMode: PoolModeRegional, PoolCount: 1, Cores: 4, MaxNodes: 2, FamilyPriority: []VMFamily{"a"}},
			},
			limits: map[VMFamily]int64{"a": 12}, wantNodes: 2, wantFailures: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := BuildEligibleSKUIndex(map[string]*skucache.SKUMetadata{
				"sku-a": {Name: "sku-a", Family: "a", VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 100, Zones: []string{"1", "2", "3"}},
				"sku-b": {Name: "sku-b", Family: "b", VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 100, Zones: []string{"1", "2", "3"}},
			})
			pools, failures, full := ComputeDesiredPools(logr.Discard(), test.tiers, test.zones, test.limits, index)
			require.Equal(t, test.wantFull, full)
			require.Len(t, failures, test.wantFailures)
			var nodes int64
			for _, pool := range pools {
				nodes += int64(pool.MaxCount)
			}
			require.Equal(t, test.wantNodes, nodes)
		})
	}
}

func TestBuildEligibleSKUIndex(t *testing.T) {
	tests := []struct {
		name         string
		skuMetadata  map[string]*skucache.SKUMetadata
		lookupFamily VMFamily
		lookupCores  int64
		wantVMSize   string
		wantFound    bool
	}{
		{
			name: "exact core match",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "StandardEdsv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32ds_v6",
			wantFound:    true,
		},
		{
			name: "no exact core match",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "StandardEdsv6Family",
			lookupCores:  16,
			wantFound:    false,
		},
		{
			name: "no ephemeral OS disk filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_D8ds_v5": {VCPUs: 8, Family: "standardDDSv5Family", EphemeralOSDiskSupported: false, EphemeralDiskSizeGB: 320},
			},
			lookupFamily: "standardDDSv5Family",
			lookupCores:  8,
			wantFound:    false,
		},
		{
			name: "zero ephemeral disk size filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 0},
			},
			lookupFamily: "StandardEdsv6Family",
			lookupCores:  32,
			wantFound:    false,
		},
		{
			name: "constrained vCPU SKU filtered out",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E16-4s_v3": {VCPUs: 16, Family: "standardESv3Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 256, ConstrainedVCPUs: true},
				"Standard_E16s_v3":   {VCPUs: 16, Family: "standardESv3Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 256},
			},
			lookupFamily: "standardESv3Family",
			lookupCores:  16,
			wantVMSize:   "Standard_E16s_v3",
			wantFound:    true,
		},
		{
			name: "zone restricted SKU is still indexed (zone eligibility is per-tier at allocation)",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792, Zones: []string{"1", "2"}},
			},
			lookupFamily: "StandardEdsv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32ds_v6",
			wantFound:    true,
		},
		{
			name: "deterministic selection picks lexicographically smallest",
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
				"Standard_E32as_v6": {VCPUs: 32, Family: "StandardEdsv6Family", EphemeralOSDiskSupported: true, EphemeralDiskSizeGB: 1792},
			},
			lookupFamily: "StandardEdsv6Family",
			lookupCores:  32,
			wantVMSize:   "Standard_E32as_v6",
			wantFound:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			index := BuildEligibleSKUIndex(test.skuMetadata)
			vmSize, _, found := index.Lookup(test.lookupFamily, test.lookupCores)
			assert.Equal(t, test.wantFound, found)
			assert.Equal(t, test.wantVMSize, vmSize)
		})
	}
}

func TestFamilyAllocationOrder(t *testing.T) {
	// famFull and famFull2 are offered in all three zones; famRestricted in a
	// single zone; famNone in none. famMissing has no SKU at the tier's core
	// count. All eligible SKUs sit at 8 vCPUs to match the tiers below.
	index := EligibleSKUIndex{
		"famFull":       {8: {VMSize: "full8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"1", "2", "3"}}}},
		"famFull2":      {8: {VMSize: "full2-8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"1", "2", "3"}}}},
		"famRestricted": {8: {VMSize: "restricted8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: []string{"2"}}}},
		"famNone":       {8: {VMSize: "none8", Meta: &skucache.SKUMetadata{VCPUs: 8, Zones: nil}}},
	}
	zones := []string{"1", "2", "3"}

	tests := []struct {
		name           string
		poolMode       PoolMode
		familyPriority []VMFamily
		want           []VMFamily
	}{
		{
			name:           "per-zone tier keeps the operator's priority",
			poolMode:       PoolModePerZone,
			familyPriority: []VMFamily{"famFull", "famRestricted"},
			want:           []VMFamily{"famFull", "famRestricted"},
		},
		{
			name:           "regional tier prefers the more zone-restricted family",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull"},
		},
		{
			name:           "regional tier sorts a zero-zone family last",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famNone", "famFull", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull", "famNone"},
		},
		{
			name:           "regional tier sorts a family without an eligible SKU last",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famMissing", "famRestricted"},
			want:           []VMFamily{"famRestricted", "famFull", "famMissing"},
		},
		{
			name:           "regional tier keeps operator order on equal coverage",
			poolMode:       PoolModeRegional,
			familyPriority: []VMFamily{"famFull", "famFull2"},
			want:           []VMFamily{"famFull", "famFull2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := TierConfig{
				Cores:          8,
				PoolMode:       tt.poolMode,
				FamilyPriority: tt.familyPriority,
			}
			got := familyAllocationOrder(tier, zones, index)
			assert.Equal(t, tt.want, got, "family allocation order mismatch")
		})
	}
}
