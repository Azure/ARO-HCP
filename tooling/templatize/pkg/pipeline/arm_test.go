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

package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/types"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func TestWaitForExistingDeployment(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name            string
		deploymentState []armresources.ProvisioningState
		missing         bool
		expectedError   *string
		expecetCallCnt  int
		returnError     *error
		timeout         int
	}{
		{
			name:            "Timeout",
			deploymentState: []armresources.ProvisioningState{"Running", "Running"},
			expectedError:   to.Ptr("timeout exeeded waiting for deployment test in rg rg"),
			expecetCallCnt:  1,
			timeout:         1,
		},
		{
			name:           "Missing Deployment",
			missing:        true,
			expecetCallCnt: 1,
			timeout:        1,
		},
		{
			name:            "Retrying",
			deploymentState: []armresources.ProvisioningState{"Running", "Running", "Succeeded"},
			expecetCallCnt:  2,
			timeout:         60,
		},
		{
			name:           "Handle Error",
			missing:        true,
			expecetCallCnt: 1,
			returnError:    to.Ptr(fmt.Errorf("test error")),
			expectedError:  to.Ptr("error getting deployment test error"),
			timeout:        1,
		},
	}

	for _, c := range cases {
		rg := "rg"
		depl := "test"
		callCnt := 0
		t.Run(c.name, func(t *testing.T) {
			a := armClient{
				deploymentRetryWaitTime: 1,
				GetDeployment: func(_ context.Context, rgName, deploymentName string) (armresources.DeploymentsClientGetResponse, error) {
					assert.Equal(t, rgName, rg)
					assert.Equal(t, deploymentName, depl)
					callCnt++

					var retErr error
					if c.returnError != nil {
						retErr = *c.returnError
					}

					returnObj := armresources.DeploymentsClientGetResponse{
						DeploymentExtended: armresources.DeploymentExtended{},
					}
					if !c.missing {
						returnObj.Properties = &armresources.DeploymentPropertiesExtended{
							ProvisioningState: &c.deploymentState[callCnt],
						}
					}
					return returnObj, retErr
				},
			}

			err := a.waitForExistingDeployment(ctx, c.timeout, rg, depl)
			if c.expectedError != nil {
				assert.Equal(t, err.Error(), *c.expectedError)
			}
			assert.Equal(t, callCnt, c.expecetCallCnt)
		})
	}
}

func TestGenerateDeploymentName(t *testing.T) {
	tests := []struct {
		name         string
		step         *types.ARMStep
		expectUnique bool
		expectPrefix string
	}{
		{
			name: "regular step returns original name",
			step: &types.ARMStep{
				StepMeta: types.StepMeta{
					Name: "test-step",
				},
				OutputOnly: false,
			},
			expectUnique: false,
			expectPrefix: "test-step",
		},
		{
			name: "outputOnly step returns unique name",
			step: &types.ARMStep{
				StepMeta: types.StepMeta{
					Name: "test-output-step",
				},
				OutputOnly: true,
			},
			expectUnique: true,
			expectPrefix: "test-output-step-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateDeploymentName(tt.step)

			if tt.expectUnique {
				// Should have the original name as prefix
				if !strings.HasPrefix(result, tt.expectPrefix) {
					t.Errorf("Expected result to start with '%s', got '%s'", tt.expectPrefix, result)
				}
				// Should be longer than the original name
				if len(result) <= len(tt.step.Name) {
					t.Errorf("Expected unique name to be longer than original name '%s', got '%s'", tt.step.Name, result)
				}
				// Should contain a hyphen and suffix
				parts := strings.Split(result, "-")
				if len(parts) < 2 {
					t.Errorf("Expected unique name to contain hyphen and suffix, got '%s'", result)
				}
				// Suffix should be 8 characters (hex encoded 4 bytes)
				suffix := parts[len(parts)-1]
				if len(suffix) != 8 {
					t.Errorf("Expected suffix to be 8 characters, got '%s' (length %d)", suffix, len(suffix))
				}
			} else {
				// Should be exactly the original name
				if result != tt.expectPrefix {
					t.Errorf("Expected result to be '%s', got '%s'", tt.expectPrefix, result)
				}
			}
		})
	}
}

func TestGenerateDeploymentNameUniqueness(t *testing.T) {
	step := &types.ARMStep{
		StepMeta: types.StepMeta{
			Name: "test-step",
		},
		OutputOnly: true,
	}

	// Generate multiple names and ensure they're unique
	names := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := generateDeploymentName(step)
		if names[name] {
			t.Errorf("Generated duplicate name: %s", name)
		}
		names[name] = true
	}
}

func TestComputeResourceGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		existingTags map[string]*string
		persist      bool
		expectedTags map[string]*string
		description  string
	}{
		// Empty existing tags
		{
			name:         "empty_tags_persist_true",
			existingTags: map[string]*string{},
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Empty tags with persist=true should add persist tag",
		},
		{
			name:         "empty_tags_persist_false",
			existingTags: map[string]*string{},
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Empty tags with persist=false should remain empty",
		},

		// Nil existing tags (edge case)
		{
			name:         "nil_tags_persist_true",
			existingTags: nil,
			persist:      true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
			},
			description: "Nil tags with persist=true should add persist tag",
		},
		{
			name:         "nil_tags_persist_false",
			existingTags: nil,
			persist:      false,
			expectedTags: map[string]*string{},
			description:  "Nil tags with persist=false should result in empty map",
		},

		// Existing tags with persist="true"
		{
			name: "existing_persist_true_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=true should preserve persist tag",
		},
		{
			name: "existing_persist_true_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"), // Should be preserved (safety rule)
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=true with persist=false should preserve persist tag (safety rule)",
		},

		// Existing tags with persist="false"
		{
			name: "existing_persist_false_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=true should set persist to true",
		},
		{
			name: "existing_persist_false_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("false"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=false with persist=false should not set persist tag",
		},

		// Existing tags with persist="something_else"
		{
			name: "existing_persist_invalid_persist_true",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"persist": to.Ptr("true"),
				"env":     to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=true should set persist to true",
		},
		{
			name: "existing_persist_invalid_persist_false",
			existingTags: map[string]*string{
				"persist": to.Ptr("maybe"),
				"env":     to.Ptr("dev"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env": to.Ptr("dev"),
			},
			description: "Existing persist=invalid with persist=false should not set persist tag",
		},

		// Existing tags without persist tag
		{
			name: "no_persist_tag_persist_true",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: true,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
				"persist": to.Ptr("true"),
			},
			description: "No persist tag with persist=true should add persist tag",
		},
		{
			name: "no_persist_tag_persist_false",
			existingTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			persist: false,
			expectedTags: map[string]*string{
				"env":     to.Ptr("dev"),
				"project": to.Ptr("aro-hcp"),
			},
			description: "No persist tag with persist=false should not add persist tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeResourceGroupTags(tt.existingTags, tt.persist)
			assert.Equal(t, tt.expectedTags, result, tt.description)
		})
	}
}
