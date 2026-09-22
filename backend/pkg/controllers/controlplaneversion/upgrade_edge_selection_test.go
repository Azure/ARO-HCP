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
	"net/http"
	"testing"

	"github.com/blang/semver/v4"
)

// makeGraph builds a graph from node versions and edges expressed as version-string
// pairs, so the tests read as update paths rather than as node indexes.
func makeGraph(t *testing.T, versions []string, edges ...[2]string) graph {
	t.Helper()
	result := graph{Nodes: make([]node, 0, len(versions))}
	indexOf := map[string]int{}
	for i, version := range versions {
		result.Nodes = append(result.Nodes, makeNode(version))
		indexOf[version] = i
	}
	for _, edge := range edges {
		from, ok := indexOf[edge[0]]
		if !ok {
			t.Fatalf("edge references unknown node %q", edge[0])
		}
		to, ok := indexOf[edge[1]]
		if !ok {
			t.Fatalf("edge references unknown node %q", edge[1])
		}
		result.Edges = append(result.Edges, [2]int{from, to})
	}
	return result
}

func TestSelectUpgradeEdge(t *testing.T) {
	const channelURL = "https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=candidate-4.21"

	tests := []struct {
		name              string
		graph             graph
		installOffset     uint
		expectedInstall   string
		expectedTarget    string
		expectedError     string
		expectedErrorType error
	}{
		{
			// The whole point of selecting by edge: 4.20.39 is the channel tip but has no
			// path into 4.21, so installing there would make the upgrade impossible.
			name: "skips a tip release that has no edge into the target minor",
			graph: makeGraph(t,
				[]string{"4.20.37", "4.20.38", "4.20.39", "4.21.33", "4.21.34"},
				[2]string{"4.20.37", "4.21.33"},
				[2]string{"4.20.38", "4.21.34"},
			),
			expectedInstall: "4.20.38",
			expectedTarget:  "4.21.34",
		},
		{
			name: "picks the newest target reachable from the chosen install",
			graph: makeGraph(t,
				[]string{"4.20.39", "4.21.30", "4.21.33", "4.21.34"},
				[2]string{"4.20.39", "4.21.30"},
				[2]string{"4.20.39", "4.21.33"},
			),
			expectedInstall: "4.20.39",
			// 4.21.34 is newer but unreachable from 4.20.39, so it must not be selected.
			expectedTarget: "4.21.33",
		},
		{
			name: "offset skips the newest connected install",
			graph: makeGraph(t,
				[]string{"4.20.37", "4.20.38", "4.21.34"},
				[2]string{"4.20.37", "4.21.34"},
				[2]string{"4.20.38", "4.21.34"},
			),
			installOffset:   1,
			expectedInstall: "4.20.37",
			expectedTarget:  "4.21.34",
		},
		{
			name: "ignores edges that stay within the install minor",
			graph: makeGraph(t,
				[]string{"4.20.38", "4.20.39", "4.21.34"},
				[2]string{"4.20.38", "4.20.39"},
			),
			expectedError:     "update channel recommends no update between the requested minors: no update from any of the 2 4.20 release(s) into the 1 4.21 release(s) in " + channelURL,
			expectedErrorType: ErrNoUpgradeEdge,
		},
		{
			// Both minors exist but nothing connects them: a real broken upgrade path,
			// which must be distinguishable from a minor that is not published yet.
			name: "no edges into the target minor",
			graph: makeGraph(t,
				[]string{"4.20.39", "4.21.34"},
			),
			expectedError:     "update channel recommends no update between the requested minors: no update from any of the 1 4.20 release(s) into the 1 4.21 release(s) in " + channelURL,
			expectedErrorType: ErrNoUpgradeEdge,
		},
		{
			// How a minor upstream has not branched yet reads, e.g. candidate-4.23 today.
			name: "target minor has no releases",
			graph: makeGraph(t,
				[]string{"4.20.38", "4.20.39"},
			),
			expectedError:     "update channel lists no release in the requested minor: " + channelURL + " lists 2 release(s) in 4.20 and 0 in 4.21",
			expectedErrorType: ErrMinorNotPublished,
		},
		{
			name: "install minor has no releases",
			graph: makeGraph(t,
				[]string{"4.21.34"},
			),
			expectedError:     "update channel lists no release in the requested minor: " + channelURL + " lists 0 release(s) in 4.20 and 1 in 4.21",
			expectedErrorType: ErrMinorNotPublished,
		},
		{
			name: "offset larger than the number of connected installs",
			graph: makeGraph(t,
				[]string{"4.20.39", "4.21.34"},
				[2]string{"4.20.39", "4.21.34"},
			),
			installOffset: 1,
			expectedError: "1 4.20 release(s) in " + channelURL + " have a recommended update into 4.21, which is not enough for the requested 1 offset",
		},
		{
			// Unparseable node versions must be skipped rather than panic the selection.
			name: "tolerates an unparseable node version",
			graph: makeGraph(t,
				[]string{"not-a-version", "4.20.39", "4.21.34"},
				[2]string{"4.20.39", "4.21.34"},
				[2]string{"not-a-version", "4.21.34"},
			),
			expectedInstall: "4.20.39",
			expectedTarget:  "4.21.34",
		},
		{
			name: "ignores an edge index outside the node list",
			graph: graph{
				Nodes: []node{makeNode("4.20.39"), makeNode("4.21.34")},
				Edges: [][2]int{{0, 1}, {0, 7}, {-1, 1}},
			},
			expectedInstall: "4.20.39",
			expectedTarget:  "4.21.34",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTripper := func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       jsonBody(tt.graph),
				}, nil
			}

			edge, err := SelectUpgradeEdge(context.Background(), roundTripper, nil, "candidate-4.21",
				semver.Version{Major: 4, Minor: 20}, tt.installOffset)

			switch {
			case len(tt.expectedError) == 0 && err != nil:
				t.Fatal(err)
			case len(tt.expectedError) > 0 && err == nil:
				t.Fatalf("expected error %q, but got none", tt.expectedError)
			case err != nil && err.Error() != tt.expectedError:
				t.Fatalf("expected error %q, but got %q", tt.expectedError, err)
			case err != nil:
				if tt.expectedErrorType != nil && !errors.Is(err, tt.expectedErrorType) {
					t.Errorf("expected error to be %v, but got %v", tt.expectedErrorType, err)
				}
				return
			}

			if edge.Install.Version != tt.expectedInstall {
				t.Errorf("expected install %q, but got %q", tt.expectedInstall, edge.Install.Version)
			}
			if edge.Target.Version != tt.expectedTarget {
				t.Errorf("expected target %q, but got %q", tt.expectedTarget, edge.Target.Version)
			}
			if len(edge.Install.Image) == 0 || len(edge.Target.Image) == 0 {
				t.Errorf("expected both releases to carry a payload image, got install=%q target=%q",
					edge.Install.Image, edge.Target.Image)
			}
		})
	}
}
