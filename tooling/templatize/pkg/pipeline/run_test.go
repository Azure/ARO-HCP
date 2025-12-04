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
	"slices"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	configtypes "github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/graph"
	"github.com/Azure/ARO-Tools/pkg/topology"
	"github.com/Azure/ARO-Tools/pkg/types"

	"github.com/Azure/ARO-HCP/tooling/templatize/bicep"
)

func TestMockedPipelineRun(t *testing.T) {
	pipeline := &types.Pipeline{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
		ResourceGroups: []*types.ResourceGroup{
			{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name:          "rg",
					ResourceGroup: "resourceGroup",
					Subscription:  "subscription",
				},
				Steps: []types.Step{
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "root"},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "second"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "root",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "third"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "second",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "fourth"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg2",
									Step:          "second2",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "fifth"},
					},
				},
			},
			{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name:          "rg2",
					ResourceGroup: "resourceGroup2",
					Subscription:  "subscription",
				},
				Steps: []types.Step{
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "root2"},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "second2"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg2",
									Step:          "root2",
								},
							}},
						}},
					},
				},
			},
		},
	}

	lock := sync.Mutex{}
	var order []types.StepDependency

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		logger, err := logr.FromContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		logger.Info("running step", "resourceGroup", executionTarget.GetResourceGroup(), "step", s.StepName())

		lock.Lock()
		defer lock.Unlock()
		order = append(order, types.StepDependency{ResourceGroup: executionTarget.GetResourceGroup(), Step: s.StepName()})

		return nil, nil, nil
	}

	t.Helper()
	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), testr.New(t))

	logger.Info("starting bicep language server...")
	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	if _, err := RunPipeline(&topology.Service{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
	}, pipeline, logr.NewContext(t.Context(), testr.New(t)), &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{
			BicepClient: lspClient,
		},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	}, executor); err != nil {
		t.Error(err)
	}

	lock.Lock()
	defer lock.Unlock()
	slices.SortFunc(order, graph.CompareStepDependencies)

	if diff := cmp.Diff(order, []types.StepDependency{
		{ResourceGroup: "resourceGroup", Step: "fifth"},
		{ResourceGroup: "resourceGroup", Step: "fourth"},
		{ResourceGroup: "resourceGroup", Step: "root"},
		{ResourceGroup: "resourceGroup", Step: "second"},
		{ResourceGroup: "resourceGroup", Step: "third"},
		{ResourceGroup: "resourceGroup2", Step: "root2"},
		{ResourceGroup: "resourceGroup2", Step: "second2"},
	}); len(diff) != 0 {
		t.Errorf("incorrect step execution order: %s", diff)
	}
}

func TestMockedPipelineRunError(t *testing.T) {
	pipeline := &types.Pipeline{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
		ResourceGroups: []*types.ResourceGroup{
			{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name:          "rg",
					ResourceGroup: "resourceGroup",
					Subscription:  "subscription",
				},
				Steps: []types.Step{
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "root"},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "second"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "root",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "third"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "second",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "fourth"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg2",
									Step:          "second2",
								},
							}},
						}},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "fifth"},
					},
				},
			},
			{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name:          "rg2",
					ResourceGroup: "resourceGroup2",
					Subscription:  "subscription",
				},
				Steps: []types.Step{
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "root2"},
					},
					&types.ShellStep{
						StepMeta: types.StepMeta{Name: "second2"},
						Variables: []types.Variable{{
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg2",
									Step:          "root2",
								},
							}},
						}, { // since we cancel the context when rg2/second2 fails, if this does not depend on everything, it's not deterministic which will have finished
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "fifth",
								},
							}},
						}, {
							Value: types.Value{Input: &types.Input{
								StepDependency: types.StepDependency{
									ResourceGroup: "rg",
									Step:          "third",
								},
							}},
						}},
					},
				},
			},
		},
	}

	lock := sync.Mutex{}
	var order []types.StepDependency

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		logger, err := logr.FromContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		logger.Info("running step", "resourceGroup", executionTarget.GetResourceGroup(), "step", s.StepName())

		lock.Lock()
		defer lock.Unlock()
		order = append(order, types.StepDependency{ResourceGroup: executionTarget.GetResourceGroup(), Step: s.StepName()})

		if s.StepName() == "second" {
			return nil, nil, fmt.Errorf("oops")
		}

		return nil, nil, nil
	}

	t.Helper()
	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), testr.New(t))

	logger.Info("starting bicep language server...")
	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	if _, err := RunPipeline(&topology.Service{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
	}, pipeline, logr.NewContext(t.Context(), testr.New(t)), &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{
			BicepClient: lspClient,
		},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	}, executor); err == nil {
		t.Errorf("expected an error, got none")
	}

	lock.Lock()
	defer lock.Unlock()
	slices.SortFunc(order, graph.CompareStepDependencies)

	if diff := cmp.Diff(order, []types.StepDependency{
		{ResourceGroup: "resourceGroup", Step: "fifth"},
		{ResourceGroup: "resourceGroup", Step: "root"},
		{ResourceGroup: "resourceGroup", Step: "second"},
		{ResourceGroup: "resourceGroup2", Step: "root2"},
	}); len(diff) != 0 {
		t.Errorf("incorrect step execution order: %s", diff)
	}
}

