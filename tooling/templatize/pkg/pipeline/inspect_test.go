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
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func TestInspectVars(t *testing.T) {
	mockSubscriptionLookup := func(ctx context.Context, name string) (string, error) {
		return "mock-sub-" + name, nil
	}

	testCases := []struct {
		name              string
		pipeline          *types.Pipeline
		caseStep          types.Step
		options           *InspectOptions
		expectedVariables map[string]string
		err               string
	}{
		{
			name: "basic",
			pipeline: &types.Pipeline{
				ResourceGroups: []*types.ResourceGroup{{
					Name:         "test-rg",
					Subscription: "test-subscription",
					Steps: []types.Step{
						&types.ShellStep{
							StepMeta: types.StepMeta{
								Action: "Shell",
								Name:   "step",
							},
							Command: "echo hello",
							Variables: []types.Variable{{
								Name: "FOO",
								Value: types.Value{
									ConfigRef: "foo",
								},
							}},
						},
					},
				}},
			},
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
				Variables: []types.Variable{{
					Name: "FOO",
					Value: types.Value{
						ConfigRef: "foo",
					},
				}},
			},
			options: &InspectOptions{
				Configuration: config.Configuration{
					"foo": "bar",
				},
				Format:                 "shell",
				Region:                 "westus3",
				SubscriptionLookupFunc: mockSubscriptionLookup,
			},
			expectedVariables: map[string]string{
				"FOO":           "bar",
				"ResourceGroup": "test-rg",
				"Subscription":  "mock-sub-test-subscription",
			},
		},
		{
			name: "makefile",
			pipeline: &types.Pipeline{
				ResourceGroups: []*types.ResourceGroup{{
					Name:         "test-rg",
					Subscription: "test-subscription",
					Steps: []types.Step{
						&types.ShellStep{
							StepMeta: types.StepMeta{
								Action: "Shell",
								Name:   "step",
							},
							Command: "echo hello",
							Variables: []types.Variable{{
								Name: "FOO",
								Value: types.Value{
									ConfigRef: "foo",
								},
							}},
						},
					},
				}},
			},
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
				Variables: []types.Variable{{
					Name: "FOO",
					Value: types.Value{
						ConfigRef: "foo",
					},
				}},
			},
			options: &InspectOptions{
				Configuration: config.Configuration{
					"foo": "bar",
				},
				Format:                 "makefile",
				Region:                 "westus3",
				SubscriptionLookupFunc: mockSubscriptionLookup,
			},
			expectedVariables: map[string]string{
				"FOO":           "bar",
				"ResourceGroup": "test-rg",
				"Subscription":  "mock-sub-test-subscription",
			},
		},
		{
			name:     "failed action",
			pipeline: &types.Pipeline{},
			caseStep: &types.ARMStep{
				StepMeta: types.StepMeta{
					Name:   "step",
					Action: "ARM",
				},
				Template:        "test.bicep",
				Parameters:      "test.bicepparam",
				DeploymentLevel: "ResourceGroup",
			},
			options: &InspectOptions{},
			err:     "inspecting step variables not implemented for action type ARM",
		},
		{
			name: "failed format",
			pipeline: &types.Pipeline{
				ResourceGroups: []*types.ResourceGroup{{
					Name:         "test-rg",
					Subscription: "test-subscription",
					Steps: []types.Step{
						&types.ShellStep{
							StepMeta: types.StepMeta{
								Action: "Shell",
								Name:   "step",
							},
							Command: "echo hello",
						},
					},
				}},
			},
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
			},
			options: &InspectOptions{
				Format:                 "unknown",
				SubscriptionLookupFunc: mockSubscriptionLookup,
			},
			err: "unknown output format \"unknown\"",
		},
		{
			name: "with AKS cluster",
			pipeline: &types.Pipeline{
				ResourceGroups: []*types.ResourceGroup{{
					Name:         "aks-rg",
					Subscription: "aks-subscription",
					Steps: []types.Step{
						&types.ShellStep{
							StepMeta: types.StepMeta{
								Action: "Shell",
								Name:   "aks-step",
							},
							Command:    "kubectl get nodes",
							AKSCluster: "my-aks-cluster",
						},
					},
				}},
			},
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "aks-step",
				},
				Command:    "kubectl get nodes",
				AKSCluster: "my-aks-cluster",
			},
			options: &InspectOptions{
				Format:                 "shell",
				Region:                 "westus3",
				SubscriptionLookupFunc: mockSubscriptionLookup,
			},
			expectedVariables: map[string]string{
				"ResourceGroup": "aks-rg",
				"Subscription":  "mock-sub-aks-subscription",
				"AKSCluster":    "my-aks-cluster",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			tc.options.OutputFile = buf
			err := inspectVars(context.Background(), tc.pipeline, tc.caseStep, tc.options)
			if tc.err == "" {
				assert.NoError(t, err)
				output := buf.String()
				// Check for presence of expected variables based on format
				for varName, varValue := range tc.expectedVariables {
					switch tc.options.Format {
					case "makefile":
						assert.Contains(t, output, fmt.Sprintf("%s ?= %s", varName, varValue))
					case "shell":
						assert.Contains(t, output, fmt.Sprintf("export %s=\"%s\"", varName, varValue))
					}
				}
			} else {
				assert.ErrorContains(t, err, tc.err)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	p := types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{{
			Steps: []types.Step{
				&types.ShellStep{
					StepMeta: types.StepMeta{
						Action: "Shell",
						Name:   "step1",
					},
					Command: "echo hello",
				},
			},
		},
		},
	}

	err := Inspect(&p, context.Background(), &InspectOptions{
		Scope:         "scope",
		Format:        "format",
		Step:          "step1",
		Region:        "",
		Configuration: config.Configuration{},
		ScopeFunctions: map[string]StepInspectScope{
			"scope": func(ctx context.Context, p *types.Pipeline, s types.Step, o *InspectOptions) error {
				assert.Equal(t, s.StepName(), "step1")
				return nil
			},
		},
		OutputFile: new(bytes.Buffer),
	})
	assert.NoError(t, err)
}

func TestInspectWrongScope(t *testing.T) {
	p := types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{{
			Steps: []types.Step{
				&types.ShellStep{
					StepMeta: types.StepMeta{
						Action: "Shell",
						Name:   "step1",
					},
					Command: "echo hello",
				}},
		},
		},
	}

	err := Inspect(&p, context.Background(), &InspectOptions{
		Scope:         "foo",
		Format:        "format",
		Step:          "step1",
		Region:        "",
		Configuration: config.Configuration{},
		OutputFile:    new(bytes.Buffer),
	})
	assert.Error(t, err, "unknown inspect scope \"foo\"")
}
