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

package apihelpers

import (
	"github.com/blang/semver/v4"

	resourcesapi "github.com/Azure/ARO-HCP/internal/apis/resources"
)

// FindLowestAndHighestClusterVersion returns (lowest, highest) *semver.Version from activeVersions.
// When activeVersions is empty, it returns (nil, nil).
func FindLowestAndHighestClusterVersion(activeVersions []resourcesapi.HCPClusterActiveVersion) (*semver.Version, *semver.Version) {
	var low, high *semver.Version
	for _, activeVersion := range activeVersions {
		if low == nil || activeVersion.Version.LT(*low) {
			low = activeVersion.Version
		}
		if high == nil || activeVersion.Version.GT(*high) {
			high = activeVersion.Version
		}
	}
	return low, high
}

// FindLowestAndHighestNodePoolVersion returns the lowest and highest versions from the node pool active versions.
// ActiveVersions can be in any order, so we iterate to find the actual minimum and maximum.
func FindLowestAndHighestNodePoolVersion(activeVersions []resourcesapi.HCPNodePoolActiveVersion) (*semver.Version, *semver.Version) {
	var lowest, highest *semver.Version
	for _, av := range activeVersions {
		if lowest == nil || av.Version.LT(*lowest) {
			lowest = av.Version
		}
		if highest == nil || av.Version.GT(*highest) {
			highest = av.Version
		}
	}
	return lowest, highest
}
