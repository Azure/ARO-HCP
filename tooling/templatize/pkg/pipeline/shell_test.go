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
	"maps"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"
)

func TestCreateCommand(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name           string
		step           *types.ShellStep
		dryRun         bool
		envVars        map[string]string
		expectedScript string
		expectedEnv    string
		configuration  config.Configuration
		skipCommand    bool
	}{
		{
			name: "basic",
			step: &types.ShellStep{
				Command: "/bin/echo hello",
			},
			expectedScript: buildBashScript("/bin/echo hello"),
		},
		{
			name: "dry-run",
			step: &types.ShellStep{
				Command: "/bin/echo hello",
				DryRun: types.DryRun{
					Command: "/bin/echo dry-run",
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/bin/echo dry-run"),
		},
		{
			name: "dry-run-env",
			step: &types.ShellStep{
				Command: "/bin/echo",
				DryRun: types.DryRun{
					Variables: []types.Variable{
						{
							Name: "DRY_RUN",
							Value: types.Value{
								Value: "true",
							},
						},
					},
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/bin/echo"),
			envVars:        map[string]string{},
			expectedEnv:    "DRY_RUN=true",
		},
		{
			name: "dry-run-configref",
			step: &types.ShellStep{
				Command: "/bin/echo",
				DryRun: types.DryRun{
					Variables: []types.Variable{
						{
							Name: "DRY_RUN",
							Value: types.Value{
								ConfigRef: "test",
							},
						},
					},
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/bin/echo"),
			envVars:        map[string]string{},
			configuration:  config.Configuration{"test": "foobar"},
			expectedEnv:    "DRY_RUN=foobar",
		},
		{
			name: "dry-run fail",
			step: &types.ShellStep{
				Command: "/bin/echo",
			},
			dryRun:      true,
			skipCommand: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var dryRun *types.DryRun
			if tc.dryRun {
				dryRun = &tc.step.DryRun
			}
			dryRunVars, err := mapStepVariables(tc.step.DryRun.Variables, tc.configuration, map[string]Output{})
			assert.NoError(t, err)
			maps.Copy(tc.envVars, dryRunVars)

			cmd, skipCommand := createCommand(ctx, tc.step.Command, "", dryRun, tc.envVars)
			assert.Empty(t, cmp.Diff(skipCommand, tc.skipCommand))
			if !tc.skipCommand {
				assert.Equal(t, strings.Join(cmd.Args, " "), fmt.Sprintf("/bin/bash -c %s", tc.expectedScript))
			}
			if tc.expectedEnv != "" {
				assert.Contains(t, cmd.Env, tc.expectedEnv)
			}
		})
	}

}

func TestMapStepVariables(t *testing.T) {
	testCases := []struct {
		name     string
		cfg      config.Configuration
		input    map[string]Output
		step     *types.ShellStep
		expected map[string]string
		err      string
	}{
		{
			name: "basic",
			cfg: config.Configuration{
				"FOO": "bar",
			},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							ConfigRef: "FOO",
						},
					},
				},
			},
			expected: map[string]string{
				"BAZ": "bar",
			},
		},
		{
			name: "missing",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Value: types.Value{
							ConfigRef: "FOO",
						},
					},
				},
			},
			err: "failed to lookup config reference FOO for ",
		},
		{
			name: "type conversion",
			cfg: config.Configuration{
				"FOO": 42,
			},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							ConfigRef: "FOO",
						},
					},
				},
			},
			expected: map[string]string{
				"BAZ": "42",
			},
		},
		{
			name: "value",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							Value: "bar",
						},
					},
				},
			},
			expected: map[string]string{
				"BAZ": "bar",
			},
		},
		{
			name: "output chaining",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							Input: &types.Input{
								Name: "output1",
								Step: "step1",
							},
						},
					},
				},
			},
			input: map[string]Output{
				"step1": ArmOutput{
					"output1": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			expected: map[string]string{
				"BAZ": "bar",
			},
		},
		{
			name: "output chaining step missing",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							Input: &types.Input{
								Name: "output1",
								Step: "step1",
							},
						},
					},
				},
			},
			err: "step step1 not found in provided outputs",
		},
		{
			name: "output chaining output missing",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
						Value: types.Value{
							Input: &types.Input{
								Name: "output1",
								Step: "step1",
							},
						},
					},
				},
			},
			input: map[string]Output{
				"step1": ArmOutput{
					"anotheroutput": map[string]any{
						"type":  "String",
						"value": "bar",
					},
				},
			},
			err: "failed to get value for input step1.output1: key \"output1\" not found",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			envVars, err := mapStepVariables(tc.step.Variables, tc.cfg, tc.input)
			t.Log(envVars)
			if tc.err != "" {
				assert.Error(t, err, tc.err)
			} else {
				assert.NoError(t, err)
				assert.Empty(t, cmp.Diff(envVars, tc.expected))
			}
		})
	}
}

