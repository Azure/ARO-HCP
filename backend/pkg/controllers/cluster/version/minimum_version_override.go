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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// currentControlPlaneMinor derives the cluster's current control-plane minor version: the
// most recent active version's minor if any active versions exist, otherwise the
// pre-override selected version's minor (the install case, where nothing has become active
// yet). Returns false when neither is available, meaning there is no minor to compare
// against.
func currentControlPlaneMinor(activeVersions []coreapi.HCPClusterActiveVersion, selected *semver.Version) (semver.Version, bool) {
	if len(activeVersions) > 0 && activeVersions[0].Version != nil {
		v := *activeVersions[0].Version
		return semver.MustParse(fmt.Sprintf("%d.%d.0", v.Major, v.Minor)), true
	}
	if selected != nil {
		return semver.MustParse(fmt.Sprintf("%d.%d.0", selected.Major, selected.Minor)), true
	}
	return semver.Version{}, false
}

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
// selected may be nil (e.g. the z-stream managed-upgrade case found no candidate patch);
// a same-minor or higher-minor minimum can still force a version in that case, as long as
// currentControlPlaneMinor can derive a minor from activeVersions. When neither
// activeVersions nor selected can establish a current minor (initial install with no
// candidate resolved yet), there is nothing to compare minimums against and nil is
// returned. When minimumVersions is empty, returns selected unchanged.
func applyMinimumVersionOverride(selected *semver.Version, activeVersions []coreapi.HCPClusterActiveVersion, minimumVersions []semver.Version) *semver.Version {
	if len(minimumVersions) == 0 {
		return selected
	}

	currentMinor, ok := currentControlPlaneMinor(activeVersions, selected)
	if !ok {
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

	// If there's a higher-minor minimum, it forces a y-stream upgrade to at least
	// that version. But if selected has already reached (or passed) that minor at
	// or above the minimum -- e.g. a customer-initiated y-stream upgrade already
	// resolved a higher patch -- keep selected instead of downgrading it back to
	// the floor.
	if higherMinorMin != nil {
		if selected != nil {
			higherMinorMinMinor := semver.MustParse(fmt.Sprintf("%d.%d.0", higherMinorMin.Major, higherMinorMin.Minor))
			selectedMinor := semver.MustParse(fmt.Sprintf("%d.%d.0", selected.Major, selected.Minor))
			if selectedMinor.GT(higherMinorMinMinor) || (selectedMinor.EQ(higherMinorMinMinor) && selected.GTE(*higherMinorMin)) {
				return selected
			}
		}
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
