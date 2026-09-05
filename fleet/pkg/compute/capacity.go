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
	"maps"
)

// RoleCapacity is configured capacity at the pool ceilings, not observed
// allocatable capacity or node readiness. Memory uses the SKU's GiB units.
type RoleCapacity struct {
	VCPUs     int64 `json:"vcpus"`
	MemoryGiB int64 `json:"memoryGiB"`
	SwiftNICs int64 `json:"swiftNICs"`
}

var CapacityRoles = [...]PoolRole{PoolRoleSystem, PoolRoleInfra, PoolRoleWorker}

type CapacityByRole map[PoolRole]RoleCapacity

// CapacityAtCount computes a pool's configured resources for a node count.
// Callers validate the pool's SKU data before using it for capacity protection.
func (p Pool) CapacityAtCount(count int64) RoleCapacity {
	capacity := RoleCapacity{
		VCPUs:     count * p.Spec.VCPUs,
		MemoryGiB: count * p.Spec.MemoryGiB,
	}
	if p.Role == PoolRoleWorker && p.EnableSwift {
		capacity.SwiftNICs = count * p.Spec.SecondaryNICs
	}
	return capacity
}

// PoolCapacities sums the ceilings of a complete desired or observed pool set.
// Callers projecting observed pools must set MaxCount to the static count when
// autoscaling is disabled. Unknown capacity must never silently count as zero.
func PoolCapacities(pools []Pool) (CapacityByRole, error) {
	result := CapacityByRole{PoolRoleSystem: {}, PoolRoleInfra: {}, PoolRoleWorker: {}}
	for _, pool := range pools {
		capacity, known := result[pool.Role]
		if !known || pool.Spec.VCPUs <= 0 || pool.Spec.MemoryGiB <= 0 || pool.MaxCount < 0 {
			return nil, fmt.Errorf("cannot determine capacity of pool %q", pool.Name)
		}
		if pool.Role == PoolRoleWorker && pool.EnableSwift && pool.Spec.SecondaryNICs <= 0 {
			return nil, fmt.Errorf("cannot determine Swift NIC capacity of pool %q", pool.Name)
		}
		poolCapacity := pool.CapacityAtCount(int64(pool.MaxCount))
		capacity.VCPUs += poolCapacity.VCPUs
		capacity.MemoryGiB += poolCapacity.MemoryGiB
		capacity.SwiftNICs += poolCapacity.SwiftNICs
		result[pool.Role] = capacity
	}
	return result, nil
}

// ValidateAgainstBaseline rejects capacity below the persisted baseline in any
// role or resource dimension. Every role must have an initialized baseline.
func (capacity CapacityByRole) ValidateAgainstBaseline(capacityBaseline CapacityByRole) error {
	for _, role := range CapacityRoles {
		minimum, ok := capacityBaseline[role]
		if !ok {
			return fmt.Errorf("missing %s capacity baseline; run aks-cluster-create to initialize it", role)
		}
		got := capacity[role]
		if got.VCPUs < minimum.VCPUs || got.MemoryGiB < minimum.MemoryGiB || got.SwiftNICs < minimum.SwiftNICs {
			return fmt.Errorf("%s capacity %+v is below protected baseline %+v", role, got, minimum)
		}
	}
	return nil
}

// TransitionFloor allows a fully allocated configuration to lower established
// capacity. Partial plans must preserve the entire baseline. Growth never raises
// the transition floor before convergence, and the stored baseline is unchanged.
func (desired CapacityByRole) TransitionFloor(baseline CapacityByRole, fullyAllocated bool) (CapacityByRole, error) {
	floor := maps.Clone(baseline)
	if fullyAllocated {
		for role, capacity := range floor {
			target := desired[role]
			floor[role] = RoleCapacity{
				VCPUs:     min(capacity.VCPUs, target.VCPUs),
				MemoryGiB: min(capacity.MemoryGiB, target.MemoryGiB),
				SwiftNICs: min(capacity.SwiftNICs, target.SwiftNICs),
			}
		}
	}
	if err := desired.ValidateAgainstBaseline(floor); err != nil {
		return nil, err
	}
	return floor, nil
}
