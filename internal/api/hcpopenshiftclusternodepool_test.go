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
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"

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
			request, err := http.NewRequest(http.MethodPut, "localhost", nil)
			require.NoError(t, err)

			actualErrors := ValidateRequest(validate, request, tt.resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}

func TestNodePoolValidateTags(t *testing.T) {
	const maxQualifiedNameLength = 63
	var value string

	type k8sValidationError struct {
		value   string
		message string
	}

	// Induce error messages from Kubernetes validation
	// functions so we don't have to hardcode them below.

	value = "-invalidname-"
	k8sQualifiedNameInvalid := k8sValidationError{value, k8svalidation.IsQualifiedName(value)[0]}

	value = strings.Repeat("x", maxQualifiedNameLength+1)
	k8sQualifiedNameTooLong := k8sValidationError{value, k8svalidation.IsQualifiedName(value)[0]}

	value = "Invalid.Prefix/name"
	k8sQualifiedNamePrefixInvalid := k8sValidationError{value, k8svalidation.IsQualifiedName(value)[0]}

	value = "-Invalid.Value"
	k8sLabelValueInvalid := k8sValidationError{value, k8svalidation.IsValidLabelValue(value)[0]}

	value = strings.Repeat("x", maxQualifiedNameLength+1)
	k8sLabelValueTooLong := k8sValidationError{value, k8svalidation.IsValidLabelValue(value)[0]}

	// Note "required_for_put" validation tests are above.
	// This function tests all the other validators in use.
	tests := []struct {
		name         string
		tweaks       *HCPOpenShiftClusterNodePool
		expectErrors []arm.CloudErrorBody
	}{
		{
			name: "Bad openshift_version",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Version: NodePoolVersionProfile{
						ID: "bad.version",
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid OpenShift version 'bad.version'",
					Target:  "properties.version.id",
				},
			},
		},
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
			name: "Only AutoScaling present",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &NodePoolAutoScaling{
						Min: 1,
						Max: 2,
					},
				},
			},
		},
		{
			name: "AutoScaling max is less than min",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					AutoScaling: &NodePoolAutoScaling{
						Min: 2,
						Max: 1,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: "Invalid value '1' for field 'max' (must be at least the value of 'min')",
					Target:  "properties.autoScaling.max",
				},
			},
		},
		{
			name: "Empty k8s_label_value is valid",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Labels: map[string]string{
						"labelName": "",
					},
				},
			},
		},
		{
			name: "Bad k8s_label_value",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Labels: map[string]string{
						"labelName1": k8sLabelValueInvalid.value,
						"labelName2": k8sLabelValueTooLong.value,
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: fmt.Sprintf("Invalid value '%s' for field 'labels[labelName1]' (%s)",
						k8sLabelValueInvalid.value,
						k8sLabelValueInvalid.message),
					Target: "properties.labels[labelName1]",
				},
				{
					Message: fmt.Sprintf("Invalid value '%s' for field 'labels[labelName2]' (%s)",
						k8sLabelValueTooLong.value,
						k8sLabelValueTooLong.message),
					Target: "properties.labels[labelName2]",
				},
			},
		},
		{
			name: "Bad k8s_qualified_name",
			tweaks: &HCPOpenShiftClusterNodePool{
				Properties: HCPOpenShiftClusterNodePoolProperties{
					Taints: []Taint{
						{
							Effect: EffectNoExecute,
							Key:    k8sQualifiedNameInvalid.value,
							Value:  "value",
						},
						{
							Effect: EffectNoExecute,
							Key:    k8sQualifiedNameTooLong.value,
							Value:  "value",
						},
						{
							Effect: EffectNoExecute,
							Key:    k8sQualifiedNamePrefixInvalid.value,
							Value:  "value",
						},
					},
				},
			},
			expectErrors: []arm.CloudErrorBody{
				{
					Message: fmt.Sprintf("Invalid value '%s' for field 'key' (%s)",
						k8sQualifiedNameInvalid.value,
						k8sQualifiedNameInvalid.message),
					Target: "properties.taints[0].key",
				},
				{
					Message: fmt.Sprintf("Invalid value '%s' for field 'key' (%s)",
						k8sQualifiedNameTooLong.value,
						k8sQualifiedNameTooLong.message),
					Target: "properties.taints[1].key",
				},
				{
					Message: fmt.Sprintf("Invalid value '%s' for field 'key' (%s)",
						k8sQualifiedNamePrefixInvalid.value,
						k8sQualifiedNamePrefixInvalid.message),
					Target: "properties.taints[2].key",
				},
			},
		},
	}

	validate := NewTestValidator()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := NodePoolTestCase(t, tt.tweaks)

			actualErrors := ValidateRequest(validate, nil, resource)

			// from hcpopenshiftcluster_test.go
			diff := compareErrors(tt.expectErrors, actualErrors)
			if diff != "" {
				t.Fatalf("Expected error mismatch:\n%s", diff)
			}
		})
	}
}
