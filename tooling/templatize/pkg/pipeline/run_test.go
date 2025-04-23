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
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func TestStepRun(t *testing.T) {
	foundOutput := ""
	s := NewShellStep("step", "echo hello").WithOutputFunc(
		func(output string) {
			foundOutput = output
		},
	)
	_, err := RunStep(s, context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{}, nil)
	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestResourceGroupRun(t *testing.T) {
	foundOutput := ""
	rg := &ResourceGroup{
		Steps: []Step{
			NewShellStep("step", "echo hello").WithOutputFunc(
				func(output string) {
					foundOutput = output
				},
			),
		},
	}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestResourceGroupError(t *testing.T) {
	tmpVals := make([]string, 0)
	outputFunc := func(output string) {
		tmpVals = append(tmpVals, output)
	}
	rg := &ResourceGroup{
		Steps: []Step{
			NewShellStep("step1", "echo hello").WithOutputFunc(outputFunc),
			NewShellStep("step2", "faaaaafffaa").WithOutputFunc(outputFunc),
			NewShellStep("step3", "echo hallo").WithOutputFunc(outputFunc),
		},
	}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, make(map[string]output))
	assert.ErrorContains(t, err, "faaaaafffaa: command not found\n exit status 127")
	// Test processing ends after first error
	assert.Equal(t, len(tmpVals), 1)
}

type testExecutionTarget struct{}

func (t *testExecutionTarget) KubeConfig(_ context.Context) (string, error) {
	return "", nil
}
func (t *testExecutionTarget) GetSubscriptionID() string { return "test" }
func (t *testExecutionTarget) GetAkSClusterName() string { return "test" }
func (t *testExecutionTarget) GetResourceGroup() string  { return "test" }
func (t *testExecutionTarget) GetRegion() string         { return "test" }

func TestResourceGroupRunRequireKubeconfig(t *testing.T) {
	rg := &ResourceGroup{Steps: []Step{}}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &testExecutionTarget{}, make(map[string]output))
	assert.NilError(t, err)
}

func TestPipelineRun(t *testing.T) {
	foundOutput := ""
	pipeline := &Pipeline{
		ResourceGroups: []*ResourceGroup{
			{
				Name:         "test",
				Subscription: "test",
				Steps: []Step{
					NewShellStep("step", "echo hello").WithOutputFunc(
						func(output string) {
							foundOutput = output
						},
					),
				},
			},
		},
	}

	_, err := RunPipeline(pipeline, context.Background(), &PipelineRunOptions{
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	})

	assert.NilError(t, err)
	assert.Equal(t, foundOutput, "hello\n")
}

func TestArmGetValue(t *testing.T) {
	output := armOutput{
		"zoneName": map[string]any{
			"type":  "String",
			"value": "test",
		},
	}

	value, err := output.GetValue("zoneName")
	assert.Equal(t, value.Value, "test")
	assert.Equal(t, value.Type, "String")
	assert.NilError(t, err)
}

func TestAddInputVars(t *testing.T) {
	testCases := []struct {
		name          string
		cfg           config.Configuration
		input         map[string]output
		stepVariables []Variable
		expected      map[string]any
		err           string
	}{
		{
			name: "output chaining",
			input: map[string]output{
				"step1": armOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []Variable{
				{
					Name: "input1",
					Input: &Input{
						Name: "output1",
						Step: "step1",
					},
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "output chaining missing step",
			input: map[string]output{
				"step1": armOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []Variable{
				{
					Name: "input1",
					Input: &Input{
						Name: "output1",
						Step: "missingstep",
					},
				},
			},
			err: "step missingstep not found in provided outputs",
		},
		{
			name: "output chaining missing variable",
			input: map[string]output{
				"step1": armOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []Variable{
				{
					Name: "input1",
					Input: &Input{
						Name: "missingvar",
						Step: "step1",
					},
				},
			},
			err: "failed to get value for input step1.missingvar: key \"missingvar\" not found",
		},
		{
			name: "value",
			stepVariables: []Variable{
				{
					Name:  "input1",
					Value: "bar",
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "configref",
			cfg: config.Configuration{
				"some": config.Configuration{
					"config": "bar",
				},
			},
			stepVariables: []Variable{
				{
					Name:      "input1",
					ConfigRef: "some.config",
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "configref missing",
			cfg: config.Configuration{
				"some": config.Configuration{
					"config": "bar",
				},
			},
			stepVariables: []Variable{
				{
					Name:      "input1",
					ConfigRef: "some.missing",
				},
			},
			err: "failed to lookup config reference some.missing for input1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getInputValues(tc.stepVariables, tc.cfg, tc.input)
			t.Log(result)
			if tc.err != "" {
				assert.Error(t, err, tc.err)
			} else {
				assert.NilError(t, err)
				assert.DeepEqual(t, result, tc.expected)
			}
		})
	}
}
