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

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func TestStepRun(t *testing.T) {
	s := types.NewShellStep("step", "echo hello")
	output, err := RunStep(s, context.Background(), "", &executionTargetImpl{}, &PipelineRunOptions{}, nil)
	assert.NoError(t, err)
	o, err := output.GetValue("output")
	assert.NoError(t, err)
	assert.Equal(t, o.Value, "hello\n")
}

func TestResourceGroupRun(t *testing.T) {
	rg := &types.ResourceGroup{
		Steps: []types.Step{
			types.NewShellStep("step", "echo hello"),
		},
	}
	o := make(map[string]Output)
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, o)
	assert.NoError(t, err)
	oValue, err := o["step"].GetValue("output")
	assert.NoError(t, err)
	assert.Equal(t, oValue.Value, "hello\n")
}

func TestResourceGroupError(t *testing.T) {
	rg := &types.ResourceGroup{
		Steps: []types.Step{
			types.NewShellStep("step1", "echo hello"),
			types.NewShellStep("step2", "faaaaafffaa"),
			types.NewShellStep("step3", "echo hallo"),
		},
	}
	o := make(map[string]Output)
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &executionTargetImpl{}, o)
	assert.ErrorContains(t, err, "faaaaafffaa: command not found\n exit status 127")
	// Test processing ends after first error
	oValue, err := o["step1"].GetValue("output")
	assert.NoError(t, err)
	assert.NoError(t, err)
	assert.Equal(t, oValue.Value, "hello\n")
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
	rg := &types.ResourceGroup{Steps: []types.Step{}}
	err := RunResourceGroup(rg, context.Background(), &PipelineRunOptions{}, &testExecutionTarget{}, make(map[string]Output))
	assert.NoError(t, err)
}

func TestPipelineRun(t *testing.T) {
	pipeline := &types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{
			{
				Name:         "test",
				Subscription: "test",
				Steps: []types.Step{
					types.NewShellStep("step", "echo hello"),
				},
			},
		},
	}

	output, err := RunPipeline(pipeline, context.Background(), &PipelineRunOptions{
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	})

	assert.NoError(t, err)
	oValue, err := output["step"].GetValue("output")
	assert.NoError(t, err)
	assert.Equal(t, oValue.Value, "hello\n")
}

func TestArmGetValue(t *testing.T) {
	output := ArmOutput{
		"zoneName": map[string]any{
			"type":  "String",
			"value": "test",
		},
	}

	value, err := output.GetValue("zoneName")
	assert.Equal(t, value.Value, "test")
	assert.Equal(t, value.Type, "String")
	assert.NoError(t, err)
}

func TestAddInputVars(t *testing.T) {
	testCases := []struct {
		name          string
		cfg           config.Configuration
		input         map[string]Output
		stepVariables []types.Variable
		expected      map[string]any
		err           string
	}{
		{
			name: "output chaining",
			input: map[string]Output{
				"step1": ArmOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Input: &types.Input{
						Name: "output1",
						Step: "step1",
					},
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "output chaining missing step",
			input: map[string]Output{
				"step1": ArmOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Input: &types.Input{
						Name: "output1",
						Step: "missingstep",
					},
				},
			},
			err: "step missingstep not found in provided outputs",
		},
		{
			name: "output chaining missing variable",
			input: map[string]Output{
				"step1": ArmOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Input: &types.Input{
						Name: "missingvar",
						Step: "step1",
					},
				},
			},
			err: "failed to get value for input step1.missingvar: key \"missingvar\" not found",
		},
		{
			name: "value",
			stepVariables: []types.Variable{
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
			stepVariables: []types.Variable{
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
			stepVariables: []types.Variable{
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
				assert.NoError(t, err)
				assert.Equal(t, result, tc.expected)
			}
		})
	}
}
