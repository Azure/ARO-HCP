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
			expectedError: "1 releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20, which is not enough for the requested 1 offset.",
		},
		{
			name:          "empty graph",
			expectedError: "no releases found in https://api.openshift.com/api/upgrades_info/graph?arch=multi&channel=fast-4.20.",
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
