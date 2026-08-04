package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

func TestSelectControlPlaneVersionInstall(t *testing.T) {
	tests := []struct {
		name            string
		graph           graph
		expectedVersion string
		expectedError   string
	}{
		{
			name: "returns highest version when channel membership ties",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1", ""),
					makeNode("4.20.2", ""),
					makeNode("4.20.3", ""),
				},
			},
			expectedVersion: "4.20.3",
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1", "stable-4.20,stable-4.21"),
					makeNode("4.20.2", "stable-4.20,stable-4.21"),
					makeNode("4.20.3", "stable-4.20"), // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
				},
			},
			expectedVersion: "4.20.2", // this leaves the 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.21 (in case the cluster wants to update to 4.21) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name: "filter rejects highest, returns an older release that most improves channel connectivity",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1", "stable-4.20,stable-4.21,stable-4.22"),
					makeNode("4.20.2", "stable-4.20,stable-4.21"), // newer than the most recent 4.21/4.22 pair, so waiting on a newer pair.
					makeNode("4.20.3", "stable-4.20"),             // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
				},
			},
			expectedVersion: "4.20.1", // this leaves the 4.20.2 and 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.22 (in case the cluster wants to update through 4.21 to 4.22) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name: "filter rejects highest, returns an older release that improves channel connectivity, but only cares about the target stability level",
			graph: graph{
				Nodes: []node{
					makeNode("4.20.1", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21,candidate-4.22"),
					makeNode("4.20.2", "stable-4.20,stable-4.21,candidate-4.20,candidate-4.21"),
					makeNode("4.20.3", "stable-4.20,candidate-4.20,candidate-4.21"), // newer than the most recent 4.21 in stable-4.21, so waiting on a newer 4.21.z.
				},
			},
			expectedVersion: "4.20.2", // this leaves the 4.20.3 fixes on the table, with installPreferFeatureConnectivityOverPatchFixes asserting the ARO policy that connectivity to 4.21 (in case the cluster wants to update to 4.21) outranks the importance of bug-fixes in stable-4.20.
		},
		{
			name:          "empty graph",
			graph:         graph{Nodes: []node{}},
			expectedError: "no install targets found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=stable-0.0.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roundTripper := func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       jsonBody(tt.graph),
				}, nil
			}
			version, err := SelectControlPlaneVersion(context.Background(), "stable", semver.Version{}, roundTripper, nil, nil)
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
