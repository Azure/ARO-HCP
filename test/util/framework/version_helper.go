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
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var (
	ErrNightlyReleaseStreamNotFound = errors.New("nightly release stream not found")
	ErrNoAcceptedNightlyTags        = errors.New("no accepted nightly tags found")
	ErrNoParseableNightlyTags       = errors.New("no parseable nightly tags found")
)

// GetInstallVersionForZStreamUpgrade returns the version to install the cluster with when testing
// a z-stream upgrade, and whether that version has an available z-stream upgrade path. It uses
// configuredVersionID and queries Cincinnati for the given channelGroup (e.g. "candidate", "stable").
// When no version with an upgrade path is found, it still returns the configured version so the
// caller can install and optionally skip upgrade assertions.
func GetInstallVersionForZStreamUpgrade(ctx context.Context, channelGroup string, configuredVersionID string) (installVersion string, hasUpgradePath bool, err error) {
	configuredVersion := api.Must(semver.ParseTolerant(configuredVersionID))
	candidates, err := GetAllVersionsInMinorStartingWith(ctx, channelGroup, configuredVersionID)
	if err != nil {
		return "", false, err
	}
	if len(candidates) == 1 {
		return configuredVersion.String(), false, nil
	}

	nextMinorStr := fmt.Sprintf("%d.%d", configuredVersion.Major, configuredVersion.Minor+1)
	maxVersion, err := GetLatestVersionInMinor(ctx, channelGroup, nextMinorStr)
	if err != nil {
		if !cincinatti.IsCincinnatiVersionNotFoundError(err) {
			return "", false, err
		}
		// we don't have the next minor, use the max version in the current minor
		maxVersion = candidates[0].String()
	}

	for i := 0; i < len(candidates)-1; i++ {
		upgradeTargets, err := GetUpgradeCandidatesInMaxMinorFromCincinnati(ctx, channelGroup, maxVersion, candidates[i].String())
		if err != nil {
			return "", false, err
		}
		if len(upgradeTargets) > 0 {
			return candidates[i+1].String(), true, nil
		}
	}
	return candidates[0].String(), false, nil
}

// GetAllVersionsInMinorStartingWith returns all OpenShift versions in the same major.minor as the given version,
// including that version, from Cincinnati for the given channelGroup. The version string is parse-tolerant
// (e.g. "4.20", "4.20.0", "4.20.1"). Results are sorted descending (latest first).
func GetAllVersionsInMinorStartingWith(ctx context.Context, channelGroup string, version string) ([]semver.Version, error) {
	fromVersion, err := semver.ParseTolerant(version)
	if err != nil {
		return nil, fmt.Errorf("parse version %q: %w", version, err)
	}

	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("get Cincinnati URI: %w", err)
	}
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	client := cvocincinnati.NewClient(uuid.NameSpaceDNS, transport, "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, fromVersion.Major, fromVersion.Minor)

	_, possibleUpgradeCandidates, _, err := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", channel, fromVersion)
	if err != nil {
		return nil, err
	}

	candidates := []semver.Version{fromVersion}
	for _, release := range possibleUpgradeCandidates {
		candidateVersion := api.Must(semver.ParseTolerant(release.Version))
		if candidateVersion.Major != fromVersion.Major || candidateVersion.Minor != fromVersion.Minor {
			continue
		}
		candidates = append(candidates, candidateVersion)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[j].LT(candidates[i])
	})
	return candidates, nil
}

// GetLatestVersionInMinor returns the latest OpenShift version for the given major.minor (e.g. "4.20")
// from Cincinnati for the given channelGroup (e.g. "candidate", "stable").
func GetLatestVersionInMinor(ctx context.Context, channelGroup string, minorVersion string) (string, error) {
	versions, err := GetAllVersionsInMinorStartingWith(ctx, channelGroup, minorVersion)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", &cvocincinnati.Error{Reason: "VersionNotFound", Message: fmt.Sprintf("no versions found for minor %s", minorVersion)}
	}
	return versions[0].String(), nil
}

// GetLatestVersionInMinorWithUpgradePathTo returns the latest OpenShift version for fromMinor (e.g. "4.20")
// that has a Cincinnati upgrade path to toMinor (e.g. "4.21"), for the given channelGroup.
// hasUpgradePath is false when no version in fromMinor has an upgrade path to toMinor.
func GetLatestVersionInMinorWithUpgradePathTo(ctx context.Context, channelGroup string, fromMinor string, toMinor string) (version string, hasUpgradePath bool, err error) {
	versionsInFromMinor, err := GetAllVersionsInMinorStartingWith(ctx, channelGroup, fromMinor)
	if err != nil {
		return "", false, err
	}
	maxInToMinor, err := GetLatestVersionInMinor(ctx, channelGroup, toMinor)
	if err != nil {
		return "", false, err
	}
	for _, v := range versionsInFromMinor {
		candidates, err := GetUpgradeCandidatesInMaxMinorFromCincinnati(ctx, channelGroup, maxInToMinor, v.String())
		if err != nil {
			return "", false, err
		}
		if len(candidates) > 0 {
			return v.String(), true, nil
		}
	}
	return "", false, nil
}

