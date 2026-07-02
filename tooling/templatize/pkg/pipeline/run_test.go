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

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/topology"
	"github.com/Azure/ARO-Tools/pipelines/types"

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
					Subscription:  TEST_SUBSCRIPTION_ID,
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
					Subscription:  TEST_SUBSCRIPTION_ID,
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
		Environment: "test-env",
		Stamp:       "1",
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) {
			return ".", nil
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
					Subscription:  TEST_SUBSCRIPTION_ID,
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
		Environment: "test-env",
		Stamp:       "1",
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) {
			return ".", nil
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
					Subscription:  TEST_SUBSCRIPTION_ID,
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

	state, err := RunPipeline(&topology.Service{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Test",
	}, pipeline, logr.NewContext(t.Context(), testr.New(t)), &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{
			BicepClient: lspClient,
			SubscriptionIdToAzureConfigDirectory: map[string]string{
				TEST_SUBSCRIPTION_ID: "test",
			},
		},
		Environment: "test-env",
		Stamp:       "1",
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return "test", nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) {
			return ".", nil
		},
	}, RunStep)

	assert.NoError(t, err)
	outputs := state.GetOutputs("1")
	oValue, err := outputs[pipeline.ServiceGroup]["test"]["step"].GetValue("output")
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

func TestRunEntrypointMultiStamp(t *testing.T) {
	stamped := func() *bool { b := true; return &b }

	parentPipeline := &types.Pipeline{
		ServiceGroup: "Microsoft.Azure.ARO.HCP.Infra",
		ResourceGroups: []*types.ResourceGroup{{
			ResourceGroupMeta: &types.ResourceGroupMeta{
				Name: "infra-rg", ResourceGroup: "infra-rg", Subscription: TEST_SUBSCRIPTION_ID,
			},
			Steps: []types.Step{
				&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
			},
		}},
	}

	topo := &topology.CombinedTopology{
		Topology: topology.Topology{
			Services: []topology.Service{{
				ServiceGroup: "Microsoft.Azure.ARO.HCP.Infra",
				PipelinePath: "infra/pipeline.yaml",
				Purpose:      "infra",
				Children: []topology.Service{{
					ServiceGroup: "Microsoft.Azure.ARO.HCP.Mgmt",
					PipelinePath: "mgmt/pipeline.yaml",
					Purpose:      "mgmt",
					Stamped:      stamped(),
				}},
			}},
			Entrypoints: []topology.Entrypoint{{Identifier: "Microsoft.Azure.ARO.HCP.Infra"}},
		},
	}
	topo.PropagateStamped()

	entrypoint := &topo.Entrypoints[0]
	basePipelines := map[string]*types.Pipeline{
		"Microsoft.Azure.ARO.HCP.Infra": parentPipeline,
		"Microsoft.Azure.ARO.HCP.Mgmt": {
			ServiceGroup: "Microsoft.Azure.ARO.HCP.Mgmt",
			ResourceGroups: []*types.ResourceGroup{{
				ResourceGroupMeta: &types.ResourceGroupMeta{
					Name: "mgmt-rg", ResourceGroup: "mgmt-rg-base", Subscription: TEST_SUBSCRIPTION_ID,
				},
				Steps: []types.Step{
					&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
				},
			}},
		},
	}

	lock := sync.Mutex{}
	var executedSteps []string

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		key := fmt.Sprintf("%s/%s/stamp=%s/rg=%s", id.ServiceGroup, s.StepName(), options.Stamp, executionTarget.GetResourceGroup())
		lock.Lock()
		executedSteps = append(executedSteps, key)
		lock.Unlock()
		return nil, nil, nil
	}

	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), logger)

	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	stampConfigs := map[string]configtypes.Configuration{
		"1": {"stamp": "1"},
		"2": {"stamp": "2"},
		"3": {"stamp": "3"},
	}

	makeStampPipelines := func(stamp string) map[string]*types.Pipeline {
		return map[string]*types.Pipeline{
			"Microsoft.Azure.ARO.HCP.Infra": parentPipeline,
			"Microsoft.Azure.ARO.HCP.Mgmt": {
				ServiceGroup: "Microsoft.Azure.ARO.HCP.Mgmt",
				ResourceGroups: []*types.ResourceGroup{{
					ResourceGroupMeta: &types.ResourceGroupMeta{
						Name: "mgmt-rg", ResourceGroup: "mgmt-rg-" + stamp, Subscription: TEST_SUBSCRIPTION_ID,
					},
					Steps: []types.Step{
						&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
					},
				}},
			},
		}
	}

	_, err = RunEntrypoint(topo, entrypoint, basePipelines, ctx, &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{
			BicepClient: lspClient,
		},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return TEST_SUBSCRIPTION_ID, nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) { return ".", nil },
		StampConfigs:      stampConfigs,
		StampPipelines: map[string]map[string]*types.Pipeline{
			"1": makeStampPipelines("1"),
			"2": makeStampPipelines("2"),
			"3": makeStampPipelines("3"),
		},
	}, executor)
	assert.NoError(t, err)

	lock.Lock()
	defer lock.Unlock()

	slices.Sort(executedSteps)
	expected := []string{
		"Microsoft.Azure.ARO.HCP.Infra/deploy/stamp=/rg=infra-rg",
		"Microsoft.Azure.ARO.HCP.Mgmt/deploy/stamp=1/rg=mgmt-rg-1",
		"Microsoft.Azure.ARO.HCP.Mgmt/deploy/stamp=2/rg=mgmt-rg-2",
		"Microsoft.Azure.ARO.HCP.Mgmt/deploy/stamp=3/rg=mgmt-rg-3",
	}
	if diff := cmp.Diff(expected, executedSteps); diff != "" {
		t.Errorf("unexpected step execution (-want +got):\n%s", diff)
	}
}

