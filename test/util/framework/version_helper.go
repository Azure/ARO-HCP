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

package framework

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
)

// GetInstallVersionForZStreamUpgrade returns the version to install the cluster with when testing
// a z-stream upgrade, and whether that version has an available z-stream upgrade path. It uses
// configuredVersionID and queries Cincinnati for the given channelGroup (e.g. "candidate", "stable").
// When no version with an upgrade path is found, it still returns the configured version so the
// caller can install and optionally skip upgrade assertions.
func GetInstallVersionForZStreamUpgrade(ctx context.Context, channelGroup string, configuredVersionID string) (installVersion string, hasUpgradePath bool, err error) {
	configuredVersion := api.Must(semver.ParseTolerant(configuredVersionID))

	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return "", false, fmt.Errorf("get Cincinnati URI: %w", err)
	}

	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	client := cvocincinnati.NewClient(uuid.NameSpaceDNS, transport, "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, configuredVersion.Major, configuredVersion.Minor)

	_, possibleUpgradeCandidates, _, err := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", channel, configuredVersion)
	if err != nil {
		return "", false, fmt.Errorf("get Cincinnati updates for %s in %s: %w", configuredVersion.String(), channel, err)
	}

	// Restrict to versions in the same major.minor (z-stream only).
	candidates := []semver.Version{configuredVersion}
	for _, release := range possibleUpgradeCandidates {
		candidateVersion := api.Must(semver.ParseTolerant(release.Version))
		if candidateVersion.Major != configuredVersion.Major || candidateVersion.Minor != configuredVersion.Minor {
			continue
		}
		candidates = append(candidates, candidateVersion)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[j].LT(candidates[i])
	})

	if len(candidates) == 1 {
		return configuredVersion.String(), false, nil
	}

	return pickInstallVersionWithNextMinorPreference(ctx, client, cincinnatiURI, channelGroup, configuredVersion, candidates)
}

// pickInstallVersionWithNextMinorPreference chooses an install version from versionsInSameMinor (sorted descending, latest first).
// When the next minor exists, it prefers a version whose upgrade target has an upgrade path to the next minor; otherwise it returns the version just before latest.
// The second return value is true only when an install version with an upgrade path was found.
func pickInstallVersionWithNextMinorPreference(ctx context.Context, client cincinatti.Client, cincinnatiURI *url.URL, channelGroup string, configuredVersion semver.Version, versionsInSameMinor []semver.Version) (string, bool, error) {
	installTarget := versionsInSameMinor[1] // latest is first, so second is the default install (upgrade to latest)
	nextMinorStr := fmt.Sprintf("%d.%d", configuredVersion.Major, configuredVersion.Minor+1)
	nextMinorChannel := fmt.Sprintf("%s-%s", channelGroup, nextMinorStr)
	_, _, _, nextMinorErr := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", nextMinorChannel, installTarget)
	if nextMinorErr != nil && !cincinatti.IsCincinnatiVersionNotFoundError(nextMinorErr) {
		return "", false, fmt.Errorf("checking next minor %s: %w", nextMinorStr, nextMinorErr)
	}
	if nextMinorErr != nil {
		// Next minor not available; use default (version just before latest). Upgrade path exists (z-stream to latest).
		return installTarget.String(), true, nil
	}
	// Find the latest upgrade target that has path to next minor; install is the version next to it (one step older).
	for i := 0; i < len(versionsInSameMinor)-1; i++ {
		hasPath, err := hasUpgradePathToNextMinor(ctx, client, cincinnatiURI, nextMinorChannel, nextMinorStr, versionsInSameMinor[i])
		if err != nil {
			return "", false, err
		}
		if hasPath {
			return versionsInSameMinor[i+1].String(), true, nil
		}
	}
	// No version has path to next minor; install the latest in same minor (no upgrade path to verify).
	return versionsInSameMinor[0].String(), false, nil
}

// hasUpgradePathToNextMinor returns true if the given version has an upgrade path to the next minor.
// nextMinorChannel is the Cincinnati channel for the next minor (e.g. "candidate-4.21"); nextMinor is the version prefix (e.g. "4.21").
func hasUpgradePathToNextMinor(ctx context.Context, cincinnatiClient cincinatti.Client, uri *url.URL, nextMinorChannel, nextMinor string, ver semver.Version) (bool, error) {
	_, updates, _, err := cincinnatiClient.GetUpdates(ctx, uri, "multi", "multi", nextMinorChannel, ver)
	if err != nil && !cincinatti.IsCincinnatiVersionNotFoundError(err) {
		return false, err
	}
	if err != nil {
		return false, nil
	}
	for _, r := range updates {
		if strings.Contains(r.Version, nextMinor+".") {
			return true, nil
		}
	}
	return false, nil
}
