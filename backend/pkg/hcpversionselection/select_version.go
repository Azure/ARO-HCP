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

package hcpversionselection

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/cincinnati"
)

// NoGatewayError is returned when the next minor version channel exists and
// has edges from some versions in the current minor, but none of the candidate
// versions reachable from the cluster's active versions have a valid transitive
// upgrade chain. The cluster must wait for Cincinnati to publish a new edge.
type NoGatewayError struct {
	ActiveVersions []semver.Version
	DesiredMinor   string
	NextMinor      string
}

func (e *NoGatewayError) Error() string {
	versions := make([]string, 0, len(e.ActiveVersions))
	for _, v := range e.ActiveVersions {
		versions = append(versions, v.String())
	}
	return fmt.Sprintf(
		"no upgrade path from active versions [%s] to next minor %s: "+
			"the %s channel exists but no reachable candidate in %s is a gateway; "+
			"the cluster must wait for a new edge to be published",
		strings.Join(versions, ", "), e.NextMinor, e.NextMinor, e.DesiredMinor,
	)
}

// SelectControlPlaneVersion selects the best z-stream within desiredYVersion's
// minor that preserves the cluster's ability to upgrade through all subsequent
// minors.
//
// The selected version is the most recent z-stream that:
//   - Is reachable from ALL active versions in the HostedCluster's control plane
//     version history (both Completed and Partial entries)
//   - Has a transitive upgrade chain: it must be a gateway to some version in
//     the next minor, and that version must itself be a gateway to the minor
//     after that, and so on until the chain terminates
//
// The chain terminates (and any candidate is acceptable) when either:
//   - The next minor's Cincinnati channel does not exist, OR
//   - No version in the current minor can reach the next minor (the next
//     minor's channel exists but has no edges from the current minor)
//
// When no candidate has a valid transitive chain but the next minor is
// reachable from other versions in the desired minor, a *NoGatewayError is
// returned — the cluster must wait for Cincinnati to publish a new edge.
//
// For install (hostedCluster is nil), there are no active versions to constrain
// the selection; the function queries from the desired minor's .0 release and
// returns the latest version with a valid chain, or .0 itself if no updates
// exist.
//
// The 4.22 → 5.0 version numbering transition is handled: 5.0 is treated as
// the next minor after 4.22.
func SelectControlPlaneVersion(ctx context.Context, channelStability string, desiredYVersion semver.Version, cincinnatiURI *url.URL, cvoClient cincinnati.Client, hostedCluster *v1beta1.HostedCluster) (*semver.Version, error) {
	desiredMinor := semver.Version{Major: desiredYVersion.Major, Minor: desiredYVersion.Minor}
	activeVersions := activeVersionsFromHostedCluster(hostedCluster)

	channel := formatChannel(channelStability, desiredMinor)
	candidates, err := findCandidates(ctx, cvoClient, cincinnatiURI, channel, desiredMinor, activeVersions)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	slices.SortFunc(candidates, func(a, b semver.Version) int {
		return b.Compare(a)
	})

	result, err := findLatestGateway(ctx, cvoClient, cincinnatiURI, channelStability, desiredMinor, candidates)
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}

	nextMajor, nextMinorNum := nextMinorVersion(desiredMinor)
	return nil, &NoGatewayError{
		ActiveVersions: activeVersions,
		DesiredMinor:   fmt.Sprintf("%d.%d", desiredMinor.Major, desiredMinor.Minor),
		NextMinor:      fmt.Sprintf("%d.%d", nextMajor, nextMinorNum),
	}
}

// findLatestGateway returns the latest version from candidates that has a valid
// transitive upgrade chain through all subsequent minors. Candidates must be
// sorted in descending order.
//
// For each candidate, the function checks whether it has a valid upgrade path
// to the next minor — either directly (it is a gateway) or transitively
// through z-stream upgrades within the same minor. When a path is found, it
// recursively verifies that the target in the next minor also has a valid
// chain. The first candidate (latest) whose chain is fully valid is returned.
//
// The chain terminates (and the latest candidate is returned) when:
//   - doesNextMinorExist returns false, OR
//   - isNextMinorReachableFromCurrentMinor returns false
//
// Returns nil when the next minor exists, is reachable, but no candidate has a
// valid chain.
func findLatestGateway(ctx context.Context, client cincinnati.Client, cincinnatiURI *url.URL, channelStability string, currentMinor semver.Version, candidates []semver.Version) (*semver.Version, error) {
	exists, err := doesNextMinorExist(ctx, client, cincinnatiURI, channelStability, currentMinor)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &candidates[0], nil
	}

	reachable, err := isNextMinorReachableFromCurrentMinor(ctx, client, cincinnatiURI, channelStability, currentMinor, nil)
	if err != nil {
		return nil, err
	}
	if !reachable {
		return &candidates[0], nil
	}

	for _, candidate := range candidates {
		valid, err := hasValidUpgradePath(ctx, client, cincinnatiURI, channelStability, currentMinor, candidate, make(map[string]bool))
		if err != nil {
			return nil, err
		}
		if valid {
			return &candidate, nil
		}
	}

	return nil, nil
}

