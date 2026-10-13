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
// allocatable capacity or node readiness. Memory is measured in bytes.
type RoleCapacity struct {
	VCPUs       int64 `json:"vcpus"`
	MemoryBytes int64 `json:"memoryBytes"`
	SwiftNICs   int64 `json:"swiftNICs"`
}

func (c RoleCapacity) String() string {
	return fmt.Sprintf("{VCPUs:%d MemoryGiB:%d SwiftNICs:%d}", c.VCPUs, c.MemoryBytes>>30, c.SwiftNICs)
}

var CapacityRoles = [...]PoolRole{PoolRoleSystem, PoolRoleInfra, PoolRoleWorker}

type CapacityByRole map[PoolRole]RoleCapacity

// CapacityAtCount computes a pool's configured resources for a node count.
// Callers validate the pool's SKU data before using it for capacity protection.
func (p Pool) CapacityAtCount(count int64) RoleCapacity {
	capacity := RoleCapacity{
		VCPUs:       count * p.Spec.VCPUs,
		MemoryBytes: count * p.Spec.MemoryBytes,
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
		if !known || pool.Spec.VCPUs <= 0 || pool.Spec.MemoryBytes <= 0 || pool.MaxCount < 0 {
			return nil, fmt.Errorf("cannot determine capacity of pool %q", pool.Name)
		}
		if pool.Role == PoolRoleWorker && pool.EnableSwift && pool.Spec.SecondaryNICs <= 0 {
			return nil, fmt.Errorf("cannot determine Swift NIC capacity of pool %q", pool.Name)
		}
		poolCapacity := pool.CapacityAtCount(int64(pool.MaxCount))
		capacity.VCPUs += poolCapacity.VCPUs
		capacity.MemoryBytes += poolCapacity.MemoryBytes
		capacity.SwiftNICs += poolCapacity.SwiftNICs
		result[pool.Role] = capacity
	}
	return result, nil
}

// EnsureMeetsBaseline rejects capacity below the supplied baseline in any
// role or resource dimension. The baseline must explicitly include every role.
func (capacity CapacityByRole) EnsureMeetsBaseline(capacityBaseline CapacityByRole) error {
	for _, role := range CapacityRoles {
		minimum, ok := capacityBaseline[role]
		if !ok {
			return fmt.Errorf("missing %s capacity baseline", role)
		}
		got := capacity[role]
		if got.VCPUs < minimum.VCPUs || got.MemoryBytes < minimum.MemoryBytes || got.SwiftNICs < minimum.SwiftNICs {
			return fmt.Errorf("%s capacity %v is below protected baseline %v", role, got, minimum)
		}
	}
	return nil
}

// ResolveEffectiveFloor uses the per-dimension minimum of current and desired
// capacity for fully allocated plans. Partial plans must preserve the entire
// current baseline. The supplied baseline is not modified.
func (desired CapacityByRole) ResolveEffectiveFloor(baseline CapacityByRole, fullyAllocated bool) (CapacityByRole, error) {
	floor := maps.Clone(baseline)
	if fullyAllocated {
		for role, capacity := range floor {
			target := desired[role]
			floor[role] = RoleCapacity{
				VCPUs:       min(capacity.VCPUs, target.VCPUs),
				MemoryBytes: min(capacity.MemoryBytes, target.MemoryBytes),
				SwiftNICs:   min(capacity.SwiftNICs, target.SwiftNICs),
			}
		}
	}
	if err := desired.EnsureMeetsBaseline(floor); err != nil {
		return nil, err
	}
	return floor, nil
}
