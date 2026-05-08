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
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	configv1 "github.com/openshift/api/config/v1"
	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

var (
	ErrNightlyReleaseStreamNotFound = errors.New("nightly release stream not found")
	ErrNoAcceptedNightlyTags        = errors.New("no accepted nightly tags found")
	ErrNoParseableNightlyTags       = errors.New("no parseable nightly tags found")
	ErrVersionNotFound              = errors.New("no graph nodes found")
)

const (
	graphAPIRequestTimeout     = 30 * time.Second
	versionFetchMaxRetries     = 3
	versionFetchRetryBaseDelay = 1 * time.Second
)

func retryOnTransientError[T any](ctx context.Context, f func() (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := range versionFetchMaxRetries + 1 {
		if attempt > 0 {
			backoff := versionFetchRetryBaseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(backoff):
			}
		}
		result, err := f()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryableVersionError(err) {
			return zero, err
		}
	}
	return zero, fmt.Errorf("after %d attempts: %w", versionFetchMaxRetries+1, lastErr)
}

func isRetryableVersionError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrVersionNotFound) ||
		errors.Is(err, ErrNightlyReleaseStreamNotFound) ||
		errors.Is(err, ErrNoAcceptedNightlyTags) ||
		errors.Is(err, ErrNoParseableNightlyTags) {
		return false
	}
	if cincinnati.IsCincinnatiVersionNotFoundError(err) {
		return false
	}
	return true
}

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
		if !cincinnati.IsCincinnatiVersionNotFoundError(err) {
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

	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("get Cincinnati URI: %w", err)
	}
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	client := cvocincinnati.NewClient(uuid.NameSpaceDNS, transport, "ARO-HCP", cincinnati.NewAlwaysConditionRegistry())
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, fromVersion.Major, fromVersion.Minor)

	possibleUpgradeCandidates, err := retryOnTransientError(ctx, func() ([]configv1.Release, error) {
		_, updates, _, err := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", channel, fromVersion)
		return updates, err
	})
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

	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, fmt.Errorf("get Cincinnati URI: %w", err)
	}
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		transport = &http.Transport{}
	}
	client := cvocincinnati.NewClient(uuid.NameSpaceDNS, transport, "ARO-HCP", cincinnati.NewAlwaysConditionRegistry())

	possibleCandidates, err := retryOnTransientError(ctx, func() ([]configv1.Release, error) {
		_, candidates, _, err := client.GetUpdates(ctx, cincinnatiURI, "multi", "multi", channel, fromVer)
		return candidates, err
	})
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

// GetLatestInstallVersion returns the latest install version for the given channel group and version.
// For nightly channels, it returns the latest accepted nightly tag.
// For all other channels, it returns the latest version in the minor.
// Transient HTTP/DNS errors are retried with exponential backoff.
func GetLatestInstallVersion(ctx context.Context, channelGroup string, version string) (string, error) {
	return retryOnTransientError(ctx, func() (string, error) {
		if channelGroup == "nightly" {
			return getLatestInstallVersionForNightlyChannel(ctx, version)
		}
		return getLatestInstallVersionForGraphChannel(ctx, channelGroup, version)
	})
}

// Note that this function is different from GetLatestVersionInMinor because it will return also engineering candidate versions.
func getLatestInstallVersionForGraphChannel(ctx context.Context, channelGroup string, version string) (string, error) {
	channel := fmt.Sprintf("%s-%s", channelGroup, version)
	graphURL := fmt.Sprintf("https://api.openshift.com/api/upgrades_info/v1/graph?channel=%s", url.QueryEscape(channel))

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, graphURL, nil)
	if err != nil {
		return "", fmt.Errorf("create %s graph request for %s: %w", channelGroup, channel, err)
	}

	client := &http.Client{Timeout: graphAPIRequestTimeout}
	resp, err := client.Do(req)
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
		return "", fmt.Errorf("%w for %s", ErrVersionNotFound, channel)
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
		return "", fmt.Errorf("%w: no %s versions in %s for requested minor %s", ErrVersionNotFound, channelGroup, channel, version)
	}

	return latestVersionID, nil
}

// getLatestInstallVersionForNightlyChannel returns the latest accepted nightly tag for the given minor version
// (for example "4.19" -> "4.19.0-0.nightly-multi-YYYY-MM-DD-HHMMSS").
func getLatestInstallVersionForNightlyChannel(ctx context.Context, version string) (string, error) {
	releaseStream := fmt.Sprintf("%s.0-0.nightly-multi", version)
	releaseTagsURL := fmt.Sprintf("https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream/%s/tags?phase=Accepted", url.PathEscape(releaseStream))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseTagsURL, nil)
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
