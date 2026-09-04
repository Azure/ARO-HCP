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

package nodepool

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

func TestConfigurationConverged(t *testing.T) {
	tests := []struct {
		name   string
		change func([]PoolState) []PoolState
		want   bool
	}{
		{name: "matching configuration", want: true},
		{name: "autoscaler below maximum", change: func(p []PoolState) []PoolState { p[0].Count = 1; return p }, want: true},
		{name: "missing pool", change: func(p []PoolState) []PoolState { return nil }},
		{name: "replacement overlap", change: func(p []PoolState) []PoolState { old := p[0]; old.Name = "old"; return append(p, old) }},
		{name: "wrong identity", change: func(p []PoolState) []PoolState { p[0].Name = "old"; return p }},
		{name: "quota blocked growth", change: func(p []PoolState) []PoolState { p[0].MaxCount = 2; p[0].Count = 1; return p }},
		{name: "frozen", change: func(p []PoolState) []PoolState { p[0].AutoScalingEnabled = false; return p }},
		{name: "failed", change: func(p []PoolState) []PoolState { p[0].ProvisioningState = "Failed"; return p }},
		{name: "updating", change: func(p []PoolState) []PoolState { p[0].ProvisioningState = "Updating"; return p }},
		{name: "unknown provisioning state", change: func(p []PoolState) []PoolState { p[0].ProvisioningState = ""; return p }},
		{name: "label drift", change: func(p []PoolState) []PoolState { p[0].Labels = map[string]string{"drift": "true"}; return p }},
		{name: "taint drift", change: func(p []PoolState) []PoolState { p[0].Taints = []string{"drift=true:NoSchedule"}; return p }},
		{name: "zone drift", change: func(p []PoolState) []PoolState { p[0].AvailabilityZones = []string{"2"}; return p }},
		{name: "disk drift", change: func(p []PoolState) []PoolState { p[0].OSDiskSizeGB = 64; return p }},
		{name: "node count exceeds target", change: func(p []PoolState) []PoolState { p[0].Count = 4; return p }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desired := []compute.Pool{{Name: "new", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Size: "sku", VCPUs: 4, MemoryGiB: 16}, MaxCount: 3, OSDiskSizeGB: 32, AvailabilityZones: []string{"1"}}}
			current := []PoolState{{Pool: desired[0], ProvisioningState: "Succeeded", AutoScalingEnabled: true, Count: 3}}
			if test.change != nil {
				current = test.change(current)
			}
			require.Equal(t, test.want, configurationConverged(desired, current))
		})
	}
}

func TestAllowsCapacityReduction(t *testing.T) {
	tests := []struct {
		name    string
		ceiling int32
		count   int32
		next    int64
		overlap bool
		floor   compute.RoleCapacity
		want    bool
	}{
		{name: "CPU floor", ceiling: 10, count: 5, next: 9, floor: compute.RoleCapacity{VCPUs: 40}},
		{name: "memory floor", ceiling: 10, count: 5, next: 9, floor: compute.RoleCapacity{MemoryGiB: 160}},
		{name: "NIC floor", ceiling: 10, count: 5, next: 9, floor: compute.RoleCapacity{SwiftNICs: 20}},
		{name: "freeze loses reserved capacity", ceiling: 10, count: 5, next: 5, floor: compute.RoleCapacity{VCPUs: 40}},
		{name: "delete loses capacity", ceiling: 10, count: 10, next: 0, floor: compute.RoleCapacity{VCPUs: 40}},
		{name: "halfway replacement can drain overlap", ceiling: 10, count: 10, next: 5, overlap: true, floor: compute.RoleCapacity{VCPUs: 40, MemoryGiB: 160, SwiftNICs: 20}, want: true},
		{name: "overlap cannot drain below baseline", ceiling: 10, count: 10, next: 4, overlap: true, floor: compute.RoleCapacity{VCPUs: 40, MemoryGiB: 160, SwiftNICs: 20}},
		{name: "growth can recover below baseline", ceiling: 5, count: 5, next: 8, floor: compute.RoleCapacity{VCPUs: 40}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := []PoolState{{Pool: compute.Pool{Name: "old", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 2}, MaxCount: test.ceiling, EnableSwift: true}, Count: test.count, AutoScalingEnabled: true}}
			if test.overlap {
				current = append(current, PoolState{Pool: compute.Pool{Name: "new", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 2}, MaxCount: 5, EnableSwift: true}, Count: 1, AutoScalingEnabled: true})
			}
			baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: test.floor}
			require.Equal(t, test.want, allowsCapacityReduction(current, current[0], test.next, baseline))
			require.Equal(t, test.ceiling, current[0].MaxCount)
			require.Equal(t, test.count, current[0].Count)
		})
	}
}

