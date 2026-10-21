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
	"maps"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/resource"
)

func memoryBytes(value string) int64 {
	quantity := resource.MustParse(value)
	return quantity.Value()
}

func TestPoolCapacities(t *testing.T) {
	tests := []struct {
		name    string
		pools   []Pool
		want    CapacityByRole
		wantErr bool
	}{
		{name: "empty", want: CapacityByRole{}},
		{
			name: "custom roles use configured NICs rather than SKU maximum",
			pools: []Pool{
				{Name: "a", Role: "database", Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30, SecondaryNICs: 7}, MaxCount: 2, EnableSwift: true, SecondaryNICs: 2},
				{Name: "b", Role: "database", Spec: VMSpec{VCPUs: 8, MemoryBytes: 32 << 30, SecondaryNICs: 7}, MaxCount: 1, EnableSwift: true},
				{Name: "c", Role: "ingress", Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30}, MaxCount: 1},
			},
			want: CapacityByRole{"database": {16, 64 << 30, 4}, "ingress": {4, 16 << 30, 0}},
		},
		{name: "unknown SKU", pools: []Pool{{Name: "unknown", Role: "custom", MaxCount: 3}}, wantErr: true},
		{name: "negative ceiling", pools: []Pool{{Role: "custom", Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30}, MaxCount: -1}}, wantErr: true},
		{name: "negative configured NICs", pools: []Pool{{Role: "custom", Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30}, EnableSwift: true, SecondaryNICs: -1}}, wantErr: true},
		{name: "NICs without Swift", pools: []Pool{{Role: "custom", Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30}, SecondaryNICs: 1}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PoolCapacities(test.pools)
			if test.wantErr {
				require.Error(t, err)
				require.Nil(t, got)
				_, err = PoolZoneCapacities(test.pools)
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestPoolCapacitiesGolden(t *testing.T) {
	pools := []Pool{
		{Name: "sys", Role: PoolRoleSystem, Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 3}, MaxCount: 3, EnableSwift: true},
		{Name: "infra", Role: PoolRoleInfra, Spec: VMSpec{VCPUs: 8, MemoryBytes: memoryBytes("32Gi")}, MaxCount: 2},
		{Name: "old", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 16, MemoryBytes: memoryBytes("128Gi"), SecondaryNICs: 7}, MaxCount: 5, EnableSwift: true, SecondaryNICs: 7},
		{Name: "new", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 32, MemoryBytes: memoryBytes("256Gi"), SecondaryNICs: 7}, MaxCount: 2, EnableSwift: true, SecondaryNICs: 7},
		{Name: "plain", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 4, MemoryBytes: memoryBytes("16Gi"), SecondaryNICs: 3}, MaxCount: 1},
	}
	got, err := PoolCapacities(pools)
	require.NoError(t, err)
	actual, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	expected, err := os.ReadFile("testdata/capacity-roles.json")
	require.NoError(t, err)
	require.Equal(t, string(expected), string(actual)+"\n")
}

func TestPoolZoneCapacities(t *testing.T) {
	spec := VMSpec{VCPUs: 4, MemoryBytes: 16 << 30, SecondaryNICs: 7}
	pools := []Pool{
		{Name: "a", Role: "database", Spec: spec, AvailabilityZones: []string{"1"}, MaxCount: 2, EnableSwift: true, SecondaryNICs: 2},
		{Name: "b", Role: "database", Spec: spec, AvailabilityZones: []string{"1"}, MaxCount: 1, EnableSwift: true, SecondaryNICs: 1},
		{Name: "c", Role: "database", Spec: spec, AvailabilityZones: []string{"2"}, MaxCount: 1},
		{Name: "d", Role: "ingress", Spec: spec, AvailabilityZones: []string{"1"}, MaxCount: 3},
		{Name: "e", Role: "database", Spec: spec, MaxCount: 2, EnableSwift: true, SecondaryNICs: 1},
	}
	got, err := PoolZoneCapacities(pools)
	require.NoError(t, err)
	require.Equal(t, CapacityByRoleZone{
		{Role: "database", Zone: "1"}: {12, 48 << 30, 5},
		{Role: "database", Zone: "2"}: {4, 16 << 30, 0},
		{Role: "ingress", Zone: "1"}:  {12, 48 << 30, 0},
		{Role: "database"}:            {8, 32 << 30, 2},
	}, got)
}

func TestPoolZoneCapacitiesFoldsUnattributableZonesIntoNonZonalBucket(t *testing.T) {
	for _, role := range []PoolRole{PoolRoleSystem, PoolRoleInfra, PoolRoleWorker, "custom"} {
		t.Run(string(role), func(t *testing.T) {
			pools := []Pool{{Name: "spread", Role: role, Spec: VMSpec{VCPUs: 4, MemoryBytes: 16 << 30}, AvailabilityZones: []string{"1", "2"}, MaxCount: 3}}
			got, err := PoolZoneCapacities(pools)
			require.NoError(t, err)
			require.Equal(t, CapacityByRoleZone{{Role: role}: {VCPUs: 12, MemoryBytes: 48 << 30}}, got)
		})
	}
}

