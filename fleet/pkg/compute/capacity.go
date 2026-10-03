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
	"slices"
	"strings"
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

// Add returns the sum of both capacities.
func (c RoleCapacity) Add(other RoleCapacity) RoleCapacity {
	return RoleCapacity{VCPUs: c.VCPUs + other.VCPUs, MemoryBytes: c.MemoryBytes + other.MemoryBytes, SwiftNICs: c.SwiftNICs + other.SwiftNICs}
}

// Sub returns c minus other.
func (c RoleCapacity) Sub(other RoleCapacity) RoleCapacity {
	return RoleCapacity{VCPUs: c.VCPUs - other.VCPUs, MemoryBytes: c.MemoryBytes - other.MemoryBytes, SwiftNICs: c.SwiftNICs - other.SwiftNICs}
}

// Min returns the per-dimension minimum of both capacities.
func (c RoleCapacity) Min(other RoleCapacity) RoleCapacity {
	return RoleCapacity{VCPUs: min(c.VCPUs, other.VCPUs), MemoryBytes: min(c.MemoryBytes, other.MemoryBytes), SwiftNICs: min(c.SwiftNICs, other.SwiftNICs)}
}

// Covers reports whether c is at least floor in every dimension.
func (c RoleCapacity) Covers(floor RoleCapacity) bool {
	return c.VCPUs >= floor.VCPUs && c.MemoryBytes >= floor.MemoryBytes && c.SwiftNICs >= floor.SwiftNICs
}

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
		result[pool.Role] = capacity.Add(pool.CapacityAtCount(int64(pool.MaxCount)))
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
		if got := capacity[role]; !got.Covers(minimum) {
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
			floor[role] = capacity.Min(desired[role])
		}
	}
	if err := desired.EnsureMeetsBaseline(floor); err != nil {
		return nil, err
	}
	return floor, nil
}

// WorkerCapacityByZone is worker capacity per availability zone. Etcd runs on
// worker pools and its zonal disks cannot move, so a zone's worker capacity
// must be protected on its own: evicted etcd pods only reschedule into their
// disk's zone.
type WorkerCapacityByZone map[string]RoleCapacity

// WorkerZoneCapacities sums the ceilings of worker pools pinned to a single
// zone, per zone. A worker pool spanning several zones is rejected: its
// capacity cannot be attributed to the zones its etcd disks live in. Zoneless
// worker pools are not zone-protected and count towards no zone. Callers
// validate capacity with PoolCapacities first.
func WorkerZoneCapacities(pools []Pool) (WorkerCapacityByZone, error) {
	result := WorkerCapacityByZone{}
	for _, pool := range pools {
		if pool.Role != PoolRoleWorker || len(pool.AvailabilityZones) == 0 {
			continue
		}
		if len(pool.AvailabilityZones) > 1 {
			return nil, fmt.Errorf("worker pool %q spans zones %s; its capacity cannot be attributed to one zone", pool.Name, strings.Join(pool.AvailabilityZones, ","))
		}
		zone := pool.AvailabilityZones[0]
		result[zone] = result[zone].Add(pool.CapacityAtCount(int64(pool.MaxCount)))
	}
	return result, nil
}

// EnsureMeetsBaseline rejects worker capacity below the supplied baseline in
// any zone or resource dimension, checking zones in order.
func (capacity WorkerCapacityByZone) EnsureMeetsBaseline(baseline WorkerCapacityByZone) error {
	for _, zone := range slices.Sorted(maps.Keys(baseline)) {
		if got := capacity[zone]; !got.Covers(baseline[zone]) {
			return fmt.Errorf("worker capacity %v in zone %s is below protected baseline %v", got, zone, baseline[zone])
		}
	}
	return nil
}

// ResolveEffectiveFloor applies CapacityByRole.ResolveEffectiveFloor per zone:
// a fully allocated plan protects the per-dimension minimum of current and
// desired capacity in every zone, a partial plan the entire current capacity.
// A desired plan without worker capacity in a zone that has it now is rejected
// either way, since the etcd disks there cannot move. The supplied baseline is
// not modified.
func (desired WorkerCapacityByZone) ResolveEffectiveFloor(baseline WorkerCapacityByZone, fullyAllocated bool) (WorkerCapacityByZone, error) {
	floor := maps.Clone(baseline)
	for _, zone := range slices.Sorted(maps.Keys(baseline)) {
		target, ok := desired[zone]
		if !ok {
			return nil, fmt.Errorf("desired plan has no worker capacity in zone %s, whose etcd disks cannot move", zone)
		}
		if fullyAllocated {
			floor[zone] = baseline[zone].Min(target)
		}
	}
	if err := desired.EnsureMeetsBaseline(floor); err != nil {
		return nil, err
	}
	return floor, nil
}
