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

package upgradecontrollers

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/cincinatti"
)

// sortReleasesByVersionDescending sorts releases in descending order (latest version first).
func sortReleasesByVersionDescending(releases []configv1.Release) {
	slices.SortFunc(releases, func(a, b configv1.Release) int {
		aVersion, err := semver.Parse(a.Version)
		if err != nil {
			return 0 // should never happen
		}

		bVersion, err := semver.Parse(b.Version)
		if err != nil { // should never happen
			return 0
		}
		return bVersion.Compare(aVersion) // descending: latest first
	})
}

// isValidNextYStreamUpgradePath validates that a next Y-stream upgrade path is valid.
// It ensures the desired minor is exactly one minor version ahead of the actual minor
// and prevents downgrades or skipping minors.
//
// Parameters:
//   - actualMinor: Current minor version in "X.Y" format (e.g., "4.19")
//   - desiredMinor: Target minor version in "X.Y" format (e.g., "4.20")
func isValidNextYStreamUpgradePath(actualMinor, desiredMinor string) bool {
	// Parse actualMinor (e.g., "4.19" -> 4.19.0)
	actualVersion, err := semver.Parse(actualMinor + ".0")
	if err != nil {
		return false
	}

	// Parse desiredMinor (e.g., "4.20" -> 4.20.0)
	desiredVersion, err := semver.Parse(desiredMinor + ".0")
	if err != nil {
		return false
	}

	// Check for downgrade (desired < actual)
	if desiredVersion.LT(actualVersion) {
		return false
	}

	// Ensure desired is exactly one minor ahead (same major, minor + 1)
	if desiredVersion.Major != actualVersion.Major || desiredVersion.Minor != actualVersion.Minor+1 {
		return false
	}

	return true
}

// isGatewayToNextMinor checks if a given version has an upgrade path to the next minor version.
// Returns true if the version is a gateway, false otherwise. Returns an error if the check fails.
func isGatewayToNextMinor(ctx context.Context, ver semver.Version, cincinnatiClient cincinatti.Client, channelGroup string, nextMinor string) (bool, error) {
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return false, err
	}

	nextMinorCincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, nextMinor)

	// Query Cincinnati for available updates
	// ARO-HCP uses Multi architecture for all clusters
	_, allNextMinorUpdates, _, err := cincinnatiClient.GetUpdates(
		ctx,
		cincinnatiURI,
		"multi",
		"multi",
		nextMinorCincinnatiChannel,
		ver,
	)
	if err != nil {
		if cincinatti.IsCincinnatiVersionNotFoundError(err) {
			return false, nil
		}
		return false, err
	}

	// Check if any release contains a version in the next minor
	hasPath := slices.ContainsFunc(allNextMinorUpdates, func(release configv1.Release) bool {
		return strings.Contains(release.Version, nextMinor+".")
	})
	return hasPath, nil
}