// hasValidUpgradePath checks whether ver can reach the next minor — either
// directly as a gateway, or transitively through z-stream upgrades within
// currentMinor — AND that the reached version in the next minor has a valid
// chain through subsequent minors.
//
// The visited set prevents cycles when following within-minor z-stream edges.
func hasValidUpgradePath(ctx context.Context, client cincinnati.Client, cincinnatiURI *url.URL, channelStability string, currentMinor semver.Version, ver semver.Version, visited map[string]bool) (bool, error) {
	if visited[ver.String()] {
		return false, nil
	}
	visited[ver.String()] = true

	nextMajor, nextMinorNum := nextMinorVersion(currentMinor)
	nextMinor := semver.Version{Major: nextMajor, Minor: nextMinorNum}

	targets, err := getGatewayTargets(ctx, client, cincinnatiURI, channelStability, currentMinor, ver)
	if err != nil {
		return false, err
	}
	if len(targets) > 0 {
		slices.SortFunc(targets, func(a, b semver.Version) int {
			return b.Compare(a)
		})
		chainResult, err := findLatestGateway(ctx, client, cincinnatiURI, channelStability, nextMinor, targets)
		if err != nil {
			return false, err
		}
		if chainResult != nil {
			return true, nil
		}
	}

	channel := formatChannel(channelStability, currentMinor)
	_, updates, _, updErr := client.GetUpdates(ctx, cloneURL(cincinnatiURI), "multi", "multi", channel, ver)
	if updErr != nil {
		return false, nil
	}

	for _, rel := range updates {
		next, parseErr := semver.Parse(rel.Version)
		if parseErr != nil {
			continue
		}
		if next.Major != currentMinor.Major || next.Minor != currentMinor.Minor {
			continue
		}
		valid, err := hasValidUpgradePath(ctx, client, cincinnatiURI, channelStability, currentMinor, next, visited)
		if err != nil {
			return false, err
		}
		if valid {
			return true, nil
		}
	}

	return false, nil
}

// doesNextMinorExist checks whether Cincinnati has a channel for the next
// minor version after currentMinor by probing for its .0 release.
func doesNextMinorExist(ctx context.Context, client cincinnati.Client, cincinnatiURI *url.URL, channelStability string, currentMinor semver.Version) (bool, error) {
	nextMajor, nextMinorNum := nextMinorVersion(currentMinor)
	nextChannel := formatChannel(channelStability, semver.Version{Major: nextMajor, Minor: nextMinorNum})
	probe := semver.Version{Major: nextMajor, Minor: nextMinorNum}

	_, _, _, err := client.GetUpdates(ctx, cloneURL(cincinnatiURI), "multi", "multi", nextChannel, probe)
	if isVersionOrChannelNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probing next minor channel %s: %w", nextChannel, err)
	}
	return true, nil
}

