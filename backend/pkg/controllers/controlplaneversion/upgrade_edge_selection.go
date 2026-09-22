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

package controlplaneversion

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"
)

var (
	// ErrMinorNotPublished indicates the channel lists no release at all in the install
	// or the target minor, so there is no upgrade to select. A minor that upstream has
	// not branched yet reads this way, which is a normal state rather than a defect.
	ErrMinorNotPublished = errors.New("update channel lists no release in the requested minor")

	// ErrNoUpgradeEdge indicates the channel lists releases in both minors but
	// recommends no update between them. Unlike ErrMinorNotPublished this means a
	// y-stream upgrade that should be possible is not, either because upstream has not
	// published the edges yet or because the path was withdrawn.
	ErrNoUpgradeEdge = errors.New("update channel recommends no update between the requested minors")
)

// UpgradeEdge is a y-stream upgrade the update graph recommends: the release to
// install, and the release to upgrade it to.
type UpgradeEdge struct {
	Install configv1.Release
	Target  configv1.Release
}

// SelectUpgradeEdge picks an install release in installMinor and an upgrade target in
// the channel's own minor such that the update graph recommends an update between the
// two. The channel is the target minor's channel ("<group>-<major>.<minor>", e.g.
// "candidate-4.21"); its graph also lists the prior minor's nodes and the edges leading
// into the target minor, which is the same data the RP's admission check consults when
// accepting or rejecting a y-stream upgrade.
//
// Resolving each minor to its channel tip independently — the obvious approach — picks
// the two nodes least likely to be connected. Upstream publishes a z-stream as a node in
// the next minor's channel before it publishes that node's outgoing edges, so edge
// fan-out decays toward the tip and the newest release often has no path into the next
// minor at all. Selecting by edge instead makes the pair valid by construction.
//
// install is the newest release in installMinor with at least one recommended update
// into the channel's minor, skipping installOffset such releases from the newest (the
// same offset convention as SelectControlPlaneVersion, so the stable channel group can
// avoid a brand-new z-stream). target is the newest release in the channel's minor
// reachable from the chosen install.
//
// It returns ErrMinorNotPublished if either minor has no releases in the channel, and
// ErrNoUpgradeEdge if both minors have releases but nothing connects them. Both are
// deterministic for a given graph and are not worth retrying.
func SelectUpgradeEdge(ctx context.Context, roundTripper RoundTrip, updateService *url.URL, channel string, installMinor semver.Version, installOffset uint) (*UpgradeEdge, error) {
	graph, updateService, err := cincinnatiGraph(ctx, roundTripper, updateService, userAgent, channel)
	if err != nil {
		return nil, err
	}

	targetMinor, err := semver.ParseTolerant(channel[strings.LastIndex(channel, "-")+1:])
	if err != nil {
		return nil, fmt.Errorf("parse major.minor from channel %q: %w", channel, err)
	}

	// Index nodes by parsed version so edges can be filtered by minor. Nodes whose
	// version does not parse are skipped, matching filterReleasesToChannelMinor.
	versions := make([]*semver.Version, len(graph.Nodes))
	installCount, targetCount := 0, 0
	for i, graphNode := range graph.Nodes {
		version, err := semver.ParseTolerant(graphNode.Version)
		if err != nil {
			continue
		}
		versions[i] = &version
		switch {
		case inMinor(version, installMinor):
			installCount++
		case inMinor(version, targetMinor):
			targetCount++
		}
	}

	// A minor with no releases at all is upstream not having branched it yet, which is
	// reported separately so callers can tell it apart from a genuinely missing path.
	if installCount == 0 || targetCount == 0 {
		return nil, fmt.Errorf("%w: %s lists %d release(s) in %d.%d and %d in %d.%d",
			ErrMinorNotPublished, updateService,
			installCount, installMinor.Major, installMinor.Minor, targetCount, targetMinor.Major, targetMinor.Minor)
	}

	// Collect, for every install-minor node, the target-minor nodes it can update to.
	targetsByInstall := map[int][]int{}
	for _, edge := range graph.Edges {
		from, to := edge[0], edge[1]
		if from < 0 || from >= len(versions) || to < 0 || to >= len(versions) {
			continue
		}
		if versions[from] == nil || versions[to] == nil {
			continue
		}
		if !inMinor(*versions[from], installMinor) || !inMinor(*versions[to], targetMinor) {
			continue
		}
		targetsByInstall[from] = append(targetsByInstall[from], to)
	}

	if len(targetsByInstall) == 0 {
		return nil, fmt.Errorf("%w: no update from any of the %d %d.%d release(s) into the %d %d.%d release(s) in %s",
			ErrNoUpgradeEdge,
			installCount, installMinor.Major, installMinor.Minor,
			targetCount, targetMinor.Major, targetMinor.Minor, updateService)
	}

	installs := make([]int, 0, len(targetsByInstall))
	for i := range targetsByInstall {
		installs = append(installs, i)
	}
	sortIndexesByVersionDescending(installs, versions)

	if int(installOffset) >= len(installs) {
		return nil, fmt.Errorf("%d %d.%d release(s) in %s have a recommended update into %d.%d, which is not enough for the requested %d offset",
			len(installs), installMinor.Major, installMinor.Minor, updateService, targetMinor.Major, targetMinor.Minor, installOffset)
	}

	install := installs[installOffset]
	targets := targetsByInstall[install]
	sortIndexesByVersionDescending(targets, versions)

	return &UpgradeEdge{
		Install: nodeToRelease(graph.Nodes[install]),
		Target:  nodeToRelease(graph.Nodes[targets[0]]),
	}, nil
}

// inMinor reports whether version belongs to the given major.minor line.
func inMinor(version, minor semver.Version) bool {
	return version.Major == minor.Major && version.Minor == minor.Minor
}

// sortIndexesByVersionDescending sorts indexes into versions so the newest release
// comes first. Every index must refer to a non-nil entry.
func sortIndexesByVersionDescending(indexes []int, versions []*semver.Version) {
	slices.SortFunc(indexes, func(a, b int) int {
		return -versions[a].Compare(*versions[b])
	})
}

func nodeToRelease(graphNode node) configv1.Release {
	return configv1.Release{
		Version: graphNode.Version,
		Image:   graphNode.Payload,
	}
}
