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
	"maps"

	"github.com/Azure/ARO-HCP/fleet/pkg/compute"
)

// configurationConverged is independent of action selection: no available
// action can also mean quota or a safety check is blocking progress.
func configurationConverged(desired []compute.Pool, current []PoolState) bool {
	if len(desired) == 0 || len(desired) != len(current) {
		return false
	}
	byName := make(map[string]PoolState, len(current))
	for _, pool := range current {
		byName[pool.Name] = pool
	}
	for _, pool := range desired {
		cur, exists := byName[pool.Name]
		if !exists || cur.ProvisioningState != "Succeeded" || !cur.AutoScalingEnabled ||
			cur.MaxCount != pool.MaxCount || cur.Count > pool.MaxCount || cur.Spec != pool.Spec || cur.Role != pool.Role ||
			cur.OSDiskSizeGB != pool.OSDiskSizeGB || cur.MaxPods != pool.MaxPods || cur.EnableSwift != pool.EnableSwift ||
			!taintsEqual(cur.AvailabilityZones, pool.AvailabilityZones) || !maps.Equal(cur.Labels, pool.Labels) || !taintsEqual(cur.Taints, pool.Taints) {
			return false
		}
	}
	return true
}

// allowsCapacityReduction protects the accepted transition floor of the affected
// role. A rejected candidate does not prevent the planner from considering
// other pools or growing replacement capacity. ARM/SKU data is validated by
// the controller before planning; Swift counts are the pool's configured counts.
func allowsCapacityReduction(current []PoolState, pool PoolState, newCeiling int64, capacityFloor compute.CapacityByRole) bool {
	delta := poolCeiling(pool) - newCeiling
	if delta <= 0 {
		return true
	}
	var capacity compute.RoleCapacity
	for _, cur := range current {
		if cur.Role != pool.Role {
			continue
		}
		poolCapacity := cur.CapacityAtCount(poolCeiling(cur))
		capacity.VCPUs += poolCapacity.VCPUs
		capacity.MemoryGiB += poolCapacity.MemoryGiB
		capacity.SwiftNICs += poolCapacity.SwiftNICs
	}
	reduction := pool.CapacityAtCount(delta)
	capacity.VCPUs -= reduction.VCPUs
	capacity.MemoryGiB -= reduction.MemoryGiB
	capacity.SwiftNICs -= reduction.SwiftNICs
	floor := capacityFloor[pool.Role]
	return capacity.VCPUs >= floor.VCPUs && capacity.MemoryGiB >= floor.MemoryGiB && capacity.SwiftNICs >= floor.SwiftNICs
}
