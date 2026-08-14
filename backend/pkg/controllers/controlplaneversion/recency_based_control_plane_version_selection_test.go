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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func makeNode(version string) node {
	return node{
		Version: version,
		Payload: fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-multi", version),
	}
}

func jsonBody(v interface{}) io.ReadCloser {
	b, _ := json.Marshal(v)
	return io.NopCloser(strings.NewReader(string(b)))
}

func TestSelectControlPlaneVersionInstall(t *testing.T) {
	tests := []struct {
		name            string
		graph           graph
		offset          uint
		expectedVersion string
		expectedError   string
	}{
		{
			name: "returns highest version with offset 0",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1"),
					makeNode("4.20.2"),
					makeNode("4.20.3"),
				},
			},
			expectedVersion: "4.20.3",
		},
		{
			name: "returns penultimate version with offset 1",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1"),
					makeNode("4.20.2"),
					makeNode("4.20.3"),
				},
			},
			offset:          1,
			expectedVersion: "4.20.2",
		},
		{
			name: "excessive offset",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1"),
				},
			},
			offset:        1,
			expectedError: "1 releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20, which is not enough for the requested 1 offset",
		},
		{
			name:          "empty graph",
			expectedError: "no releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20",
		},
		{
			// The channel graph lists prior-minor nodes; the offset must not spill
			// into them once the channel minor is exhausted.
			name: "offset does not spill into an older minor",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1"),
					makeNode("4.19.10"),
					makeNode("4.19.9"),
				},
			},
			offset:        1,
			expectedError: "1 releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20, which is not enough for the requested 1 offset",
		},
		{
			// A graph with no node in the channel's own minor yields no releases,
			// rather than returning a version from an older minor.
			name: "no releases in the channel minor",
			graph: graph{
				Nodes: []node{
					makeNode("4.19.9"),
					makeNode("4.19.10"),
				},
			},
			expectedError: "no releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20",
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

			version, err := SelectControlPlaneVersion(context.Background(), roundTripper, nil, "fast-4.20", tt.offset)
			if len(tt.expectedError) == 0 && err != nil {
				t.Error(err)
			} else if len(tt.expectedError) > 0 && err == nil {
				t.Errorf("expected error %q, but got %v", tt.expectedError, err)
			} else if err != nil && err.Error() != tt.expectedError {
				t.Errorf("expected error %q, but got %q", tt.expectedError, err)
			}
			if len(tt.expectedVersion) == 0 && version != nil {
				t.Errorf("expected nil version, got %v", version)
			} else if len(tt.expectedVersion) > 0 && version == nil {
				t.Errorf("expected version %q, but got %v", tt.expectedVersion, version)
			} else if version != nil && version.Version != tt.expectedVersion {
				t.Errorf("expected version %q, but got %v", tt.expectedVersion, version)
			}
		})
	}
}