func TestStampOutputIsolation(t *testing.T) {
	stamped := func() *bool { b := true; return &b }

	topo := &topology.CombinedTopology{
		Topology: topology.Topology{
			Services: []topology.Service{{
				ServiceGroup: "SG.Mgmt",
				PipelinePath: "mgmt/pipeline.yaml",
				Purpose:      "mgmt",
				Stamped:      stamped(),
			}},
			Entrypoints: []topology.Entrypoint{{Identifier: "SG.Mgmt"}},
		},
	}
	topo.PropagateStamped()

	makeStampPipelines := func(stamp string) map[string]*types.Pipeline {
		return map[string]*types.Pipeline{
			"SG.Mgmt": {
				ServiceGroup: "SG.Mgmt",
				ResourceGroups: []*types.ResourceGroup{{
					ResourceGroupMeta: &types.ResourceGroupMeta{
						Name: "rg", ResourceGroup: "mgmt-rg-" + stamp, Subscription: "sub-" + stamp,
					},
					Steps: []types.Step{
						&types.ShellStep{StepMeta: types.StepMeta{Name: "step1"}},
						&types.ShellStep{
							StepMeta: types.StepMeta{Name: "step2"},
							Variables: []types.Variable{{
								Name: "FROM_STEP1",
								Value: types.Value{
									Input: &types.Input{
										StepDependency: types.StepDependency{ResourceGroup: "rg", Step: "step1"},
										Name:           "result",
									},
								},
							}},
						},
					},
				}},
			},
		}
	}

	lock := sync.Mutex{}
	inputsPerStamp := map[string]any{}

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		if s.StepName() == "step1" {
			return ArmOutput{"result": map[string]any{"type": "string", "value": "output-from-" + id.Stamp}}, nil, nil
		}
		if s.StepName() == "step2" {
			state.RLock()
			outputs := state.GetOutputs(id.Stamp)
			state.RUnlock()
			vals, err := getInputValues(id.ServiceGroup, s.(*types.ShellStep).Variables, options.Configuration, outputs)
			if err != nil {
				return nil, nil, err
			}
			lock.Lock()
			inputsPerStamp[id.Stamp] = vals["FROM_STEP1"]
			lock.Unlock()
		}
		return nil, nil, nil
	}

	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), logger)

	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	_, err = RunEntrypoint(topo, &topo.Entrypoints[0], makeStampPipelines("1"), ctx, &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{BicepClient: lspClient},
		SubsciptionLookupFunc: func(_ context.Context, subName string) (string, error) {
			return "id-" + subName, nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) { return ".", nil },
		StampConfigs: map[string]configtypes.Configuration{
			"1": {"stamp": "1"},
			"2": {"stamp": "2"},
		},
		StampPipelines: map[string]map[string]*types.Pipeline{
			"1": makeStampPipelines("1"),
			"2": makeStampPipelines("2"),
		},
	}, executor)
	assert.NoError(t, err)

	assert.Equal(t, "output-from-1", inputsPerStamp["1"], "stamp 1 should see its own step1 output")
	assert.Equal(t, "output-from-2", inputsPerStamp["2"], "stamp 2 should see its own step1 output")
}

