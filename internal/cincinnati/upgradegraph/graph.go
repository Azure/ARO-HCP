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

package upgradegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/path"
	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/traverse"
)

const DefaultGraphAPIBase = "https://api.openshift.com/api/upgrades_info/v1/graph"

// VersionNode represents an OpenShift version in the upgrade graph.
type VersionNode struct {
	id      int64
	Version semver.Version
}

func (n *VersionNode) ID() int64 { return n.id }

// UpgradeGraph is a directed weighted graph of OpenShift versions and upgrade
// edges, built from Cincinnati channel data. Edge weights are set so that edges
// leading to more recent z-streams cost less, causing shortest-path algorithms
// to prefer paths through newer versions.
type UpgradeGraph struct {
	directed  *simple.WeightedDirectedGraph
	byVersion map[string]*VersionNode
	byID      map[int64]*VersionNode
	nextID    int64
}

func New() *UpgradeGraph {
	return &UpgradeGraph{
		directed:  simple.NewWeightedDirectedGraph(0, 0),
		byVersion: make(map[string]*VersionNode),
		byID:      make(map[int64]*VersionNode),
	}
}

func (g *UpgradeGraph) Directed() graph.Directed { return g.directed }

func (g *UpgradeGraph) addNode(v semver.Version) *VersionNode {
	key := v.String()
	if n, ok := g.byVersion[key]; ok {
		return n
	}
	n := &VersionNode{id: g.nextID, Version: v}
	g.nextID++
	g.directed.AddNode(n)
	g.byVersion[key] = n
	g.byID[n.id] = n
	return n
}

func (g *UpgradeGraph) AddVersion(v semver.Version) {
	g.addNode(v)
}

func (g *UpgradeGraph) addWeightedEdge(from, to *VersionNode) {
	if g.directed.HasEdgeFromTo(from.ID(), to.ID()) {
		return
	}
	g.directed.SetWeightedEdge(g.directed.NewWeightedEdge(from, to, edgeWeight(to.Version)))
}

func (g *UpgradeGraph) AddEdge(from, to semver.Version) {
	f := g.addNode(from)
	t := g.addNode(to)
	g.addWeightedEdge(f, t)
}

// edgeWeight assigns a cost that decreases for newer z-streams so that
// shortest-path searches prefer paths through the latest versions.
func edgeWeight(to semver.Version) float64 {
	return 1.0 / float64(to.Patch+1)
}

func (g *UpgradeGraph) Versions() []semver.Version {
	versions := make([]semver.Version, 0, len(g.byVersion))
	for _, n := range g.byVersion {
		versions = append(versions, n.Version)
	}
	slices.SortFunc(versions, semver.Version.Compare)
	return versions
}

func (g *UpgradeGraph) NodeCount() int {
	return len(g.byVersion)
}

// VersionsInMinor returns all versions with the given major.minor, sorted ascending.
func (g *UpgradeGraph) VersionsInMinor(major, minor uint64) []semver.Version {
	var versions []semver.Version
	for _, n := range g.byVersion {
		if n.Version.Major == major && n.Version.Minor == minor {
			versions = append(versions, n.Version)
		}
	}
	slices.SortFunc(versions, semver.Version.Compare)
	return versions
}

// DirectSuccessorsInMinor returns the direct graph successors of src that
// are in the given major.minor, sorted ascending. This corresponds to the
// set of versions that GetUpdates would return filtered to one minor.
func (g *UpgradeGraph) DirectSuccessorsInMinor(src semver.Version, major, minor uint64) []semver.Version {
	srcNode, ok := g.byVersion[src.String()]
	if !ok {
		return nil
	}
	var result []semver.Version
	iter := g.directed.From(srcNode.ID())
	for iter.Next() {
		succ := g.byID[iter.Node().ID()]
		if succ.Version.Major == major && succ.Version.Minor == minor {
			result = append(result, succ.Version)
		}
	}
	slices.SortFunc(result, semver.Version.Compare)
	return result
}