func TestCapacityByRoleZoneKeys(t *testing.T) {
	capacity := CapacityByRoleZone{
		{Role: "ingress", Zone: "1"}:  {},
		{Role: "database", Zone: "2"}: {},
		{Role: "database"}:            {},
		{Role: "database", Zone: "1"}: {},
	}
	require.Equal(t, []RoleZone{
		{Role: "database"},
		{Role: "database", Zone: "1"},
		{Role: "database", Zone: "2"},
		{Role: "ingress", Zone: "1"},
	}, capacity.Keys())
}

func TestCapacityByRoleZoneResolveEffectiveFloor(t *testing.T) {
	tests := []struct {
		name           string
		desired        CapacityByRoleZone
		fullyAllocated bool
		want           CapacityByRoleZone
		wantErr        bool
	}{
		{
			name:           "full plan protects per-resource minimum in non-zonal and zonal buckets",
			desired:        CapacityByRoleZone{{Role: "custom"}: {80, 900, 50}, {Role: "custom", Zone: "1"}: {120, 600, 30}},
			fullyAllocated: true,
			want:           CapacityByRoleZone{{Role: "custom"}: {80, 800, 40}, {Role: "custom", Zone: "1"}: {100, 600, 30}},
		},
		{
			name:    "partial growth preserves baseline",
			desired: CapacityByRoleZone{{Role: "custom"}: {120, 900, 50}, {Role: "custom", Zone: "1"}: {100, 800, 40}},
			want:    CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}, {Role: "custom", Zone: "1"}: {100, 800, 40}},
		},
		{
			name:    "partial plan cannot transfer capacity between zones",
			desired: CapacityByRoleZone{{Role: "custom"}: {150, 1200, 60}, {Role: "custom", Zone: "1"}: {50, 400, 20}},
			wantErr: true,
		},
		{
			name:           "full plan cannot remove non-zonal bucket",
			desired:        CapacityByRoleZone{{Role: "custom", Zone: "1"}: {200, 1600, 80}},
			fullyAllocated: true,
			wantErr:        true,
		},
		{
			name:           "full plan cannot remove zonal bucket",
			desired:        CapacityByRoleZone{{Role: "custom"}: {200, 1600, 80}},
			fullyAllocated: true,
			wantErr:        true,
		},
		{
			name:    "partial plan cannot remove non-zonal bucket",
			desired: CapacityByRoleZone{{Role: "custom", Zone: "1"}: {200, 1600, 80}},
			wantErr: true,
		},
		{
			name:           "full plan may add role and zone",
			desired:        CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}, {Role: "custom", Zone: "1"}: {100, 800, 40}, {Role: "new", Zone: "2"}: {100, 800, 40}},
			fullyAllocated: true,
			want:           CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}, {Role: "custom", Zone: "1"}: {100, 800, 40}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}, {Role: "custom", Zone: "1"}: {100, 800, 40}}
			before := maps.Clone(baseline)
			floor, err := test.desired.ResolveEffectiveFloor(baseline, test.fullyAllocated)
			require.Equal(t, before, baseline)
			if test.wantErr {
				require.Error(t, err)
				require.Nil(t, floor)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, floor)
			floor[RoleZone{Role: "custom"}] = RoleCapacity{}
			require.Equal(t, before, baseline)
		})
	}
}

func TestCapacityByRoleZoneEnsureMeetsBaseline(t *testing.T) {
	tests := []struct {
		name     string
		capacity CapacityByRoleZone
		wantErr  bool
	}{
		{name: "equal", capacity: CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}}},
		{name: "fewer CPUs", capacity: CapacityByRoleZone{{Role: "custom"}: {99, 900, 50}}, wantErr: true},
		{name: "less memory", capacity: CapacityByRoleZone{{Role: "custom"}: {110, 799, 50}}, wantErr: true},
		{name: "fewer NICs", capacity: CapacityByRoleZone{{Role: "custom"}: {110, 900, 39}}, wantErr: true},
		{name: "other role cannot compensate", capacity: CapacityByRoleZone{{Role: "custom"}: {50, 400, 20}, {Role: "other"}: {100, 800, 40}}, wantErr: true},
		{name: "other zone cannot compensate", capacity: CapacityByRoleZone{{Role: "custom"}: {50, 400, 20}, {Role: "custom", Zone: "1"}: {100, 800, 40}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := CapacityByRoleZone{{Role: "custom"}: {100, 800, 40}}
			err := test.capacity.EnsureMeetsBaseline(baseline)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCapacityByRoleZonePreservesEmptyBuckets(t *testing.T) {
	baseline := CapacityByRoleZone{{Role: "custom"}: {}}
	desired := CapacityByRoleZone{{Role: "custom", Zone: "1"}: {100, 800, 40}}
	require.Error(t, desired.EnsureMeetsBaseline(baseline))
	for _, fullyAllocated := range []bool{false, true} {
		floor, err := desired.ResolveEffectiveFloor(baseline, fullyAllocated)
		require.Error(t, err)
		require.Nil(t, floor)
	}
}
