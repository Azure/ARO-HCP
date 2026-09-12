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
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

// ResolveZones computes the availability zones to plan node pools across, shared
// by the nodepool controller and the aks-cluster-create tool so both derive and
// validate zones identically.
//
// regionAvailabilityZones is the number of availability zones the region offers
// (config azureRegionAvailabilityZoneCount), trusted as authoritative. We only
// target zonal regions, so a region with no zones cannot host node pools and
// regionAvailabilityZones < 1 is always an error — there is no point planning
// anything there.
//
// explicitZones is an optional operator override (comma-separated, e.g. "1,3")
// for skipping a known-bad zone. When empty, ResolveZones returns the region's
// full zone set "1".."regionAvailabilityZones". When set, every entry must be an
// integer in [1,regionAvailabilityZones] with no duplicates; the list is
// returned normalized but in the given order, so an operator can plan across a
// subset. Entries outside the region's range (e.g. "4", or "1,3,4", in a 3-zone
// region) are rejected rather than silently reaching Azure and failing pool
// creation.
func ResolveZones(explicitZones string, regionAvailabilityZones int) ([]string, error) {
	if regionAvailabilityZones < 1 {
		return nil, fmt.Errorf("region has no availability zones (azureRegionAvailabilityZoneCount=%d); zonal node pools are unsupported", regionAvailabilityZones)
	}

	if len(strings.TrimSpace(explicitZones)) == 0 {
		zones := make([]string, 0, regionAvailabilityZones)
		for zone := 1; zone <= regionAvailabilityZones; zone++ {
			zones = append(zones, strconv.Itoa(zone))
		}
		return zones, nil
	}

	seen := sets.New[int]()
	var zones []string
	for entry := range strings.SplitSeq(explicitZones, ",") {
		entry = strings.TrimSpace(entry)
		if len(entry) == 0 {
			return nil, fmt.Errorf("zone list %q contains an empty zone", explicitZones)
		}
		zone, err := strconv.Atoi(entry)
		if err != nil {
			return nil, fmt.Errorf("zone %q is not a valid integer", entry)
		}
		if zone < 1 || zone > regionAvailabilityZones {
			return nil, fmt.Errorf("zone %d is outside the region's availability zones [1,%d]", zone, regionAvailabilityZones)
		}
		if seen.Has(zone) {
			return nil, fmt.Errorf("zone list %q contains duplicate zone %d", explicitZones, zone)
		}
		seen.Insert(zone)
		zones = append(zones, strconv.Itoa(zone))
	}
	return zones, nil
}