// HasCrossMinorEdge reports whether any version in (fromMajor.fromMinor) has
// a direct edge to any version in (toMajor.toMinor).
func (g *UpgradeGraph) HasCrossMinorEdge(fromMajor, fromMinor, toMajor, toMinor uint64) bool {
	for _, n := range g.byVersion {
		if n.Version.Major != fromMajor || n.Version.Minor != fromMinor {
			continue
		}
		iter := g.directed.From(n.ID())
		for iter.Next() {
			succ := g.byID[iter.Node().ID()]
			if succ.Version.Major == toMajor && succ.Version.Minor == toMinor {
				return true
			}
		}
	}
	return false
}

// Reachable returns true if there is a directed path from src to dst.
func (g *UpgradeGraph) Reachable(src, dst semver.Version) bool {
	srcNode, ok := g.byVersion[src.String()]
	if !ok {
		return false
	}
	dstNode, ok := g.byVersion[dst.String()]
	if !ok {
		return false
	}
	if srcNode.id == dstNode.id {
		return true
	}

	bfs := traverse.BreadthFirst{}
	found := bfs.Walk(g.directed, srcNode, func(n graph.Node, _ int) bool {
		return n.ID() == dstNode.id
	})
	return found != nil
}

// ReachableSet returns the set of version strings reachable from src via BFS.
func (g *UpgradeGraph) ReachableSet(src semver.Version) map[string]bool {
	srcNode, ok := g.byVersion[src.String()]
	if !ok {
		return nil
	}
	result := map[string]bool{src.String(): true}
	bfs := traverse.BreadthFirst{}
	bfs.Walk(g.directed, srcNode, func(n graph.Node, _ int) bool {
		result[g.byID[n.ID()].Version.String()] = true
		return false
	})
	return result
}

// PathBetween returns the lowest-cost directed path from src to dst using A*,
// which prefers edges leading to newer z-streams. Returns nil if unreachable.
func (g *UpgradeGraph) PathBetween(src, dst semver.Version) []semver.Version {
	srcNode, ok := g.byVersion[src.String()]
	if !ok {
		return nil
	}
	dstNode, ok := g.byVersion[dst.String()]
	if !ok {
		return nil
	}

	shortest, _ := path.AStar(srcNode, dstNode, g.directed, func(_, _ graph.Node) float64 { return 0 })
	nodes, _ := shortest.To(dstNode.ID())
	if len(nodes) == 0 {
		return nil
	}

	versions := make([]semver.Version, len(nodes))
	for i, n := range nodes {
		versions[i] = g.byID[n.ID()].Version
	}
	return versions
}

// MinorStream groups versions sharing the same major.minor.
type MinorStream struct {
	Major    uint64
	Minor    uint64
	Versions []semver.Version
	LatestZ  semver.Version
}

func (m MinorStream) String() string {
	return fmt.Sprintf("%d.%d", m.Major, m.Minor)
}

func (g *UpgradeGraph) Streams() []MinorStream {
	byMinor := map[string]*MinorStream{}
	for _, n := range g.byVersion {
		key := fmt.Sprintf("%d.%d", n.Version.Major, n.Version.Minor)
		s, ok := byMinor[key]
		if !ok {
			s = &MinorStream{Major: n.Version.Major, Minor: n.Version.Minor}
			byMinor[key] = s
		}
		s.Versions = append(s.Versions, n.Version)
	}

	streams := make([]MinorStream, 0, len(byMinor))
	for _, s := range byMinor {
		slices.SortFunc(s.Versions, semver.Version.Compare)
		s.LatestZ = s.Versions[len(s.Versions)-1]
		streams = append(streams, *s)
	}
	slices.SortFunc(streams, func(a, b MinorStream) int {
		if a.Major != b.Major {
			if a.Major < b.Major {
				return -1
			}
			return 1
		}
		if a.Minor < b.Minor {
			return -1
		}
		if a.Minor > b.Minor {
			return 1
		}
		return 0
	})
	return streams
}

// ValidationFailure describes a single upgrade path validation failure.
type ValidationFailure struct {
	Version     semver.Version
	LatestZ     semver.Version
	NextLatestZ *semver.Version
	Reason      string
}

func (f ValidationFailure) String() string {
	if f.NextLatestZ != nil {
		return fmt.Sprintf("%s -> %s (cross-minor): %s", f.Version, f.NextLatestZ, f.Reason)
	}
	return fmt.Sprintf("%s -> %s (within-minor): %s", f.Version, f.LatestZ, f.Reason)
}

