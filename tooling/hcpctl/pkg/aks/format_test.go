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

package aks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/testutil"
)

func TestFormatAKSTable(t *testing.T) {
	t.Run("empty clusters returns no clusters found message", func(t *testing.T) {
		output := FormatAKSTable([]AKSCluster{})
		assert.Equal(t, "No management clusters found", output)
	})

	t.Run("output formatting using golden test", func(t *testing.T) {
		clusters := []AKSCluster{
			{
				Name:           "cluster-1",
				SubscriptionID: "sub-123",
				ResourceGroup:  "rg-1",
				Location:       "eastus",
				ResourceID:     "/subscriptions/sub-123/resourceGroups/rg-1/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
			},
			{
				Name:           "cluster-2",
				SubscriptionID: "sub-456",
				ResourceGroup:  "rg-2",
				Location:       "westus2",
				ResourceID:     "/subscriptions/sub-456/resourceGroups/rg-2/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
			},
		}

		output := FormatAKSTable(clusters)
		testutil.CompareWithFixture(t, output, testutil.WithExtension(".txt"))
	})
}

func TestFormatAKSYAML(t *testing.T) {
	t.Run("empty clusters returns correct YAML structure", func(t *testing.T) {
		output, err := FormatAKSYAML([]AKSCluster{})
		require.NoError(t, err)

		testutil.CompareWithFixture(t, output, testutil.WithExtension(".yaml"))
	})

	t.Run("output using golden test", func(t *testing.T) {
		clusters := []AKSCluster{
			{
				Name:           "test-cluster",
				ResourceGroup:  "test-rg",
				SubscriptionID: "sub-123",
				Subscription:   "Test Subscription",
				Location:       "eastus",
				ResourceID:     "/subscriptions/sub-123/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
				State:          "Succeeded",
				Tags: map[string]string{
					"environment": "test",
					"owner":       "team-a",
				},
			},
		}

		output, err := FormatAKSYAML(clusters)
		require.NoError(t, err)

		testutil.CompareWithFixture(t, output, testutil.WithExtension(".yaml"))
	})
}

func TestFormatAKSJSON(t *testing.T) {
	t.Run("empty clusters returns correct JSON structure", func(t *testing.T) {
		output, err := FormatAKSJSON([]AKSCluster{})
		require.NoError(t, err)

		testutil.CompareWithFixture(t, output, testutil.WithExtension(".json"))
	})

	t.Run("output using golden test", func(t *testing.T) {
		clusters := []AKSCluster{
			{
				Name:           "test-cluster",
				ResourceGroup:  "test-rg",
				SubscriptionID: "sub-123",
				Subscription:   "Test Subscription",
				Location:       "eastus",
				ResourceID:     "/subscriptions/sub-123/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
				State:          "Succeeded",
				Tags: map[string]string{
					"environment": "test",
					"owner":       "team-a",
				},
			},
		}

		output, err := FormatAKSJSON(clusters)
		require.NoError(t, err)

		testutil.CompareWithFixture(t, output, testutil.WithExtension(".json"))
	})
}

func TestValidateOutputFormat(t *testing.T) {
	testCases := []struct {
		name        string
		format      string
		expected    OutputFormat
		expectError bool
	}{
		{
			name:        "valid table format",
			format:      "table",
			expected:    OutputFormatTable,
			expectError: false,
		},
		{
			name:        "valid yaml format",
			format:      "yaml",
			expected:    OutputFormatYAML,
			expectError: false,
		},
		{
			name:        "valid json format",
			format:      "json",
			expected:    OutputFormatJSON,
			expectError: false,
		},
		{
			name:        "invalid format returns error",
			format:      "xml",
			expected:    "",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ValidateOutputFormat(tc.format)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported output format")
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestFormatAKSClusters(t *testing.T) {
	clusters := []AKSCluster{
		{
			Name:           "test-cluster",
			ResourceGroup:  "test-rg",
			SubscriptionID: "sub-123",
			ResourceID:     "/subscriptions/sub-123/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
			Location:       "eastus",
		},
	}

	testCases := []struct {
		name        string
		format      OutputFormat
		expectError bool
		extension   string
	}{
		{
			name:        "table format returns formatted table",
			format:      OutputFormatTable,
			expectError: false,
			extension:   ".txt",
		},
		{
			name:        "yaml format returns formatted yaml",
			format:      OutputFormatYAML,
			expectError: false,
			extension:   ".yaml",
		},
		{
			name:        "json format returns formatted json",
			format:      OutputFormatJSON,
			expectError: false,
			extension:   ".json",
		},
		{
			name:        "invalid format returns error",
			format:      "invalid",
			expectError: true,
			extension:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := FormatAKSClusters(clusters, tc.format)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported output format")
				assert.Empty(t, output)
			} else {
				require.NoError(t, err)
				testutil.CompareWithFixture(t, output, testutil.WithExtension(tc.extension))
			}
		})
	}
}

func TestDisplayAKSClusters(t *testing.T) {
	clusters := []AKSCluster{
		{
			Name:           "test-cluster",
			ResourceGroup:  "test-rg",
			SubscriptionID: "sub-123",
			Location:       "eastus",
		},
	}

	t.Run("invalid format returns error", func(t *testing.T) {
		err := DisplayAKSClusters(clusters, "invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported output format")
	})
}
