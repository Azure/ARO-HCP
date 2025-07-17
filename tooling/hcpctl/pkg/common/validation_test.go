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

package common

import (
	"strings"
	"testing"
)

func TestParseClusterIdentifier(t *testing.T) {
	testCases := []struct {
		input              string
		expectedClusterID  string
		expectedResourceID string
		expectError        bool
		description        string
	}{
		// Valid cluster ID cases
		{
			input:              "2jesjug41iavg27inj078ssjidn20clk",
			expectedClusterID:  "2jesjug41iavg27inj078ssjidn20clk",
			expectedResourceID: "",
			expectError:        false,
			description:        "valid 32-char cluster ID",
		},
		{
			input:              "abcdef1234567890abcdef1234567890",
			expectedClusterID:  "abcdef1234567890abcdef1234567890",
			expectedResourceID: "",
			expectError:        false,
			description:        "valid 32-char alphanumeric",
		},
		{
			input:              strings.Repeat("a", 32),
			expectedClusterID:  strings.Repeat("a", 32),
			expectedResourceID: "",
			expectError:        false,
			description:        "exactly 32 chars lowercase",
		},
		{
			input:              strings.Repeat("1", 32),
			expectedClusterID:  strings.Repeat("1", 32),
			expectedResourceID: "",
			expectError:        false,
			description:        "exactly 32 chars numeric",
		},
		{
			input:              "9z8y7x6w5v4u3t2s1r0q9p8o7n6m5l4k",
			expectedClusterID:  "9z8y7x6w5v4u3t2s1r0q9p8o7n6m5l4k",
			expectedResourceID: "",
			expectError:        false,
			description:        "mixed numbers and lowercase",
		},

		// Invalid cluster ID cases
		{
			input:              "",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "empty input (not allowed)",
		},
		{
			input:              "2jesjug41iavg27inj078ssjidn20cl-k",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID contains hyphen",
		},
		{
			input:              "2jesjug41iavg27inj078ssjidn20cl_k",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID contains underscore",
		},
		{
			input:              "2jesjug41iavg27inj078ssjidn20cl.k",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID contains dot",
		},
		{
			input:              "2JESJUG41IAVG27INJ078SSJIDN20CLK",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID with uppercase letters",
		},
		{
			input:              "2jesjug41iavg27inj078ssjidn20cl!",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID contains special character",
		},
		{
			input:              strings.Repeat("a", 31),
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID too short (31 chars)",
		},
		{
			input:              strings.Repeat("a", 33),
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "cluster ID too long (33 chars)",
		},
		{
			input:              "invalid-cluster",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "invalid cluster ID format",
		},

		// Valid Azure resource ID cases
		{
			input:              "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expectedClusterID:  "",
			expectedResourceID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			expectError:        false,
			description:        "valid Azure resource ID",
		},
		{
			input:              "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster-123",
			expectedClusterID:  "",
			expectedResourceID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster-123",
			expectError:        false,
			description:        "Azure resource ID with complex cluster name",
		},
		{
			input:              "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/mmazur-rg-03/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/mmazur",
			expectedClusterID:  "",
			expectedResourceID: "/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/mmazur-rg-03/providers/Microsoft.RedHatOpenshift/hcpOpenShiftClusters/mmazur",
			expectError:        false,
			description:        "Azure resource ID with lowercase 'shift' in OpenShift",
		},
		{
			input:              "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/-invalid-cluster",
			expectedClusterID:  "",
			expectedResourceID: "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/-invalid-cluster",
			expectError:        false,
			description:        "Azure resource ID with any cluster name (validation not performed)",
		},

		// Invalid Azure resource ID cases
		{
			input:              "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.RedHatOpenShift/otherResource/my-cluster",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "invalid Azure resource type",
		},
		{
			input:              "/subscriptions/12345678-1234-1234-1234-123456789012/resourceGroups/myRG/providers/Microsoft.Compute/virtualMachines/my-vm",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "different Azure provider",
		},
		{
			input:              "/invalid/resource/id",
			expectedClusterID:  "",
			expectedResourceID: "",
			expectError:        true,
			description:        "malformed resource ID",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			result, err := ParseHCPIdentifier(tc.input)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error for input '%s', got nil", tc.input)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for input '%s': %v", tc.input, err)
					return
				}
				if result.ClusterID != tc.expectedClusterID {
					t.Errorf("ParseClusterIdentifier('%s') clusterID = '%s', expected '%s'", tc.input, result.ClusterID, tc.expectedClusterID)
				}
				var actualResourceID string
				if result.ResourceID != nil {
					actualResourceID = result.ResourceID.String()
				}
				if actualResourceID != tc.expectedResourceID {
					t.Errorf("ParseClusterIdentifier('%s') resourceID = '%s', expected '%s'", tc.input, actualResourceID, tc.expectedResourceID)
				}
			}
		})
	}
}
