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

// RequiredAvailabilityZones is the number of zones every pool spans.
const RequiredAvailabilityZones = 3

// ResolveZones computes exactly RequiredAvailabilityZones availability zones
// to plan node pools across, shared by the nodepool controller and the
// aks-cluster-create tool so both derive and validate zones identically.
//
// regionAvailabilityZones is the region's real zone count (config
// azureRegionAvailabilityZoneCount), trusted as authoritative.
//
// explicitZones is an optional operator override (comma-separated, e.g.
// "4,2,1") for choosing which zones to use, e.g. to skip a known-bad zone.
// It must name exactly RequiredAvailabilityZones distinct integers in
// [1,regionAvailabilityZones]; the list is returned normalized but in the
// given order. When empty, ResolveZones returns the region's first
// RequiredAvailabilityZones zones. Either way, a region with too few zones,
// or an explicit list naming the wrong count or an out-of-range/duplicate
// zone, is rejected.
func ResolveZones(explicitZones string, regionAvailabilityZones int) ([]string, error) {
	if regionAvailabilityZones < RequiredAvailabilityZones {
		return nil, fmt.Errorf("region has %d availability zones, fewer than the %d required", regionAvailabilityZones, RequiredAvailabilityZones)
	}

	if len(strings.TrimSpace(explicitZones)) == 0 {
		zones := make([]string, 0, RequiredAvailabilityZones)
		for zone := 1; zone <= RequiredAvailabilityZones; zone++ {
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
	if len(zones) != RequiredAvailabilityZones {
		return nil, fmt.Errorf("zone list %q names %d zones, must name exactly %d", explicitZones, len(zones), RequiredAvailabilityZones)
	}
	return zones, nil
}
