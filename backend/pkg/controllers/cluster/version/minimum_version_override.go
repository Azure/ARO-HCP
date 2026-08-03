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

package version

import (
	"fmt"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api"
)

// applyMinimumVersionOverride applies SRE-specified minimum version constraints.
// It compares the selected version against the MinimumVersions list and returns
// a version that satisfies the minimum requirements.
//
// Rules (from the field documentation):
//  1. If a minimum exists for the selected version's minor and the selected version
//     is already >= that minimum, keep the selected version.
//  2. If a minimum exists for the selected version's minor but the selected version
//     is < the minimum, return the minimum instead.
//  3. If any minimum targets a minor AHEAD of the cluster's current minor (from
//     activeVersions), return that minimum directly (forces y-stream upgrade).
//     When multiple higher-minor minimums exist, use the lowest one (one step at a time).
//
// When selected is nil and a minimum forces a version, it returns the forced version.
// When minimumVersions is empty, returns selected unchanged.
func applyMinimumVersionOverride(selected *semver.Version, activeVersions []api.HCPClusterActiveVersion, minimumVersions []semver.Version) *semver.Version {
	if len(minimumVersions) == 0 {
		return selected
	}

	// Determine the cluster's current minor from the most recent active version.
	// If no active versions, use the selected version's minor (install case).
	var currentMinor semver.Version
	if len(activeVersions) > 0 && activeVersions[0].Version != nil {
		v := *activeVersions[0].Version
		currentMinor = semver.MustParse(fmt.Sprintf("%d.%d.0", v.Major, v.Minor))
	} else if selected != nil {
		currentMinor = semver.MustParse(fmt.Sprintf("%d.%d.0", selected.Major, selected.Minor))
	} else {
		// No active versions and no selected version: examine minimums for any
		// that would force a version. Without a current minor to compare against
		// we cannot determine same-minor vs higher-minor, so return nil.
		return nil
	}

	// Find the highest minimum for the current minor and the lowest minimum
	// that targets a higher minor than the current.
	var sameMinorMax *semver.Version
	var higherMinorMin *semver.Version
	for i := range minimumVersions {
		mv := minimumVersions[i]
		mvMinor := semver.MustParse(fmt.Sprintf("%d.%d.0", mv.Major, mv.Minor))
		if mvMinor.EQ(currentMinor) {
			if sameMinorMax == nil || mv.GT(*sameMinorMax) {
				cp := mv
				sameMinorMax = &cp
			}
		} else if mvMinor.GT(currentMinor) {
			if higherMinorMin == nil || mv.LT(*higherMinorMin) {
				cp := mv
				higherMinorMin = &cp
			}
		}
	}

	// If there's a higher-minor minimum, return it (forces y-stream upgrade
	// regardless of selected).
	if higherMinorMin != nil {
		return higherMinorMin
	}

	// If selected is nil and there's a same-minor minimum, return the minimum.
	if selected == nil && sameMinorMax != nil {
		return sameMinorMax
	}

	// If selected is non-nil and < same-minor minimum, return the minimum.
	if selected != nil && sameMinorMax != nil && selected.LT(*sameMinorMax) {
		return sameMinorMax
	}

	// Otherwise return selected.
	return selected
}