// isNextMinorReachableFromCurrentMinor checks whether any version in
// currentMinor's channel has an upgrade path to the next minor. It discovers
// versions by querying from currentMinor's .0 release, then checks each
// (skipping versions in alreadyChecked) for a gateway edge to the next minor.
//
// Returns false if the .0 release is not in the channel (cannot discover
// versions to check).
func isNextMinorReachableFromCurrentMinor(ctx context.Context, client cincinnati.Client, cincinnatiURI *url.URL, channelStability string, currentMinor semver.Version, alreadyChecked map[string]bool) (bool, error) {
	channel := formatChannel(channelStability, currentMinor)
	dotZero := semver.Version{Major: currentMinor.Major, Minor: currentMinor.Minor}

	_, discovered, _, err := client.GetUpdates(ctx, cloneURL(cincinnatiURI), "multi", "multi", channel, dotZero)
	if err != nil {
		return false, nil
	}

	for _, rel := range discovered {
		ver, parseErr := semver.Parse(rel.Version)
		if parseErr != nil || alreadyChecked[ver.String()] {
			continue
		}
		if ver.Major != currentMinor.Major || ver.Minor != currentMinor.Minor {
			continue
		}
		targets, err := getGatewayTargets(ctx, client, cincinnatiURI, channelStability, currentMinor, ver)
		if err != nil {
			return false, err
		}
		if len(targets) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// getGatewayTargets returns the versions in the next minor that ver can
// upgrade to via the next minor's Cincinnati channel. Returns nil if ver is
// not in the next minor's channel or the channel does not exist.
func getGatewayTargets(ctx context.Context, client cincinnati.Client, cincinnatiURI *url.URL, channelStability string, currentMinor semver.Version, ver semver.Version) ([]semver.Version, error) {
	nextMajor, nextMinorNum := nextMinorVersion(currentMinor)
	nextChannel := formatChannel(channelStability, semver.Version{Major: nextMajor, Minor: nextMinorNum})

	_, updates, _, err := client.GetUpdates(ctx, cloneURL(cincinnatiURI), "multi", "multi", nextChannel, ver)
	if isVersionOrChannelNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checking gateway targets from %s in %s: %w", ver, nextChannel, err)
	}

	var targets []semver.Version
	for _, rel := range updates {
		v, parseErr := semver.Parse(rel.Version)
		if parseErr != nil {
			continue
		}
		if v.Major == nextMajor && v.Minor == nextMinorNum {
			targets = append(targets, v)
		}
	}
	return targets, nil
}

// nextMinorVersion returns the major and minor numbers for the next minor
// version, handling the 4.22 → 5.0 transition.
func nextMinorVersion(v semver.Version) (uint64, uint64) {
	if v.Major == 4 && v.Minor == 22 {
		return 5, 0
	}
	return v.Major, v.Minor + 1
}

func formatChannel(channelStability string, minor semver.Version) string {
	return fmt.Sprintf("%s-%d.%d", channelStability, minor.Major, minor.Minor)
}

// activeVersionsFromHostedCluster extracts the versions from the HostedCluster's
// control plane version history. All history entries (Completed and Partial)
// are included because an in-progress version is still active on the cluster
// and must have upgrade paths from it.
func activeVersionsFromHostedCluster(hc *v1beta1.HostedCluster) []semver.Version {
	if hc == nil {
		return nil
	}
	var versions []semver.Version
	for _, entry := range hc.Status.ControlPlaneVersion.History {
		v, err := semver.Parse(entry.Version)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	return versions
}

// findCandidates returns the set of versions in the desired minor that are
// reachable from all active versions. For install (no active versions), it
// queries from the .0 release and includes .0 itself as a candidate.
func findCandidates(ctx context.Context, client cincinnati.Client, baseURI *url.URL, channel string, desiredMinor semver.Version, activeVersions []semver.Version) ([]semver.Version, error) {
	if len(activeVersions) == 0 {
		return findCandidatesForInstall(ctx, client, baseURI, channel, desiredMinor)
	}
	return findCandidatesForUpgrade(ctx, client, baseURI, channel, desiredMinor, activeVersions)
}

func findCandidatesForInstall(ctx context.Context, client cincinnati.Client, baseURI *url.URL, channel string, desiredMinor semver.Version) ([]semver.Version, error) {
	dotZero := semver.Version{Major: desiredMinor.Major, Minor: desiredMinor.Minor}
	_, updates, _, err := client.GetUpdates(ctx, cloneURL(baseURI), "multi", "multi", channel, dotZero)
	if err != nil {
		return nil, fmt.Errorf("querying Cincinnati for %s in %s: %w", dotZero, channel, err)
	}

	candidates := []semver.Version{dotZero}
	for _, rel := range updates {
		v, err := semver.Parse(rel.Version)
		if err != nil {
			continue
		}
		if v.Major == desiredMinor.Major && v.Minor == desiredMinor.Minor {
			candidates = append(candidates, v)
		}
	}
	return candidates, nil
}

func findCandidatesForUpgrade(ctx context.Context, client cincinnati.Client, baseURI *url.URL, channel string, desiredMinor semver.Version, activeVersions []semver.Version) ([]semver.Version, error) {
	type candidateEntry struct {
		version semver.Version
		count   int
	}
	candidatesByVersion := map[string]*candidateEntry{}

	for _, activeVersion := range activeVersions {
		_, updates, _, err := client.GetUpdates(ctx, cloneURL(baseURI), "multi", "multi", channel, activeVersion)
		if err != nil {
			return nil, fmt.Errorf("querying Cincinnati for upgrades from %s in %s: %w", activeVersion, channel, err)
		}

		for _, rel := range updates {
			v, err := semver.Parse(rel.Version)
			if err != nil {
				continue
			}
			if v.Major != desiredMinor.Major || v.Minor != desiredMinor.Minor {
				continue
			}
			entry, ok := candidatesByVersion[v.String()]
			if !ok {
				entry = &candidateEntry{version: v}
				candidatesByVersion[v.String()] = entry
			}
			entry.count++
		}
	}

	var candidates []semver.Version
	for _, entry := range candidatesByVersion {
		if entry.count == len(activeVersions) {
			candidates = append(candidates, entry.version)
		}
	}
	return candidates, nil
}

// isVersionOrChannelNotFound returns true when the Cincinnati error indicates
// either the queried version is not in the channel's graph (VersionNotFound)
// or the channel itself does not exist (ResponseFailed from a non-200 status).
func isVersionOrChannelNotFound(err error) bool {
	var cErr *cvocincinnati.Error
	if !errors.As(err, &cErr) {
		return false
	}
	return cErr.Reason == "VersionNotFound" || cErr.Reason == "ResponseFailed"
}

func cloneURL(u *url.URL) *url.URL {
	clone := *u
	return &clone
}
