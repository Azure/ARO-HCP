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

package cmd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRegion(t *testing.T) {
	tests := []struct {
		name            string
		region          string
		wantErrContains string
	}{
		{
			name:   "valid region eastus",
			region: "eastus",
		},
		{
			name:   "valid region westus3",
			region: "westus3",
		},
		{
			name:   "valid region centralus",
			region: "centralus",
		},
		{
			name:   "valid region northeurope",
			region: "northeurope",
		},
		{
			name:   "uppercase is normalized",
			region: "EastUS",
		},
		{
			name:            "empty region",
			region:          "",
			wantErrContains: "region is required",
		},
		{
			name:            "region with spaces",
			region:          "West US 3",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with hyphens",
			region:          "us-east-1",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with single quote",
			region:          "east'us",
			wantErrContains: "invalid region",
		},
		{
			name:            "region with semicolon",
			region:          "eastus;drop",
			wantErrContains: "invalid region",
		},
		{
			name:            "single character region",
			region:          "e",
			wantErrContains: "invalid region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.Region = tt.region
			opts.ClusterTypes = []string{"svc-cluster"}

			validated, err := opts.Validate(context.Background())

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Regexp(t, `^[a-z][a-z0-9]+$`, validated.Region)
		})
	}
}

func TestValidateClusterTypes(t *testing.T) {
	tests := []struct {
		name            string
		clusterTypes    []string
		wantErrContains string
		wantTypes       []string
	}{
		{
			name:         "valid single type",
			clusterTypes: []string{"svc-cluster"},
			wantTypes:    []string{"svc-cluster"},
		},
		{
			name:         "valid multiple types",
			clusterTypes: []string{"svc-cluster", "mgmt-cluster"},
			wantTypes:    []string{"svc-cluster", "mgmt-cluster"},
		},
		{
			name:         "valid type with dots and underscores",
			clusterTypes: []string{"my_cluster.v2"},
			wantTypes:    []string{"my_cluster.v2"},
		},
		{
			name:         "whitespace is trimmed",
			clusterTypes: []string{" svc-cluster ", "  mgmt-cluster"},
			wantTypes:    []string{"svc-cluster", "mgmt-cluster"},
		},
		{
			name:            "empty list",
			clusterTypes:    []string{},
			wantErrContains: "cluster-types is required",
		},
		{
			name:            "empty element",
			clusterTypes:    []string{"svc-cluster", ""},
			wantErrContains: "must not contain empty values",
		},
		{
			name:            "whitespace-only element",
			clusterTypes:    []string{"svc-cluster", "  "},
			wantErrContains: "must not contain empty values",
		},
		{
			name:            "type with spaces",
			clusterTypes:    []string{"svc cluster"},
			wantErrContains: "invalid cluster-type",
		},
		{
			name:            "type with single quote",
			clusterTypes:    []string{"svc'cluster"},
			wantErrContains: "invalid cluster-type",
		},
		{
			name:            "type with semicolon",
			clusterTypes:    []string{"type;drop"},
			wantErrContains: "invalid cluster-type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ClusterTypes = tt.clusterTypes
			opts.Region = "eastus"

			validated, err := opts.Validate(context.Background())

			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTypes, validated.ClusterTypes)
		})
	}
}
