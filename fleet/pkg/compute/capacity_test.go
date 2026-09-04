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
)

func TestPoolCapacities(t *testing.T) {
	tests := []struct {
		name    string
		pools   []Pool
		wantErr string
	}{
		{name: "empty"},
		{name: "roles", pools: []Pool{
			{Name: "sys", Role: PoolRoleSystem, Spec: VMSpec{VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 3}, MaxCount: 3, EnableSwift: true},
			{Name: "infra", Role: PoolRoleInfra, Spec: VMSpec{VCPUs: 8, MemoryGiB: 32}, MaxCount: 2},
			{Name: "old", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 16, MemoryGiB: 128, SecondaryNICs: 7}, MaxCount: 5, EnableSwift: true},
			{Name: "new", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 32, MemoryGiB: 256, SecondaryNICs: 7}, MaxCount: 2, EnableSwift: true},
			{Name: "plain", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 3}, MaxCount: 1},
		}},
		{name: "unknown SKU", pools: []Pool{{Name: "unknown", Role: PoolRoleWorker, MaxCount: 3}}, wantErr: "cannot determine capacity"},
		{name: "unknown role", pools: []Pool{{Name: "unknown", Role: "other", Spec: VMSpec{VCPUs: 4, MemoryGiB: 16}}}, wantErr: "cannot determine capacity"},
		{name: "unknown Swift capacity", pools: []Pool{{Name: "unknown", Role: PoolRoleWorker, Spec: VMSpec{VCPUs: 4, MemoryGiB: 16}, EnableSwift: true}}, wantErr: "cannot determine Swift"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PoolCapacities(test.pools)
			if len(test.wantErr) > 0 {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			actual, err := json.MarshalIndent(got, "", "  ")
			require.NoError(t, err)
			expected, err := os.ReadFile("testdata/capacity-" + test.name + ".json")
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual)+"\n")
		})
	}
}

func TestCapacityValidateAgainstBaseline(t *testing.T) {
	tests := []struct {
		name     string
		capacity RoleCapacity
		missing  bool
		wantErr  bool
	}{
		{name: "equal", capacity: RoleCapacity{100, 800, 40}},
		{name: "overlap", capacity: RoleCapacity{150, 1200, 60}},
		{name: "fewer CPUs", capacity: RoleCapacity{99, 900, 50}, wantErr: true},
		{name: "less memory", capacity: RoleCapacity{110, 799, 50}, wantErr: true},
		{name: "fewer NICs", capacity: RoleCapacity{110, 900, 39}, wantErr: true},
		{name: "tier removed", capacity: RoleCapacity{}, wantErr: true},
		{name: "missing baseline", missing: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			floor := CapacityByRole{PoolRoleSystem: {}, PoolRoleInfra: {}, PoolRoleWorker: {100, 800, 40}}
			if test.missing {
				delete(floor, PoolRoleWorker)
			}
			actual := CapacityByRole{PoolRoleSystem: {}, PoolRoleInfra: {}, PoolRoleWorker: test.capacity}
			err := actual.ValidateAgainstBaseline(floor)
			if test.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTransitionFloor(t *testing.T) {
	tests := []struct {
		name            string
		desired         RoleCapacity
		fullyAllocated  bool
		missingBaseline bool
		want            RoleCapacity
		wantErr         bool
	}{
		{name: "full CPU reduction", desired: RoleCapacity{80, 900, 50}, fullyAllocated: true, want: RoleCapacity{80, 800, 40}},
		{name: "full memory reduction", desired: RoleCapacity{120, 600, 50}, fullyAllocated: true, want: RoleCapacity{100, 600, 40}},
		{name: "full NIC reduction", desired: RoleCapacity{120, 900, 30}, fullyAllocated: true, want: RoleCapacity{100, 800, 30}},
		{name: "full role removal", fullyAllocated: true, want: RoleCapacity{}},
		{name: "full growth keeps baseline", desired: RoleCapacity{120, 900, 50}, fullyAllocated: true, want: RoleCapacity{100, 800, 40}},
		{name: "partial growth keeps baseline", desired: RoleCapacity{120, 900, 50}, want: RoleCapacity{100, 800, 40}},
		{name: "partial CPU reduction rejected", desired: RoleCapacity{80, 900, 50}, wantErr: true},
		{name: "partial memory reduction rejected", desired: RoleCapacity{120, 600, 50}, wantErr: true},
		{name: "partial NIC reduction rejected", desired: RoleCapacity{120, 900, 30}, wantErr: true},
		{name: "partial role removal rejected", wantErr: true},
		{name: "full plan still requires baseline initialization", fullyAllocated: true, missingBaseline: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := CapacityByRole{PoolRoleSystem: {4, 16, 0}, PoolRoleInfra: {8, 64, 0}, PoolRoleWorker: {100, 800, 40}}
			if test.missingBaseline {
				delete(baseline, PoolRoleWorker)
			}
			before := maps.Clone(baseline)
			desired := CapacityByRole{PoolRoleSystem: {4, 16, 0}, PoolRoleInfra: {8, 64, 0}, PoolRoleWorker: test.desired}
			floor, err := desired.TransitionFloor(baseline, test.fullyAllocated)
			require.Equal(t, before, baseline, "selecting a floor must not update the persisted baseline")
			if test.wantErr {
				require.Error(t, err)
				require.Nil(t, floor)
				return
			}
			require.NoError(t, err)
			require.Equal(t, CapacityByRole{PoolRoleSystem: {4, 16, 0}, PoolRoleInfra: {8, 64, 0}, PoolRoleWorker: test.want}, floor)
		})
	}
}
