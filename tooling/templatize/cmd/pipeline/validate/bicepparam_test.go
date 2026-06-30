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

package validate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pipelines/topology"
)

func TestCollectBicepparamFiles(t *testing.T) {
	topo, err := topology.LoadCombined([]string{"testdata/topology.yaml"})
	require.NoError(t, err)

	tests := []struct {
		name     string
		service  topology.Service
		expected []string
	}{
		{
			name:    "ARM step collects bicepparam file",
			service: topo.Services[0], // TestSvcA
			expected: []string{
				filepath.Join("testdata", "svc-a", "network.tmpl.bicepparam"),
				filepath.Join("testdata", "svc-b", "cluster.tmpl.bicepparam"),
			},
		},
		{
			name:     "Shell-only service collects nothing",
			service:  topo.Services[1], // TestSvcC
			expected: []string{},
		},
		{
			name:     "empty pipeline path collects nothing",
			service:  topology.Service{ServiceGroup: "NoPipeline"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := sets.New[string]()
			err := collectBicepparamFiles(topo, tt.service, files)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, files.UnsortedList())
		})
	}
}

func TestValidateBicepparamTemplates(t *testing.T) {
	tests := []struct {
		name         string
		topoFile     string
		expectError  bool
		errorContain string
	}{
		{
			name:        "valid templates pass",
			topoFile:    "testdata/topology.yaml",
			expectError: false,
		},
		{
			name:         "invalid templates fail",
			topoFile:     "testdata/topology-invalid.yaml",
			expectError:  true,
			errorContain: "range loop not allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topo, err := topology.LoadCombined([]string{tt.topoFile})
			require.NoError(t, err)

			opts := &ValidationOptions{
				completedValidationOptions: &completedValidationOptions{
					Topology: topo,
				},
			}

			ctx := logr.NewContext(context.Background(), logr.Discard())
			err = opts.ValidateBicepparamTemplates(ctx)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContain)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
