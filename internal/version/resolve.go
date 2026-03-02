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
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ResolveInitialVersion determines the best X.Y.Z version for initial cluster creation
// given a customer-desired minor version (X.Y) and channel group.
//
// It queries the Cincinnati update graph to find the latest Z-stream version in the
// target minor that is also a gateway to the next minor version (when one exists).
// If no suitable version is found, it falls back to X.Y.0.
//
// This duplicates the initial version selection logic from the backend upgrade controller's
// desiredControlPlaneZVersion for use in the frontend creation flow.
func ResolveInitialVersion(ctx context.Context, cincinnatiClient cincinatti.Client, channelGroup string, customerDesiredMinor string) (semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Resolving initial desired version", "customerDesiredMinor", customerDesiredMinor, "channelGroup", channelGroup)

	// ParseTolerant handles both "4.19" and "4.19.0" formats
	customerDotZeroRelease := api.Must(semver.ParseTolerant(customerDesiredMinor))

	initialDesiredVersion, err := findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, customerDotZeroRelease, []semver.Version{customerDotZeroRelease})
	if err != nil {
		return semver.Version{}, fmt.Errorf("failed to resolve initial version: %w", err)
	}

	// If no desired version found, fall back to customerDotZeroRelease.
	// This happens when either:
	// - there is no latestVersion greater than customerDotZeroRelease
	// - or there is a latestVersion greater than customerDotZeroRelease but it doesn't have
	//   an upgrade path to the next minor if the next minor existed
	// In both cases, customerDotZeroRelease is guaranteed to exist (since we didn't get a
	// VersionNotFound error back when querying for it from Cincinnati). It is safe to use.
	if initialDesiredVersion == nil {
		return customerDotZeroRelease, nil
	}

	return *initialDesiredVersion, nil
}

// findLatestVersionInMinor queries Cincinnati and finds the latest version within the specified target minor.
//
// It implements the core version selection logic:
//  1. Query Cincinnati for all available updates from EACH active version in the target minor channel
//  2. Filter candidates: only include versions within the target minor
//  3. Intersect candidate sets: only keep versions reachable from ALL active versions
//  4. Delegate to selectBestVersionFromCandidates for final selection
//
// Returns nil if no suitable version is found.
func findLatestVersionInMinor(
	ctx context.Context,
	cincinnatiClient cincinatti.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	activeVersions []semver.Version,
) (*semver.Version, error) {
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err)
	}

	targetMinorString := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor)
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinorString)

	candidatesByVersion := map[string]struct {
		version semver.Version
		count   int
	}{}

	for _, activeVersion := range activeVersions {
		_, candidateReleases, _, err := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, activeVersion)
		if err != nil {
			return nil, err
		}

		for _, candidate := range candidateReleases {
			candidateTargetVersion := semver.MustParse(candidate.Version)

			if candidateTargetVersion.Major != targetMinorVersion.Major || candidateTargetVersion.Minor != targetMinorVersion.Minor {
				continue
			}

			candidateEntry := candidatesByVersion[candidateTargetVersion.String()]
			candidateEntry.version = candidateTargetVersion
			candidateEntry.count++
			candidatesByVersion[candidateTargetVersion.String()] = candidateEntry
		}
	}

	commonCandidates := []semver.Version{}
	for _, candidateEntry := range candidatesByVersion {
		if candidateEntry.count == len(activeVersions) {
			commonCandidates = append(commonCandidates, candidateEntry.version)
		}
	}

	return selectBestVersionFromCandidates(ctx, cincinnatiClient, channelGroup, targetMinorVersion, commonCandidates)
}

// selectBestVersionFromCandidates finds the best version to upgrade to from a list of candidate versions.
// It prioritizes versions that are gateways to the next minor version.
//
// Algorithm:
//  1. Sort candidates by version (descending - latest first)
//  2. Check if the next minor channel exists in Cincinnati
//  3. If next minor doesn't exist: return the latest candidate
//  4. If next minor exists: iterate through candidates to find a gateway version to the next minor
//  5. If no gateway found: return nil
//
// Returns nil if no suitable version is found.
func selectBestVersionFromCandidates(
	ctx context.Context,
	cincinnatiClient cincinatti.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	candidates []semver.Version,
) (*semver.Version, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	slices.SortFunc(candidates, func(a, b semver.Version) int {
		return b.Compare(a)
	})

	nextMinor := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor+1)

	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err)
	}

	_, _, _, nextMinorExistsErr := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", fmt.Sprintf("%s-%s", channelGroup, nextMinor), candidates[0])

	if nextMinorExistsErr != nil && !cincinatti.IsCincinnatiVersionNotFoundError(nextMinorExistsErr) {
		return nil, nextMinorExistsErr
	}

	nextMinorExists := nextMinorExistsErr == nil

	if !nextMinorExists {
		return &candidates[0], nil
	}

	for _, candidate := range candidates {
		isGateway, err := isGatewayToNextMinor(ctx, candidate, cincinnatiClient, channelGroup, nextMinor)
		if err != nil {
			return nil, err
		}

		if isGateway {
			return &candidate, nil
		}
	}

	return nil, nil
}

// isGatewayToNextMinor checks if a given version has an upgrade path to the next minor version.
func isGatewayToNextMinor(ctx context.Context, ver semver.Version, cincinnatiClient cincinatti.Client, channelGroup string, nextMinor string) (bool, error) {
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return false, err
	}

	nextMinorCincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, nextMinor)

	_, allNextMinorUpdates, _, err := cincinnatiClient.GetUpdates(
		ctx,
		cincinnatiURI,
		"multi",
		"multi",
		nextMinorCincinnatiChannel,
		ver,
	)
	if cincinatti.IsCincinnatiVersionNotFoundError(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	hasPath := slices.ContainsFunc(allNextMinorUpdates, func(release configv1.Release) bool {
		return strings.Contains(release.Version, nextMinor+".")
	})
	return hasPath, nil
}