func TestStampSubscriptionResolution(t *testing.T) {
	stamped := func() *bool { b := true; return &b }

	topo := &topology.CombinedTopology{
		Topology: topology.Topology{
			Services: []topology.Service{{
				ServiceGroup: "SG.Mgmt",
				PipelinePath: "mgmt/pipeline.yaml",
				Purpose:      "mgmt",
				Stamped:      stamped(),
			}},
			Entrypoints: []topology.Entrypoint{{Identifier: "SG.Mgmt"}},
		},
	}
	topo.PropagateStamped()

	makeStampPipelines := func(stamp, subscription string) map[string]*types.Pipeline {
		return map[string]*types.Pipeline{
			"SG.Mgmt": {
				ServiceGroup: "SG.Mgmt",
				ResourceGroups: []*types.ResourceGroup{{
					ResourceGroupMeta: &types.ResourceGroupMeta{
						Name: "rg", ResourceGroup: "mgmt-rg-" + stamp, Subscription: subscription,
					},
					Steps: []types.Step{
						&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
					},
				}},
			},
		}
	}

	lock := sync.Mutex{}
	subscriptionsUsed := map[string]string{}

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		lock.Lock()
		subscriptionsUsed[id.Stamp] = executionTarget.GetSubscriptionID()
		lock.Unlock()
		return nil, nil, nil
	}

	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), logger)

	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	_, err = RunEntrypoint(topo, &topo.Entrypoints[0], makeStampPipelines("1", "sub-alpha"), ctx, &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{BicepClient: lspClient},
		SubsciptionLookupFunc: func(_ context.Context, subName string) (string, error) {
			return "resolved-" + subName, nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) { return ".", nil },
		StampConfigs: map[string]configtypes.Configuration{
			"1": {"stamp": "1"},
			"2": {"stamp": "2"},
		},
		StampPipelines: map[string]map[string]*types.Pipeline{
			"1": makeStampPipelines("1", "sub-alpha"),
			"2": makeStampPipelines("2", "sub-beta"),
		},
	}, executor)
	assert.NoError(t, err)

	assert.Equal(t, "resolved-sub-alpha", subscriptionsUsed["1"], "stamp 1 should resolve sub-alpha")
	assert.Equal(t, "resolved-sub-beta", subscriptionsUsed["2"], "stamp 2 should resolve sub-beta")
}

func TestUnstampedOutputVisibleToStampedSteps(t *testing.T) {
	stamped := func() *bool { b := true; return &b }

	topo := &topology.CombinedTopology{
		Topology: topology.Topology{
			Services: []topology.Service{{
				ServiceGroup: "SG.Infra",
				PipelinePath: "infra/pipeline.yaml",
				Purpose:      "infra",
				Children: []topology.Service{{
					ServiceGroup: "SG.Mgmt",
					PipelinePath: "mgmt/pipeline.yaml",
					Purpose:      "mgmt",
					Stamped:      stamped(),
				}},
			}},
			Entrypoints: []topology.Entrypoint{{Identifier: "SG.Infra"}},
		},
	}
	topo.PropagateStamped()

	infraPipeline := &types.Pipeline{
		ServiceGroup: "SG.Infra",
		ResourceGroups: []*types.ResourceGroup{{
			ResourceGroupMeta: &types.ResourceGroupMeta{
				Name: "infra-rg", ResourceGroup: "infra-rg", Subscription: TEST_SUBSCRIPTION_ID,
			},
			Steps: []types.Step{
				&types.ShellStep{StepMeta: types.StepMeta{Name: "setup"}},
			},
		}},
	}

	makeStampPipelines := func(stamp string) map[string]*types.Pipeline {
		return map[string]*types.Pipeline{
			"SG.Infra": infraPipeline,
			"SG.Mgmt": {
				ServiceGroup: "SG.Mgmt",
				ResourceGroups: []*types.ResourceGroup{{
					ResourceGroupMeta: &types.ResourceGroupMeta{
						Name: "mgmt-rg", ResourceGroup: "mgmt-rg-" + stamp, Subscription: TEST_SUBSCRIPTION_ID,
					},
					Steps: []types.Step{
						&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
					},
				}},
			},
		}
	}

	lock := sync.Mutex{}
	infraOutputSeen := map[string]string{}

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		if s.StepName() == "setup" {
			return ArmOutput{"endpoint": map[string]any{"type": "string", "value": "https://infra.example.com"}}, nil, nil
		}
		if s.StepName() == "deploy" {
			state.RLock()
			outputs := state.GetOutputs(id.Stamp)
			state.RUnlock()
			if sg, ok := outputs["SG.Infra"]; ok {
				if rg, ok := sg["infra-rg"]; ok {
					if step, ok := rg["setup"]; ok {
						val, _ := step.GetValue("endpoint")
						lock.Lock()
						infraOutputSeen[id.Stamp] = val.Value.(string)
						lock.Unlock()
					}
				}
			}
		}
		return nil, nil, nil
	}

	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), logger)

	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	_, err = RunEntrypoint(topo, &topo.Entrypoints[0], makeStampPipelines("1"), ctx, &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{BicepClient: lspClient},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return TEST_SUBSCRIPTION_ID, nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) { return ".", nil },
		StampConfigs: map[string]configtypes.Configuration{
			"1": {"stamp": "1"},
			"2": {"stamp": "2"},
		},
		StampPipelines: map[string]map[string]*types.Pipeline{
			"1": makeStampPipelines("1"),
			"2": makeStampPipelines("2"),
		},
	}, executor)
	assert.NoError(t, err)

	assert.Equal(t, "https://infra.example.com", infraOutputSeen["1"], "stamp 1 should see unstamped infra output")
	assert.Equal(t, "https://infra.example.com", infraOutputSeen["2"], "stamp 2 should see unstamped infra output")
}

