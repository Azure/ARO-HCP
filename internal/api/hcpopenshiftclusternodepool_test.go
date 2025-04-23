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

package api

import (
	"net/http"
	"testing"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

func TestNodePoolRequiredForPut(t *testing.T) {
	tests := []struct {
		name         string
		resource     *HCPOpenShiftClusterNodePool
		expectErrors []arm.CloudErrorBody
	}{
		{
			name:     "Empty node pool",
			resource: &HCPOpenShiftClusterNodePool{},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'properties'",
					Target:  "properties",
				},
			},
		},
		{
			name:     "Default node pool",
			resource: NewDefaultHCPOpenShiftClusterNodePool(),
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Missing required field 'vmSize'",
					Target:  "properties.platform.vmSize",
				},
			},
		},
		{
			name:     "Minimum valid node pool",
			resource: MinimumValidNodePoolTestCase(),
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualErrors := ValidateRequest(validate, http.MethodPut, tt.resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}

func TestNodePoolValidateTags(t *testing.T) {
	// Note "required_for_put" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *HCPOpenShiftClusterNodePool
		expectErrors []arm.CloudErrorBody
	}{
		{
			name: "Min=0 not satisfied",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Replicas: int32(-1),
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '-1' for field 'replicas' (must be non-negative)",
					Target:  "properties.replicas",
				},
			},
		},
		{
			name: "Both Replicas and AutoScaling present",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Replicas: int32(1),
					AutoScaling: &NodePoolAutoScaling{
						Min: 1,
						Max: 2,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Field 'replicas' must be 0 when 'autoScaling' is specified",
					Target:  "properties.replicas",
				},
			},
		},
		{
			name: "Only AutoScaling present with zero-values",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &NodePoolAutoScaling{
						Min: 0,
						Max: 0,
					},
				},
			},
		},
		{
			name: "AutoScaling max is less than min",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &NodePoolAutoScaling{
						Min: 1,
						Max: 0,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '0' for field 'max' (must be at least the value of 'min')",
					Target:  "properties.autoScaling.max",
				},
			},
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := NodePoolTestCase(t, tt.tweaks)

			actualErrors := ValidateRequest(validate, http.MethodPut, resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
