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

package testserver

import (
	"encoding/json"
	"fmt"

	"github.com/blang/semver/v4"
)

type graphNode struct {
	Version  string            `json:"version"`
	Payload  string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type graphJSON struct {
	Nodes []graphNode `json:"nodes"`
	Edges [][2]int    `json:"edges"`
}

// Graph is a declarative builder for Cincinnati update graphs.
// Each graph represents the set of versions and upgrade edges that
// the Cincinnati server returns for a given channel query.
type Graph struct {
	nodes   []graphNode
	nodeIdx map[string]int
	edges   [][2]int
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodeIdx: make(map[string]int),
	}
}

func (g *Graph) addNode(version string) int {
	if idx, ok := g.nodeIdx[version]; ok {
		return idx
	}
	if _, err := semver.Parse(version); err != nil {
		panic(fmt.Sprintf("invalid semver %q: %v", version, err))
	}
	idx := len(g.nodes)
	g.nodes = append(g.nodes, graphNode{
		Version: version,
		Payload: "quay.io/openshift-release-dev/ocp-release:" + version + "-multi",
	})
	g.nodeIdx[version] = idx
	return idx
}

// Versions adds version nodes to the graph. Versions that already exist are
// silently ignored. Returns the graph for chaining.
func (g *Graph) Versions(versions ...string) *Graph {
	for _, v := range versions {
		g.addNode(v)
	}
	return g
}

// Edges adds directed upgrade edges from the first argument to all subsequent
// arguments. All versions are added as nodes if not already present.
// For example, Edges("4.19.10", "4.19.15", "4.19.18") means version 4.19.10
// can upgrade to 4.19.15 and 4.19.18.
func (g *Graph) Edges(from string, to ...string) *Graph {
	fromIdx := g.addNode(from)
	for _, t := range to {
		toIdx := g.addNode(t)
		g.edges = append(g.edges, [2]int{fromIdx, toIdx})
	}
	return g
}

func (g *Graph) marshal() ([]byte, error) {
	nodes := g.nodes
	if nodes == nil {
		nodes = []graphNode{}
	}
	edges := g.edges
	if edges == nil {
		edges = [][2]int{}
	}
	return json.Marshal(graphJSON{
		Nodes: nodes,
		Edges: edges,
	})
}