// GetUpgradeCandidatesInMaxMinorFromCincinnati returns all versions in the same major.minor as maxVersion
// that are <= maxVersion and have a Cincinnati upgrade path from fromVersion, for the given channelGroup.
// Results are sorted from lowest to highest. Use for possible upgrade targets (e.g. node pool y-stream upgrade).
func GetUpgradeCandidatesInMaxMinorFromCincinnati(ctx context.Context, channelGroup string, maxVersion string, fromVersion string) (candidates []semver.Version, err error) {
	maxVer, err := semver.ParseTolerant(maxVersion)
	if err != nil {
		return nil, fmt.Errorf("parse maxVersion %q: %w", maxVersion, err)
	}
	fromVer, err := semver.ParseTolerant(fromVersion)
	if err != nil {
		return nil, fmt.Errorf("parse fromVersion %q: %w", fromVersion, err)
	}
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, maxVer.Major, maxVer.Minor)

	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("get Cincinnati URI: %w", err)
	}
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	client := cvocincinnati.NewClient(uuid.NameSpaceDNS, transport, "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())

	_, possibleCandidates, _, err := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", channel, fromVer)
	if err != nil {
		return nil, err
	}

	var out []semver.Version
	for _, release := range possibleCandidates {
		candidateVersion := api.Must(semver.ParseTolerant(release.Version))
		if candidateVersion.Major != maxVer.Major || candidateVersion.Minor != maxVer.Minor {
			continue
		}
		if !candidateVersion.GT(maxVer) {
			out = append(out, candidateVersion)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LT(out[j])
	})
	return out, nil
}

func GetLatestInstallVersion(channelGroup string, version string) (string, error) {
	switch channelGroup {
	case "nightly":
		return GetLatestInstallVersionForNightlyChannel(version)
	case "stable":
		return GetLatestInstallVersionForGraphChannel("stable", version)
	case "candidate":
		return GetLatestInstallVersionForGraphChannel("candidate", version)
	case "fast":
		return GetLatestInstallVersionForGraphChannel("fast", version)
	default:
		return "", fmt.Errorf("invalid channel group: %s", channelGroup)
	}
}

func GetLatestInstallVersionForGraphChannel(channelGroup string, version string) (string, error) {
	channel := fmt.Sprintf("%s-%s", channelGroup, version)
	graphURL := fmt.Sprintf("https://api.openshift.com/api/upgrades_info/v1/graph?channel=%s", url.QueryEscape(channel))

	req, err := http.NewRequest(http.MethodGet, graphURL, nil)
	if err != nil {
		return "", fmt.Errorf("create %s graph request for %s: %w", channelGroup, channel, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("query %s graph for %s: %w", channelGroup, channel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("query %s graph for %s returned %s: %s", channelGroup, channel, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Nodes []struct {
			Version string `json:"version"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode %s graph response for %s: %w", channelGroup, channel, err)
	}
	if len(payload.Nodes) == 0 {
		return "", fmt.Errorf("no graph nodes found for %s", channel)
	}

	requestedMinor, err := semver.ParseTolerant(version)
	if err != nil {
		return "", fmt.Errorf("parse requested %s minor %q: %w", channelGroup, version, err)
	}

	var latestVersion semver.Version
	latestVersionID := ""
	for _, node := range payload.Nodes {
		nodeVersion, parseErr := semver.ParseTolerant(node.Version)
		if parseErr != nil {
			continue
		}
		if nodeVersion.Major != requestedMinor.Major || nodeVersion.Minor != requestedMinor.Minor {
			continue
		}
		if latestVersionID == "" || nodeVersion.GT(latestVersion) {
			latestVersion = nodeVersion
			latestVersionID = node.Version
		}
	}

	if latestVersionID == "" {
		return "", fmt.Errorf("no %s versions found in %s for requested minor %s", channelGroup, channel, version)
	}

	// For stable channel, return the latest version in the minor
	if channelGroup == "stable" {
		return fmt.Sprintf("%d.%d", latestVersion.Major, latestVersion.Minor), nil
	}

	return latestVersionID, nil
}


// GetLatestInstallVersionForNightlyChannel returns the latest accepted nightly tag for the given minor version
// (for example "4.19" -> "4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS").
func GetLatestInstallVersionForNightlyChannel(version string) (string, error) {
	releaseStream := fmt.Sprintf("%s.0-0.nightly-multi", version)
	releaseTagsURL := fmt.Sprintf("https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags?phase=Accepted", url.PathEscape(releaseStream))

	req, err := http.NewRequest(http.MethodGet, releaseTagsURL, nil)
	if err != nil {
		return "", fmt.Errorf("create nightly tags request for %s: %w", releaseStream, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("query nightly tags for %s: %w", releaseStream, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%w for %s: %s", ErrNightlyReleaseStreamNotFound, releaseStream, strings.TrimSpace(string(body)))
		}
		return "", fmt.Errorf("query nightly tags for %s returned %s: %s", releaseStream, resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode nightly tags response for %s: %w", releaseStream, err)
	}
	if len(payload.Tags) == 0 {
		return "", fmt.Errorf("%w for %s", ErrNoAcceptedNightlyTags, releaseStream)
	}

	var (
		latestTagName string
		latestVersion semver.Version
		foundValid    bool
	)
	for _, tag := range payload.Tags {
		candidateVersion, err := semver.ParseTolerant(tag.Name)
		if err != nil {
			// Ignore tags that cannot be parsed as a semantic version.
			continue
		}
		if !foundValid || candidateVersion.GT(latestVersion) {
			latestTagName = tag.Name
			latestVersion = candidateVersion
			foundValid = true
		}
	}
	if !foundValid {
		return "", fmt.Errorf("%w for %s", ErrNoParseableNightlyTags, releaseStream)
	}

	return latestTagName, nil
}
