// Copyright 2025 Microsoft Corporation
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

package release

import (
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

func TestOutputReports(t *testing.T) {
	fixedTime := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	// Sample test data
	backendWorkloads := []WorkloadInfo{
		{
			Name:         "backend-deployment",
			Namespace:    "aro-hcp",
			Kind:         "Deployment",
			DesiredImage: "registry.redhat.io/aro-hcp/backend:v1.2.3",
			CurrentImage: "registry.redhat.io/aro-hcp/backend:v1.2.3",
		},
	}
	frontendWorkloads := []WorkloadInfo{
		{
			Name:         "frontend-deployment",
			Namespace:    "aro-hcp",
			Kind:         "Deployment",
			DesiredImage: "registry.redhat.io/aro-hcp/frontend:v2.1.0",
			CurrentImage: "registry.redhat.io/aro-hcp/frontend:v2.0.9",
		},
	}

	backendRelease := NewComponentRelease("backend", fixedTime, backendWorkloads)
	backendRelease.Metadata.CreationTimestamp = fixedTime

	frontendRelease := NewComponentRelease("frontend", fixedTime, frontendWorkloads)
	frontendRelease.Metadata.CreationTimestamp = fixedTime

	reports := []ComponentRelease{backendRelease, frontendRelease}

	tests := []struct {
		name               string
		format             string
		aroHcpCommit       string
		sdpPipelinesCommit string
		expectError        bool
	}{
		{
			name:               "YAML output format",
			format:             "yaml",
			aroHcpCommit:       "abc123def456",
			sdpPipelinesCommit: "xyz789uvw012",
			expectError:        false,
		},
		{
			name:               "JSON output format",
			format:             "json",
			aroHcpCommit:       "abc123def456",
			sdpPipelinesCommit: "xyz789uvw012",
			expectError:        false,
		},
		{
			name:               "Empty commit values",
			format:             "yaml",
			aroHcpCommit:       "",
			sdpPipelinesCommit: "",
			expectError:        false,
		},
		{
			name:               "Unsupported format",
			format:             "xml",
			aroHcpCommit:       "abc123def456",
			sdpPipelinesCommit: "xyz789uvw012",
			expectError:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			// Mock time.Now() for consistent timestamps in golden files
			// We'll need to modify the actual output to have a fixed timestamp
			err := OutputReports(reports, tt.format, tt.aroHcpCommit, tt.sdpPipelinesCommit)

			// Restore stdout and read captured output
			w.Close()
			os.Stdout = oldStdout
			output, _ := io.ReadAll(r)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Parse the output to replace the timestamp with a fixed one for golden comparison
			var clusterRelease ClusterComponentRelease
			if tt.format == "json" {
				require.NoError(t, json.Unmarshal(output, &clusterRelease))
			} else {
				require.NoError(t, yaml.Unmarshal(output, &clusterRelease))
			}

			// Set fixed timestamp for golden comparison
			clusterRelease.Metadata.CreationTimestamp = fixedTime

			// Use golden fixture for comparison
			testutil.CompareWithFixture(t, clusterRelease, testutil.WithExtension("."+tt.format))
		})
	}
}

func TestOutputYAML(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Name:         "test-deployment",
			Namespace:    "test-ns",
			Kind:         "Deployment",
			DesiredImage: "nginx:latest",
			CurrentImage: "nginx:latest",
		},
	}

	component := NewComponentRelease("test-component", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), workloads)

	components := []ComponentRelease{component}

	testData := NewClusterComponentRelease("cluster-component-releases", "abc123def456", "xyz789uvw012", components)
	// Override timestamp for golden fixture consistency
	testData.Metadata.CreationTimestamp = time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := outputYAML(testData)

	// Restore stdout and read captured output
	w.Close()
	os.Stdout = oldStdout
	output, _ := io.ReadAll(r)

	require.NoError(t, err)

	// Use golden fixture for comparison
	testutil.CompareWithFixture(t, string(output), testutil.WithExtension(".yaml"))
}

func TestOutputJSON(t *testing.T) {
	workloads := []WorkloadInfo{
		{
			Name:         "test-deployment",
			Namespace:    "test-ns",
			Kind:         "Deployment",
			DesiredImage: "nginx:latest",
			CurrentImage: "nginx:latest",
		},
	}

	component := NewComponentRelease("test-component", time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC), workloads)

	components := []ComponentRelease{component}

	testData := NewClusterComponentRelease("cluster-component-releases", "abc123def456", "xyz789uvw012", components)
	// Override timestamp for golden fixture consistency
	testData.Metadata.CreationTimestamp = time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := outputJSON(testData)

	// Restore stdout and read captured output
	w.Close()
	os.Stdout = oldStdout
	output, _ := io.ReadAll(r)

	require.NoError(t, err)

	// Use golden fixture for comparison
	testutil.CompareWithFixture(t, string(output), testutil.WithExtension(".json"))
}