// ValidationResult holds the outcome of upgrade path validation.
type ValidationResult struct {
	Streams  []MinorStream
	Failures []ValidationFailure
}

func (r *ValidationResult) OK() bool { return len(r.Failures) == 0 }

func (r *ValidationResult) String() string {
	total := 0
	for _, s := range r.Streams {
		total += len(s.Versions)
	}
	if r.OK() {
		return fmt.Sprintf("PASS: %d versions across %d streams all have valid upgrade paths", total, len(r.Streams))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "FAIL: %d issues across %d streams (%d versions total)\n", len(r.Failures), len(r.Streams), total)
	for _, f := range r.Failures {
		fmt.Fprintf(&sb, "  %s\n", f.String())
	}
	return sb.String()
}

// Validate checks the upgrade path invariant: every version in each minor
// stream can reach the latest-z of that stream, and the latest-z of each
// stream can reach the latest-z of the next stream.
func (g *UpgradeGraph) Validate() *ValidationResult {
	streams := g.Streams()
	result := &ValidationResult{Streams: streams}

	for i, stream := range streams {
		for _, v := range stream.Versions {
			if v.EQ(stream.LatestZ) {
				continue
			}
			if !g.Reachable(v, stream.LatestZ) {
				result.Failures = append(result.Failures, ValidationFailure{
					Version: v,
					LatestZ: stream.LatestZ,
					Reason:  "no path to latest-z within minor",
				})
			}
		}

		if i < len(streams)-1 {
			next := streams[i+1]
			if !g.Reachable(stream.LatestZ, next.LatestZ) {
				nextLatest := next.LatestZ
				result.Failures = append(result.Failures, ValidationFailure{
					Version:     stream.LatestZ,
					LatestZ:     stream.LatestZ,
					NextLatestZ: &nextLatest,
					Reason:      fmt.Sprintf("no cross-minor path from %s to %s", stream.String(), next.String()),
				})
			}
		}
	}

	return result
}

type graphResponse struct {
	Nodes []graphNode `json:"nodes"`
	Edges [][2]int    `json:"edges"`
}

type graphNode struct {
	Version  string            `json:"version"`
	Payload  string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func fetchChannelGraph(ctx context.Context, baseURL, channel string) (*graphResponse, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL %q: %w", baseURL, err)
	}
	q := u.Query()
	q.Set("channel", channel)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", channel, err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch graph for %s: %w", channel, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("graph for %s returned %s: %s", channel, resp.Status, strings.TrimSpace(string(body)))
	}

	var result graphResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode graph for %s: %w", channel, err)
	}
	return &result, nil
}

// FetchAndBuild queries Cincinnati for all channels in the given channel group
// starting from startMinor, advancing through subsequent minors (including the
// 4.22 to 5.0 transition) until it finds a channel with no versions.
func FetchAndBuild(ctx context.Context, baseURL, channelGroup string, startMinor semver.Version) (*UpgradeGraph, error) {
	g := New()

	major := startMinor.Major
	minor := startMinor.Minor

	for {
		channel := fmt.Sprintf("%s-%d.%d", channelGroup, major, minor)
		raw, err := fetchChannelGraph(ctx, baseURL, channel)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", channel, err)
		}
		if len(raw.Nodes) == 0 {
			break
		}

		nodeMap := make([]*VersionNode, len(raw.Nodes))
		for i, node := range raw.Nodes {
			v, parseErr := semver.Parse(node.Version)
			if parseErr != nil {
				continue
			}
			nodeMap[i] = g.addNode(v)
		}

		for _, edge := range raw.Edges {
			from, to := edge[0], edge[1]
			if from < 0 || from >= len(nodeMap) || to < 0 || to >= len(nodeMap) {
				continue
			}
			if nodeMap[from] == nil || nodeMap[to] == nil {
				continue
			}
			g.addWeightedEdge(nodeMap[from], nodeMap[to])
		}

		if major == 4 && minor == 22 {
			major = 5
			minor = 0
		} else {
			minor++
		}
	}

	return g, nil
}
