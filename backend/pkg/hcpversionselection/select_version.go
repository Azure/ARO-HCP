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
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/cincinnati/upgradegraph"
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
// The function fetches the Cincinnati graph for channelStability starting from
// the desired minor (or the earliest active version's minor), builds a weighted
// gonum directed graph, and uses graph traversal to select the version.
//
// The selected version is the most recent z-stream that:
//   - Is a direct upgrade target from ALL active versions in the HostedCluster's
//     control plane version history (both Completed and Partial entries)
//   - Has a transitive upgrade chain: it must be a gateway to some version in
//     the next minor, and that version must itself be a gateway to the minor
//     after that, and so on until the chain terminates
//
// The chain terminates (and any candidate is acceptable) when either:
//   - The next minor has no versions in the graph, OR
//   - No version in the current minor has a direct edge to the next minor
//
// When no candidate has a valid transitive chain but the next minor is
// reachable from other versions in the desired minor, a *NoGatewayError is
// returned — the cluster must wait for Cincinnati to publish a new edge.
//
// For install (hostedCluster is nil), there are no active versions to constrain
// the selection; all versions in the desired minor are candidates.
//
// The 4.22 → 5.0 version numbering transition is handled: 5.0 is treated as
// the next minor after 4.22.
func SelectControlPlaneVersion(ctx context.Context, channelStability string, desiredYVersion semver.Version, cincinnatiURI *url.URL, _ cincinnati.Client, hostedCluster *v1beta1.HostedCluster) (*semver.Version, error) {
	desiredMinor := semver.Version{Major: desiredYVersion.Major, Minor: desiredYVersion.Minor}
	activeVersions := activeVersionsFromHostedCluster(hostedCluster)

	startMinor := desiredMinor
	for _, av := range activeVersions {
		avMinor := semver.Version{Major: av.Major, Minor: av.Minor}
		if avMinor.LT(startMinor) {
			startMinor = avMinor
		}
	}

	g, err := upgradegraph.FetchAndBuild(ctx, cincinnatiURI.String(), channelStability, startMinor)
	if err != nil {
		return nil, fmt.Errorf("build upgrade graph: %w", err)
	}

	candidates := findCandidates(g, desiredMinor, activeVersions)
	if len(candidates) == 0 {
		return nil, nil
	}

	slices.SortFunc(candidates, func(a, b semver.Version) int {
		return b.Compare(a)
	})

	streams := g.Streams()
	result := findLatestGateway(g, streams, desiredMinor, candidates)
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

// findCandidates returns versions in the desired minor that are upgrade
// targets. For install (no active versions), all versions in the minor are
// candidates. For upgrade, only versions that are direct successors of ALL
// active versions in the graph are candidates.
func findCandidates(g *upgradegraph.UpgradeGraph, desiredMinor semver.Version, activeVersions []semver.Version) []semver.Version {
	if len(activeVersions) == 0 {
		return g.VersionsInMinor(desiredMinor.Major, desiredMinor.Minor)
	}

	type candidateEntry struct {
		version semver.Version
		count   int
	}
	byVersion := map[string]*candidateEntry{}

	for _, active := range activeVersions {
		for _, succ := range g.DirectSuccessorsInMinor(active, desiredMinor.Major, desiredMinor.Minor) {
			entry, ok := byVersion[succ.String()]
			if !ok {
				entry = &candidateEntry{version: succ}
				byVersion[succ.String()] = entry
			}
			entry.count++
		}
	}

	var candidates []semver.Version
	for _, entry := range byVersion {
		if entry.count == len(activeVersions) {
			candidates = append(candidates, entry.version)
		}
	}
	return candidates
}

// findLatestGateway returns the latest candidate with a valid transitive
// upgrade chain through subsequent minors. Candidates must be sorted
// descending.
func findLatestGateway(g *upgradegraph.UpgradeGraph, streams []upgradegraph.MinorStream, currentMinor semver.Version, candidates []semver.Version) *semver.Version {
	streamIdx := -1
	for i, s := range streams {
		if s.Major == currentMinor.Major && s.Minor == currentMinor.Minor {
			streamIdx = i
			break
		}
	}

	if streamIdx == -1 || streamIdx >= len(streams)-1 {
		return &candidates[0]
	}

	next := streams[streamIdx+1]
	if !g.HasCrossMinorEdge(currentMinor.Major, currentMinor.Minor, next.Major, next.Minor) {
		return &candidates[0]
	}

	for i := range candidates {
		if hasValidForwardChain(g, streams, candidates[i], streamIdx) {
			return &candidates[i]
		}
	}

	return nil
}

// hasValidForwardChain checks whether candidate has a transitive path through
// all subsequent minor streams. It does a single BFS from the candidate and
// then verifies that each minor boundary is crossed.
func hasValidForwardChain(g *upgradegraph.UpgradeGraph, streams []upgradegraph.MinorStream, candidate semver.Version, startStreamIdx int) bool {
	reachable := g.ReachableSet(candidate)

	for i := startStreamIdx; i < len(streams)-1; i++ {
		current := streams[i]
		next := streams[i+1]

		if !g.HasCrossMinorEdge(current.Major, current.Minor, next.Major, next.Minor) {
			return true
		}

		reachesNext := false
		for _, v := range next.Versions {
			if reachable[v.String()] {
				reachesNext = true
				break
			}
		}
		if !reachesNext {
			return false
		}
	}
	return true
}

// nextMinorVersion returns the major and minor numbers for the next minor
// version, handling the 4.22 → 5.0 transition.
func nextMinorVersion(v semver.Version) (uint64, uint64) {
	if v.Major == 4 && v.Minor == 22 {
		return 5, 0
	}
	return v.Major, v.Minor + 1
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
