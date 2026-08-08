package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
)

func makeNode(version string, channels string) node {
	return node{
		Version:  version,
		Payload:  fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-multi", version),
		Metadata: metadata{ChannelsString: channels},
	}
}

func jsonBody(v interface{}) io.ReadCloser {
	b, _ := json.Marshal(v)
	return io.NopCloser(strings.NewReader(string(b)))
}

func testRoundTripper(graphs map[string]graph) RoundTrip {
	return func(request *http.Request) (*http.Response, error) {
		values, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			return nil, err
		}
		channel := values.Get("channel")
		graph, ok := graphs[channel]
		if !ok {
			return nil, fmt.Errorf("requested channel %q not in test fixture %v", channel, slices.Collect(maps.Keys(graphs)))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       jsonBody(graph),
		}, nil
	}
}

func TestSelectControlPlaneVersionInstall(t *testing.T) {
	tests := []struct {
		name            string
		graphs          map[string]graph
		expectedVersion string
		expectedError   string
	}{
		{
			name: "returns highest version when channel membership ties",
			graphs: map[string]graph{
				"stable-4.20": {
					Nodes: []node{
						makeNode("4.20.1", ""),
						makeNode("4.20.2", ""),
						makeNode("4.20.3", ""),
					},
				},
			},
			expectedVersion: "4.20.3",
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity",
			graphs: map[string]graph{
				"stable-4.20": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"),
						makeNode("4.20.3", "stable-4.20"), // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
					},
				},
				"stable-4.21": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"),
					},
				},
			},
			expectedVersion: "4.20.2", // this leaves the 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.21 (in case the cluster wants to update to 4.21) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name: "filter rejects highest, returns an older release that most improves channel connectivity",
			graphs: map[string]graph{
				"stable-4.20": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21,stable-4.22"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"), // newer than the most recent 4.21/4.22 pair, so waiting on a newer pair.
						makeNode("4.20.3", "stable-4.20"),             // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
					},
				},
				"stable-4.21": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21,stable-4.22"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"), // newer than the most recent 4.21/4.22 pair, so waiting on a newer pair.
					},
				},
				"stable-4.22": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21,stable-4.22"),
					},
				},
			},
			expectedVersion: "4.20.1", // this leaves the 4.20.2 and 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.22 (in case the cluster wants to update through 4.21 to 4.22) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity, but only cares about the target stability level",
			graphs: map[string]graph{
				"stable-4.20": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21,candidate-4.22"),
						makeNode("4.20.2", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21"),
						makeNode("4.20.3", "stable-4.20,candidate-4.20,candidate-4.21"), // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
					},
				},
				"stable-4.21": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21,candidate-4.22"),
						makeNode("4.20.2", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21"),
					},
				},
			},
			expectedVersion: "4.20.2", // this leaves the 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.21 (in case the cluster wants to update to 4.21) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity, including walking future channel changes",
			graphs: map[string]graph{
				"stable-4.20": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"),
						makeNode("4.20.3", "stable-4.20"), // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
					},
				},
				"stable-4.21": {
					Nodes: []node{
						makeNode("4.20.1", "stable-4.20,stable-4.21"),
						makeNode("4.20.2", "stable-4.20,stable-4.21"),
						makeNode("4.21.0", "stable-4.21,stable-4.22"),
					},
					Edges: []edge{{Origin: 0, Destination: 2}}, // connect 4.20.1 to 4.21.0.  4.20.2 is newer than 4.21.0, so that update could have regressions vs. backported fixes, and is not supported.
				},
				"stable-4.22": {
					Nodes: []node{
						makeNode("4.21.0", "stable-4.21,stable-4.22"),
						makeNode("4.22.0", "stable-4.22"),
					},
					Edges: []edge{{Origin: 0, Destination: 1}}, // connect 4.21.0 to 4.22.0, which is a requirement before 4.22.0 is included in stable-4.22.
				},
			},
			expectedVersion: "4.20.1", // this leaves the 4.20.3 and 4.20.2 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.22 (in case the cluster wants to update through 4.21 to 4.22) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name:          "empty graph",
			graphs:        map[string]graph{"stable-4.20": {Nodes: []node{}}},
			expectedError: "no install targets found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=stable-4.20.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTripper := testRoundTripper(tt.graphs)
			version, err := SelectControlPlaneVersion(context.Background(), "stable", semver.Version{Major: 4, Minor: 20}, roundTripper, nil, nil)
			if len(tt.expectedError) == 0 && err != nil {
				t.Error(err)
			} else if len(tt.expectedError) > 0 && err == nil {
				t.Errorf("expected error %q, but got %v", tt.expectedError, err)
			} else if err != nil && err.Error() != tt.expectedError {
				t.Errorf("expected error %q, but got %q", tt.expectedError, err)
			}
			if len(tt.expectedVersion) == 0 && version != nil {
				t.Errorf("expected nil version, got %q", version)
			} else if len(tt.expectedVersion) > 0 && version == nil {
				t.Errorf("expected version %q, but got %v", tt.expectedVersion, version)
			} else if version != nil && version.String() != tt.expectedVersion {
				t.Errorf("expected version %q, but got %q", tt.expectedVersion, version)
			}
		})
	}
}