func TestFindNextActionSkipsUnsafeCorrections(t *testing.T) {
	tests := []struct {
		name            string
		count           int32
		autoscaling     bool
		headroom        int64
		laterCorrection bool
		wantType        string
		wantPool        string
	}{
		{name: "blocked bounds correction permits replacement", count: 5, autoscaling: true, headroom: 100, wantType: "Create", wantPool: "new"},
		{name: "blocked freeze permits replacement", count: 8, autoscaling: true, headroom: 100, wantType: "Create", wantPool: "new"},
		{name: "blocked drain permits replacement", count: 10, autoscaling: false, headroom: 100, wantType: "Create", wantPool: "new"},
		{name: "blocked correction permits later correction", count: 5, autoscaling: true, headroom: 100, laterCorrection: true, wantType: "SetScalingBounds", wantPool: "infra"},
		{name: "no quota and no safe reduction", count: 5, autoscaling: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			old := compute.Pool{Name: "old", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Size: "sku", Family: "family", VCPUs: 4, MemoryGiB: 16}, MaxCount: 5}
			replacement := old
			replacement.Name = "new"
			desired := []compute.Pool{old, replacement}
			old.MaxCount = 10
			current := []PoolState{{Pool: old, Count: test.count, AutoScalingEnabled: test.autoscaling, ProvisioningState: "Succeeded"}}
			if test.laterCorrection {
				infra := compute.Pool{Name: "infra", Role: compute.PoolRoleInfra, Spec: compute.VMSpec{Size: "infraSKU", Family: "infraFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 1}
				desired = append(desired, infra)
				infra.MaxCount = 2
				current = append(current, PoolState{Pool: infra, Count: 1, AutoScalingEnabled: true, ProvisioningState: "Succeeded"})
			}
			baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 40, MemoryGiB: 160}}
			action := findNextAction(desired, current, map[compute.VMFamily]int64{"family": test.headroom}, baseline, compute.NetworkConfig{})
			if len(test.wantType) == 0 {
				require.Nil(t, action)
				return
			}
			require.NotNil(t, action)
			require.Equal(t, test.wantPool, action.poolName())
			require.Equal(t, test.wantType, string(action.kind()))
		})
	}
}

func TestProtectedReplacement(t *testing.T) {
	tests := []struct {
		name       string
		available  int64
		desiredMax int32
		converged  bool
	}{
		{name: "spare_quota", available: 40, desiredMax: 10, converged: true},
		{name: "no_spare_quota", available: 20, desiredMax: 10},
		{name: "growing_replacement", available: 60, desiredMax: 20, converged: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desired := []compute.Pool{{Name: "new", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Size: "sku", Family: "family", VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 2}, MaxCount: test.desiredMax, EnableSwift: true}}
			old := desired[0]
			old.Name = "old"
			old.MaxCount = 10
			current := []PoolState{{Pool: old, Count: 5, AutoScalingEnabled: true, ProvisioningState: "Succeeded"}}
			baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 40, MemoryGiB: 160, SwiftNICs: 20}}
			tr := simulateAndTrace(t, desired, current, map[compute.VMFamily]int64{"family": test.available}, baseline, true, 30)
			require.NoError(t, tr.RejectedPlan)
			require.Equal(t, test.converged, configurationConverged(desired, tr.finalState()))
			compareGolden(t, formatTrace(tr))
		})
	}
}

