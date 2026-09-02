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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/fleet/pkg/azure/skucache"
)

type desiredPoolsResult struct {
	Pools    []Pool              `json:"pools"`
	Failures []AllocationFailure `json:"failures,omitempty"`
}

func assertGolden(t *testing.T, got string) {
	t.Helper()
	golden := filepath.Join("testdata", t.Name()+".json")

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

var (
	allZones = []string{"1", "2", "3"}

	e32dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E32ds_v6",
		Family:                   "standardEDSv6Family",
		VCPUs:                    32,
		MemoryGB:                 256,
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      1792,
		Zones:                    allZones,
	}

	e16dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E16ds_v6",
		Family:                   "standardEDSv6Family",
		VCPUs:                    16,
		MemoryGB:                 128,
		SecondaryNICs:            7,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      896,
		Zones:                    allZones,
	}

	e8dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_E8ds_v6",
		Family:                   "standardEDSv6Family",
		VCPUs:                    8,
		MemoryGB:                 64,
		SecondaryNICs:            3,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      448,
		Zones:                    allZones,
	}

	d4dsv6 = &skucache.SKUMetadata{
		Name:                     "Standard_D4ds_v6",
		Family:                   "standardDDSv6Family",
		VCPUs:                    4,
		MemoryGB:                 16,
		SecondaryNICs:            1,
		EphemeralOSDiskSupported: true,
		EphemeralDiskSizeGB:      200,
		Zones:                    allZones,
	}
)

func TestComputeDesiredPools(t *testing.T) {
	tests := []struct {
		name          string
		tiers         []TierConfig
		familyBudgets map[VMFamily]int64
		skuMetadata   map[string]*skucache.SKUMetadata
	}{
		{
			name: "single tier single family",
			tiers: []TierConfig{
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 224},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			name: "insufficient quota",
			tiers: []TierConfig{
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 10, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}},
			},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 0},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
		{
			name: "multi role production like",
			tiers: []TierConfig{
				{Role: PoolRoleSystem, PoolMode: PoolModeSpanZones, Cores: 8, OSDiskSizeGB: 128, MaxNodes: 3, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 100, Labels: map[string]string{RoleLabel: "system"}, Taints: []string{TaintCriticalAddonsOnly}},
				{Role: PoolRoleInfra, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 128, MaxNodes: 1, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "infra"}, Taints: []string{TaintInfra}},
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 16, OSDiskSizeGB: 256, MaxNodes: 2, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}, EnableSwift: true},
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 8, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}, EnableSwift: true},
			},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 1000},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E16ds_v6": e16dsv6,
				"Standard_E8ds_v6":  e8dsv6,
			},
		},
		{
			name: "family fallback",
			tiers: []TierConfig{
				{Role: PoolRoleSystem, PoolMode: PoolModeSpanZones, Cores: 4, OSDiskSizeGB: 32, MaxNodes: 3, FamilyPriority: []VMFamily{"standardEDSv6Family", "standardDDSv6Family"}, MaxPods: 100, Labels: map[string]string{RoleLabel: "system"}},
			},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 0, "standardDDSv6Family": 100},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_D4ds_v6": d4dsv6},
		},
		{
			name: "surge reservation",
			tiers: []TierConfig{
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 16, OSDiskSizeGB: 256, MaxNodes: 2, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}},
				{Role: PoolRoleWorker, PoolMode: PoolModePerZone, Cores: 32, OSDiskSizeGB: 512, MaxNodes: 8, FamilyPriority: []VMFamily{"standardEDSv6Family"}, MaxPods: 225, Labels: map[string]string{RoleLabel: "worker"}},
			},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 232},
			skuMetadata: map[string]*skucache.SKUMetadata{
				"Standard_E32ds_v6": e32dsv6,
				"Standard_E16ds_v6": e16dsv6,
			},
		},
		{
			name:          "no tiers",
			tiers:         []TierConfig{},
			familyBudgets: map[VMFamily]int64{"standardEDSv6Family": 100},
			skuMetadata:   map[string]*skucache.SKUMetadata{"Standard_E32ds_v6": e32dsv6},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skuIndex := BuildEligibleSKUIndex(test.skuMetadata, allZones)
			pools, failures := ComputeDesiredPools(logr.Discard(), test.tiers, allZones, test.familyBudgets, skuIndex)

			result := desiredPoolsResult{Pools: pools, Failures: failures}
			b, err := json.MarshalIndent(result, "", "  ")
			require.NoError(t, err)
			assertGolden(t, string(b)+"\n")
		})
	}
}

// TestPoolName pins the name format and hash algorithm: the pool identity
// derived from VMSize and OSDiskSizeGB must not change across code changes,
// since a hash change would make the controller treat existing AKS pools as
// unrecognized, creating duplicates and orphaning the originals.
func TestPoolName(t *testing.T) {
	tests := []struct {
		name         string
		role         PoolRole
		vmSize       string
		zone         int64
		osDiskSizeGB int32
		want         string
	}{
		{
			name:         "system role uses s prefix",
			role:         PoolRoleSystem,
			vmSize:       "Standard_D4ds_v6",
			zone:         0,
			osDiskSizeGB: 32,
			want:         "s096e5ae8f6f",
		},
		{
			name:         "infra role uses i prefix",
			role:         PoolRoleInfra,
			vmSize:       "Standard_E16ds_v6",
			zone:         2,
			osDiskSizeGB: 100,
			want:         "i2591d831ece",
		},
		{
			name:         "worker role uses w prefix",
			role:         PoolRoleWorker,
			vmSize:       "Standard_E16ds_v6",
			zone:         3,
			osDiskSizeGB: 64,
			want:         "w394705c5a16",
		},
		{
			name:         "unknown role falls back to w prefix",
			role:         "",
			vmSize:       "Standard_E16ds_v6",
			zone:         3,
			osDiskSizeGB: 64,
			want:         "w394705c5a16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, poolName(tt.role, tt.vmSize, tt.zone, tt.osDiskSizeGB))
		})
	}
}

// TestPoolName_SameSKUAcrossZonesSharesHashSuffix verifies that the hash
// portion of the name (used to detect identical pool specs) ignores zone, so
// per-zone pools of the same SKU/disk size are recognized as the same spec.
func TestPoolName_SameSKUAcrossZonesSharesHashSuffix(t *testing.T) {
	zone1 := poolName(PoolRoleWorker, "Standard_E16ds_v6", 1, 100)
	zone2 := poolName(PoolRoleWorker, "Standard_E16ds_v6", 2, 100)

	assert.NotEqual(t, zone1, zone2, "names should differ by zone digit")
	assert.Equal(t, zone1[2:], zone2[2:], "hash suffix should be identical across zones for the same VM size and disk size")
}
