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
	"cmp"
	"fmt"
	"maps"
	"slices"
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
	return RoleCapacity{
		VCPUs:       count * p.Spec.VCPUs,
		MemoryBytes: count * p.Spec.MemoryBytes,
		SwiftNICs:   count * p.SecondaryNICs,
	}
}

// PoolCapacities sums the ceilings of a complete desired or observed pool set.
// Callers projecting observed pools must set MaxCount to the static count when
// autoscaling is disabled. Unknown capacity must never silently count as zero.
func PoolCapacities(pools []Pool) (CapacityByRole, error) {
	result := CapacityByRole{}
	for _, pool := range pools {
		if err := validatePoolCapacity(pool); err != nil {
			return nil, err
		}
		result[pool.Role] = result[pool.Role].Add(pool.CapacityAtCount(int64(pool.MaxCount)))
	}
	return result, nil
}

func validatePoolCapacity(pool Pool) error {
	if pool.Spec.VCPUs <= 0 || pool.Spec.MemoryBytes <= 0 || pool.MaxCount < 0 {
		return fmt.Errorf("cannot determine capacity of pool %q", pool.Name)
	}
	if pool.SecondaryNICs < 0 || (pool.SecondaryNICs > 0 && !pool.EnableSwift) {
		return fmt.Errorf("invalid configured Swift NIC capacity of pool %q", pool.Name)
	}
	return nil
}

// RoleZone identifies a capacity bucket. The empty zone is the non-zonal bucket.
type RoleZone struct {
	Role PoolRole
	Zone string
}

// RoleZoneKey returns the capacity bucket a pool's ceiling belongs to: its
// single zone, or the non-zonal bucket when it has zero or several zones (a
// pool spanning several zones cannot be attributed to just one of them).
// PoolZoneCapacities and capacity-reduction checks must use this so a pool is
// always looked up under the same bucket it was summed into.
func (p Pool) RoleZoneKey() RoleZone {
	key := RoleZone{Role: p.Role}
	if len(p.AvailabilityZones) == 1 {
		key.Zone = p.AvailabilityZones[0]
	}
	return key
}

type CapacityByRoleZone map[RoleZone]RoleCapacity

// Keys returns the capacity buckets sorted by role, then zone.
func (capacity CapacityByRoleZone) Keys() []RoleZone {
	return slices.SortedFunc(maps.Keys(capacity), func(a, b RoleZone) int {
		if order := cmp.Compare(a.Role, b.Role); order != 0 {
			return order
		}
		return cmp.Compare(a.Zone, b.Zone)
	})
}

// PoolZoneCapacities sums pool ceilings per role and zone. A pool pinned to
// exactly one zone belongs to that zone's bucket; a pool with zero or several
// zones cannot be attributed to a single zone and belongs to the non-zonal
// bucket instead. A role whose desired capacity is itself zone-agnostic (see
// PoolModeRegional) only ever checks the non-zonal bucket, so folding such a
// pool's full capacity into it is exact, not a guess. A role whose desired
// capacity is zone-pinned (PoolModePerZone) still gets the zone protection it
// needs: ResolveEffectiveFloor rejects a desired plan with no capacity in a
// zone the non-zonal fold cannot satisfy.
func PoolZoneCapacities(pools []Pool) (CapacityByRoleZone, error) {
	result := CapacityByRoleZone{}
	for _, pool := range pools {
		if err := validatePoolCapacity(pool); err != nil {
			return nil, err
		}
		key := pool.RoleZoneKey()
		result[key] = result[key].Add(pool.CapacityAtCount(int64(pool.MaxCount)))
	}
	return result, nil
}

// EnsureMeetsBaseline rejects missing buckets or capacity below the supplied
// baseline in any role, zone, or resource dimension.
func (capacity CapacityByRoleZone) EnsureMeetsBaseline(baseline CapacityByRoleZone) error {
	for _, key := range baseline.Keys() {
		got, ok := capacity[key]
		if !ok {
			return fmt.Errorf("missing %s capacity in zone %q", key.Role, key.Zone)
		}
		if !got.Covers(baseline[key]) {
			return fmt.Errorf("%s capacity %v in zone %q is below protected baseline %v", key.Role, got, key.Zone, baseline[key])
		}
	}
	return nil
}

// ResolveEffectiveFloor protects the per-resource minimum of current and
// desired capacity for fully allocated plans, or the entire current capacity
// for partial plans. Every existing role-zone bucket, including the non-zonal
// bucket, must remain present. The supplied baseline is not modified.
func (desired CapacityByRoleZone) ResolveEffectiveFloor(baseline CapacityByRoleZone, fullyAllocated bool) (CapacityByRoleZone, error) {
	floor := maps.Clone(baseline)
	for _, key := range baseline.Keys() {
		target, ok := desired[key]
		if !ok {
			return nil, fmt.Errorf("desired plan has no %s capacity in zone %q", key.Role, key.Zone)
		}
		if fullyAllocated {
			floor[key] = baseline[key].Min(target)
		}
	}
	if err := desired.EnsureMeetsBaseline(floor); err != nil {
		return nil, err
	}
	return floor, nil
}