func TestFindNextActionSkipsUnsafeShrinks(t *testing.T) {
	tests := []struct {
		name     string
		desired  []compute.Pool
		current  []PoolState
		baseline compute.CapacityByRole
		wantType actionType
		wantPool string
	}{
		{
			name: "blocked worker squeeze permits infra squeeze",
			desired: []compute.Pool{
				{Name: "new-worker", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "workerFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 10},
				{Name: "new-infra", Role: compute.PoolRoleInfra, Spec: compute.VMSpec{Family: "infraFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 1},
			},
			current: []PoolState{
				{Pool: compute.Pool{Name: "a-worker", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "workerFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 10}, Count: 5, AutoScalingEnabled: true},
				{Pool: compute.Pool{Name: "z-infra", Role: compute.PoolRoleInfra, Spec: compute.VMSpec{Family: "infraFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 2}, Count: 1, AutoScalingEnabled: true},
			},
			baseline: compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {VCPUs: 4, MemoryGiB: 16}, compute.PoolRoleWorker: {VCPUs: 40, MemoryGiB: 160}},
			wantType: actionSetScalingBounds, wantPool: "z-infra",
		},
		{
			name: "blocked system deletion permits worker squeeze",
			desired: []compute.Pool{
				{Name: "keep-system", Role: compute.PoolRoleSystem, Spec: compute.VMSpec{Family: "systemFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 3},
				{Name: "new-worker", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "workerFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 1},
			},
			current: []PoolState{
				{Pool: compute.Pool{Name: "keep-system", Role: compute.PoolRoleSystem, Spec: compute.VMSpec{Family: "systemFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 1}, Count: 1, AutoScalingEnabled: true},
				{Pool: compute.Pool{Name: "old-system", Role: compute.PoolRoleSystem, Spec: compute.VMSpec{Family: "systemFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 3}, Count: 3, AutoScalingEnabled: true},
				{Pool: compute.Pool{Name: "old-worker", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "workerFamily", VCPUs: 4, MemoryGiB: 16}, MaxCount: 5}, Count: 1, AutoScalingEnabled: true},
			},
			baseline: compute.CapacityByRole{compute.PoolRoleSystem: {VCPUs: 12, MemoryGiB: 48}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 4, MemoryGiB: 16}},
			wantType: actionSetScalingBounds, wantPool: "old-worker",
		},
		{
			name:    "blocked high memory drain permits low memory drain",
			desired: []compute.Pool{{Name: "new", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "family", VCPUs: 4, MemoryGiB: 32}, MaxCount: 1}},
			current: []PoolState{
				{Pool: compute.Pool{Name: "a-high-memory", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "family", VCPUs: 4, MemoryGiB: 32}, MaxCount: 1}, Count: 1},
				{Pool: compute.Pool{Name: "b-low-memory", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Family: "family", VCPUs: 4, MemoryGiB: 16}, MaxCount: 1}, Count: 1},
			},
			baseline: compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: {VCPUs: 4, MemoryGiB: 32}},
			wantType: actionReduce, wantPool: "b-low-memory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desiredCapacity, err := compute.PoolCapacities(test.desired)
			require.NoError(t, err)
			require.NoError(t, desiredCapacity.ValidateAgainstBaseline(test.baseline))
			action := findNextAction(test.desired, test.current, map[compute.VMFamily]int64{}, test.baseline, compute.NetworkConfig{})
			require.NotNil(t, action)
			require.Equal(t, test.wantType, action.kind())
			require.Equal(t, test.wantPool, action.poolName())
		})
	}
}

// Capacity in another VM family may cover a role's baseline while a desired
// pool in the old family is still below target. Family quota remains separate.
func TestFindNextActionDrainUsesBaseline(t *testing.T) {
	tests := []struct {
		name      string
		baseline  compute.RoleCapacity
		wantDrain bool
	}{
		{name: "other family covers all resources", baseline: compute.RoleCapacity{VCPUs: 40, MemoryGiB: 160, SwiftNICs: 20}, wantDrain: true},
		{name: "CPU baseline blocks", baseline: compute.RoleCapacity{VCPUs: 60, MemoryGiB: 160, SwiftNICs: 20}},
		{name: "memory baseline blocks", baseline: compute.RoleCapacity{VCPUs: 40, MemoryGiB: 240, SwiftNICs: 20}},
		{name: "NIC baseline blocks", baseline: compute.RoleCapacity{VCPUs: 40, MemoryGiB: 160, SwiftNICs: 30}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			desired := []compute.Pool{
				{Name: "new-a", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Size: "sku-a", Family: "family-a", VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 2}, MaxCount: 10, EnableSwift: true},
				{Name: "new-b", Role: compute.PoolRoleWorker, Spec: compute.VMSpec{Size: "sku-b", Family: "family-b", VCPUs: 4, MemoryGiB: 16, SecondaryNICs: 2}, MaxCount: 10, EnableSwift: true},
			}
			old := desired[0]
			old.Name = "old-a"
			current := []PoolState{
				{Pool: old, Count: 10, ProvisioningState: "Succeeded"},
				{Pool: desired[1], Count: 1, AutoScalingEnabled: true, ProvisioningState: "Succeeded"},
			}
			current[1].MaxCount = 5
			baseline := compute.CapacityByRole{compute.PoolRoleSystem: {}, compute.PoolRoleInfra: {}, compute.PoolRoleWorker: test.baseline}
			desiredCapacity, err := compute.PoolCapacities(desired)
			require.NoError(t, err)
			require.NoError(t, desiredCapacity.ValidateAgainstBaseline(baseline))
			// The new family's four unused slots consume all its available quota.
			action := findNextAction(desired, current, map[compute.VMFamily]int64{"family-a": 0, "family-b": 16}, baseline, compute.NetworkConfig{})
			if !test.wantDrain {
				require.Nil(t, action)
				return
			}
			requireAction(t, newReduceAction("old-a", "sku-a", "", "", 9), action)
			capacity := stateCapacity(t, applyAction(current, action))
			require.NoError(t, capacity.ValidateAgainstBaseline(baseline))
			require.Equal(t, compute.RoleCapacity{VCPUs: 56, MemoryGiB: 224, SwiftNICs: 28}, capacity[compute.PoolRoleWorker])
		})
	}
}
