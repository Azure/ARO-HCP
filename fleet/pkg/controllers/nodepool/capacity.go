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
			cur.MaxCount != pool.MaxCount || cur.Count > pool.MaxCount || cur.Spec.Size != pool.Spec.Size || cur.Role != pool.Role ||
			cur.OSDiskSizeGB != pool.OSDiskSizeGB || cur.MaxPods != pool.MaxPods || cur.EnableSwift != pool.EnableSwift ||
			!taintsEqual(cur.AvailabilityZones, pool.AvailabilityZones) || !maps.Equal(cur.Labels, pool.Labels) || !taintsEqual(cur.Taints, pool.Taints) {
			return false
		}
	}
	return true
}

// transitionFloor is the capacity a pool transition must preserve: per role,
// and for worker pools additionally per zone (see compute.WorkerCapacityByZone).
type transitionFloor struct {
	roles       compute.CapacityByRole
	workerZones compute.WorkerCapacityByZone
}

// allowsCapacityReduction protects the accepted transition floor of the affected
// role and, for a worker pool pinned to one zone, of that zone. A rejected
// candidate does not prevent the planner from considering other pools or
// growing replacement capacity. ARM/SKU data is validated by the controller
// before planning; Swift counts are the pool's configured counts.
func allowsCapacityReduction(current []PoolState, pool PoolState, newCeiling int64, floor transitionFloor) bool {
	delta := poolCeiling(pool) - newCeiling
	if delta <= 0 {
		return true
	}
	reduction := pool.CapacityAtCount(delta)
	var roleCapacity compute.RoleCapacity
	for _, cur := range current {
		if cur.Role == pool.Role {
			roleCapacity = roleCapacity.Add(cur.CapacityAtCount(poolCeiling(cur)))
		}
	}
	if !roleCapacity.Sub(reduction).Covers(floor.roles[pool.Role]) {
		return false
	}
	if pool.Role != compute.PoolRoleWorker || len(pool.AvailabilityZones) != 1 {
		return true
	}
	zoneCapacity, err := compute.WorkerZoneCapacities(ceilingPools(current))
	if err != nil {
		// Unattributable worker capacity: refuse rather than guess.
		return false
	}
	zone := pool.AvailabilityZones[0]
	return zoneCapacity[zone].Sub(reduction).Covers(floor.workerZones[zone])
}