func TestMultiStampErrorPropagation(t *testing.T) {
	stamped := func() *bool { b := true; return &b }

	topo := &topology.CombinedTopology{
		Topology: topology.Topology{
			Services: []topology.Service{{
				ServiceGroup: "SG.Mgmt",
				PipelinePath: "mgmt/pipeline.yaml",
				Purpose:      "mgmt",
				Stamped:      stamped(),
			}},
			Entrypoints: []topology.Entrypoint{{Identifier: "SG.Mgmt"}},
		},
	}
	topo.PropagateStamped()

	makeStampPipelines := func(stamp string) map[string]*types.Pipeline {
		return map[string]*types.Pipeline{
			"SG.Mgmt": {
				ServiceGroup: "SG.Mgmt",
				ResourceGroups: []*types.ResourceGroup{{
					ResourceGroupMeta: &types.ResourceGroupMeta{
						Name: "rg", ResourceGroup: "mgmt-rg-" + stamp, Subscription: TEST_SUBSCRIPTION_ID,
					},
					Steps: []types.Step{
						&types.ShellStep{StepMeta: types.StepMeta{Name: "deploy"}},
					},
				}},
			},
		}
	}

	var executor Executor = func(id graph.Identifier, s types.Step, ctx context.Context, executionTarget ExecutionTarget, options *StepRunOptions, state *ExecutionState) (Output, DetailsProducer, error) {
		if id.Stamp == "2" {
			return nil, nil, fmt.Errorf("deployment failed for stamp 2")
		}
		return nil, nil, nil
	}

	logger := testr.New(t)
	ctx := logr.NewContext(t.Context(), logger)

	lspClient, err := bicep.StartJSONRPCServer(ctx, logger, false)
	if err != nil {
		t.Fatalf("failed to start bicep language server: %v", err)
	}

	_, err = RunEntrypoint(topo, &topo.Entrypoints[0], makeStampPipelines("1"), ctx, &PipelineRunOptions{
		BaseRunOptions: BaseRunOptions{BicepClient: lspClient},
		SubsciptionLookupFunc: func(_ context.Context, _ string) (string, error) {
			return TEST_SUBSCRIPTION_ID, nil
		},
		TopoDirLookupFunc: func(_ string) (string, error) { return ".", nil },
		StampConfigs: map[string]configtypes.Configuration{
			"1": {"stamp": "1"},
			"2": {"stamp": "2"},
		},
		StampPipelines: map[string]map[string]*types.Pipeline{
			"1": makeStampPipelines("1"),
			"2": makeStampPipelines("2"),
		},
	}, executor)

	assert.Error(t, err, "RunEntrypoint should return error when a stamp fails")
	assert.Contains(t, err.Error(), "stamp 2", "error should contain stamp context")
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
			result := shouldRetryError(testr.New(t), tc.step, tc.err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestShouldExecuteStep(t *testing.T) {
	testCases := []struct {
		name     string
		step     types.Step
		runCount int
		expected bool
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
			runCount: 0,
			expected: true,
		},
		{
			name: "default, no retries",
			step: &types.ShellStep{
				StepMeta: types.StepMeta{
					AutomatedRetry: nil,
				},
			},
			runCount: 0,
			expected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := shouldExecuteStep(tc.step, tc.runCount)
			assert.Equal(t, tc.expected, result)
		})
	}
}