func TestRunShellStep(t *testing.T) {
	var buf bytes.Buffer
	testCases := []struct {
		name string
		cfg  config.Configuration
		step *types.ShellStep
		err  string
	}{
		{
			name: "basic",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Command: "echo hello",
			},
		},
		{
			name: "test nounset",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Command: "echo $DOES_NOT_EXIST",
			},
			err: "DOES_NOT_EXIST: unbound variable\n exit status 1",
		},
		{
			name: "test errexit",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Command: "false ; echo hello",
			},
			err: "failed to execute shell command:  exit status 1",
		},
		{
			name: "test pipefail",
			cfg:  config.Configuration{},
			step: &types.ShellStep{
				Command: "false | echo",
			},
			err: "failed to execute shell command: \n exit status 1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			target := &executionTargetImpl{
				subscriptionID: "test-subscription-id",
				resourceGroup:  "test-resource-group",
				region:         "westus3",
			}
			err := runShellStep(tc.step, context.Background(), "", target, &PipelineRunOptions{}, map[string]Output{}, &buf)
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRunShellStepCaptureOutput(t *testing.T) {
	step := &types.ShellStep{
		Command: "echo hallo",
	}
	var buf bytes.Buffer

	target := &executionTargetImpl{
		subscriptionID: "test-subscription-id",
		resourceGroup:  "test-resource-group",
		region:         "westus3",
	}
	err := runShellStep(step, context.Background(), "", target, &PipelineRunOptions{}, map[string]Output{}, &buf)
	assert.NoError(t, err)
	assert.Equal(t, buf.String(), "hallo\n")
}

func TestRunShellStepEnvironmentVariables(t *testing.T) {
	testCases := []struct {
		name            string
		step            *types.ShellStep
		kubeconfigFile  string
		executionTarget ExecutionTarget
		configuration   config.Configuration
		expectedOutput  string
		expectedError   string
	}{
		{
			name: "test ResourceGroup and Subscription environment variables",
			step: &types.ShellStep{
				Command: "echo \"RG: $ResourceGroup, SUB: $Subscription\"",
			},
			executionTarget: &executionTargetImpl{
				subscriptionID: "12345-abcde",
				resourceGroup:  "my-resource-group",
				region:         "westus3",
			},
			expectedOutput: "RG: my-resource-group, SUB: 12345-abcde\n",
		},
		{
			name: "test AKSCluster environment variable with kubeconfig",
			step: &types.ShellStep{
				Command:    "echo \"AKS: $AKSCluster\"",
				AKSCluster: "my-aks-cluster",
			},
			kubeconfigFile: "/tmp/kubeconfig",
			executionTarget: &executionTargetImpl{
				subscriptionID: "12345-abcde",
				resourceGroup:  "my-resource-group",
				region:         "westus3",
			},
			expectedOutput: "AKS: my-aks-cluster\n",
		},
		{
			name: "test all environment variables together with kubeconfig",
			step: &types.ShellStep{
				Command:    "echo \"RG: $ResourceGroup, SUB: $Subscription, AKS: $AKSCluster, KUBE: $KUBECONFIG\"",
				AKSCluster: "test-cluster",
			},
			kubeconfigFile: "/tmp/kubeconfig",
			executionTarget: &executionTargetImpl{
				subscriptionID: "test-sub-123",
				resourceGroup:  "test-rg",
				region:         "westus3",
			},
			expectedOutput: "RG: test-rg, SUB: test-sub-123, AKS: test-cluster, KUBE: /tmp/kubeconfig\n",
		},
		{
			name: "test without AKSCluster defined should cause unbound variable error",
			step: &types.ShellStep{
				Command: "echo \"AKS: $AKSCluster\"",
			},
			executionTarget: &executionTargetImpl{
				subscriptionID: "test-sub-123",
				resourceGroup:  "test-rg",
				region:         "westus3",
			},
			expectedError: "AKSCluster: unbound variable",
		},
		{
			name: "test without kubeconfig should cause unbound KUBECONFIG variable error",
			step: &types.ShellStep{
				Command: "echo \"KUBE: $KUBECONFIG\"",
			},
			executionTarget: &executionTargetImpl{
				subscriptionID: "test-sub-123",
				resourceGroup:  "test-rg",
				region:         "westus3",
			},
			expectedError: "KUBECONFIG: unbound variable",
		},
		{
			name: "test shell step variable definitions are honored as environment variables",
			step: &types.ShellStep{
				Command: "echo \"Static: $STATIC_VAR, Config: $CONFIG_VAR\"",
				Variables: []types.Variable{
					{
						Name: "STATIC_VAR",
						Value: types.Value{
							Value: "custom-value",
						},
					},
					{
						Name: "CONFIG_VAR",
						Value: types.Value{
							ConfigRef: "testconfig",
						},
					},
				},
			},
			executionTarget: &executionTargetImpl{
				subscriptionID: "test-sub-123",
				resourceGroup:  "test-rg",
				region:         "westus3",
			},
			configuration: config.Configuration{
				"testconfig": "config-value",
			},
			expectedOutput: "Static: custom-value, Config: config-value\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			options := &PipelineRunOptions{
				Configuration: tc.configuration,
			}
			err := runShellStep(tc.step, context.Background(), tc.kubeconfigFile, tc.executionTarget, options, map[string]Output{}, &buf)
			if tc.expectedError != "" {
				assert.ErrorContains(t, err, tc.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedOutput, buf.String())
			}
		})
	}
}