func TestPipelineRun(t *testing.T) {
	pipeline := &types.Pipeline{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
		ResourceGroups: []*types.ResourceGroup{
			{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name:          "test",
					ResourceGroup: "test",
					Subscription:  "test",
				},
				Steps: []types.Step{
					&types.ShellStep{
						StepMeta: types.StepMeta{
							Name:   "step",
							Action: "Shell",
						},
						Command: "echo hello",
					},
				},
			},
		},
	}

	t.Helper()
	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), testr.New(t))

	logger.Info("starting bicep language server...")
	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	output, err := RunPipeline(&topology.Service{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
	}, pipeline, logr.NewContext(t.Context(), testr.New(t)), &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{
			BicepClient: lspClient,
		},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
	}, RunStep)

	assert.NoError(t, err)
	oValue, err := output[pipeline.ServiceGroup]["test"]["step"].GetValue("output")
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
		cfg           configtypes.Configuration
		input         Outputs
		stepVariables []types.Variable
		expected      map[string]any
		err           string
	}{
		{
			name: "output chaining",
			input: Outputs{
				"Microsoft.Azure.ARO.Whatever": map[string]map[string]Output{
					"rg": {
						"step1": ArmOutput{
							"output1": map[string]any{
								"type":  "String",
								"value": "bar",
							},
						},
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						Input: &types.Input{
							Name: "output1",
							StepDependency: types.StepDependency{
								ResourceGroup: "rg",
								Step:          "step1",
							},
						},
					},
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "output chaining missing step",
			input: Outputs{
				"Microsoft.Azure.ARO.Whatever": map[string]map[string]Output{
					"rg": {
						"step1": ArmOutput{
							"output1": map[string]any{
								"type":  "String",
								"value": "bar",
							},
						},
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						Input: &types.Input{
							Name: "output1",
							StepDependency: types.StepDependency{
								ResourceGroup: "rg",
								Step:          "missingstep",
							},
						},
					},
				},
			},
			err: "step missingstep not found in provided outputs",
		},
		{
			name: "output chaining missing variable",
			input: Outputs{
				"Microsoft.Azure.ARO.Whatever": map[string]map[string]Output{
					"rg": {
						"step1": ArmOutput{
							"output1": map[string]any{
								"type":  "String",
								"value": "bar",
							},
						},
					},
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						Input: &types.Input{
							Name: "missingvar",
							StepDependency: types.StepDependency{
								ResourceGroup: "rg",
								Step:          "step1",
							},
						},
					},
				},
			},
			err: "failed to get value for input step1.missingvar: key \"missingvar\" not found",
		},
		{
			name: "value",
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						Value: "bar",
					},
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "configref",
			cfg: configtypes.Configuration{
				"some": map[string]any{
					"config": "bar",
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						ConfigRef: "some.config",
					},
				},
			},
			expected: map[string]any{"input1": "bar"},
		},
		{
			name: "configref missing",
			cfg: configtypes.Configuration{
				"some": map[string]any{
					"config": "bar",
				},
			},
			stepVariables: []types.Variable{
				{
					Name: "input1",
					Value: types.Value{
						ConfigRef: "some.missing",
					},
				},
			},
			err: "failed to lookup config reference some.missing for input1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getInputValues("Microsoft.Azure.ARO.Whatever", tc.stepVariables, tc.cfg, tc.input)
			t.Log(result)
			if tc.err != "" {
				assert.Error(t, err, tc.err)
			} else {
				assert.NoError(t, err)
				assert.Empty(t, cmp.Diff(tc.expected, result))
			}
		})
	}
}

func TestShouldRetryError(t *testing.T) {
	testCases := []struct {
		name     string
		step     types.Step
		err      error
		expected bool
	}{
		{
			name: "should retry",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: &types.AutomatedRetry{
						ErrorContainsAny: []string{"error"},
					},
				},
			},
			err:      fmt.Errorf("error"),
			expected: true,
		},
		{
			name: "should not retry",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: &types.AutomatedRetry{
						ErrorContainsAny: []string{"this is broken"},
					},
				},
			},
			err:      fmt.Errorf("other error"),
			expected: false,
		},
		{
			name: "no retries",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: nil,
				},
			},
			err:      fmt.Errorf("other error"),
			expected: false,
		},
		{
			name: "nil error",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: &types.AutomatedRetry{
						ErrorContainsAny: []string{"this is broken"},
					},
				},
			},
			err:      nil,
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := checkAutomatedRetry(testr.New(t), tc.step, tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestShouldExecuteStep(t *testing.T) {
	testCases := []struct {
		name                 string
		step                 types.Step
		runCount             int
		retryOnAnyErrorCount int
		expected             bool
	}{
		{
			name: "should execute",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: &types.AutomatedRetry{
						MaximumRetryCount: 1,
					},
				},
			},
			runCount:             0,
			retryOnAnyErrorCount: 0,
			expected:             true,
		},
		{
			name: "default, no retries",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: nil,
				},
			},
			runCount:             0,
			retryOnAnyErrorCount: 0,
			expected:             true,
		},
		{
			name: "retry on any error",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{},
			},
			runCount:             3,
			retryOnAnyErrorCount: 4,
			expected:             true,
		},
		{
			name: "retry on any error exhausted",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{},
			},
			runCount:             3,
			retryOnAnyErrorCount: 3,
			expected:             false,
		},
		{
			name: "automated retries bigger than any error retries",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: &types.AutomatedRetry{
						MaximumRetryCount: 5,
					},
				},
			},
			runCount:             3,
			retryOnAnyErrorCount: 3,
			expected:             true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := shouldExecuteStep(tc.step, tc.retryOnAnyErrorCount, tc.runCount)
			assert.Equal(t, tc.expected, result)
		})
	}
}
