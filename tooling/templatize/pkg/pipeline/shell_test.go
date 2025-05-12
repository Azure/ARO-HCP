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
							Name:  "DRY_RUN",
							Value: "true",
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
							Name:      "DRY_RUN",
							ConfigRef: "test",
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

			cmd, skipCommand := createCommand(ctx, tc.step.Command, dryRun, tc.envVars)
			assert.Equal(t, skipCommand, tc.skipCommand)
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
						Name:      "BAZ",
						ConfigRef: "FOO",
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
						ConfigRef: "FOO",
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
						Name:      "BAZ",
						ConfigRef: "FOO",
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
						Name:  "BAZ",
						Value: "bar",
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
						Input: &types.Input{
							Name: "output1",
							Step: "step1",
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
						Input: &types.Input{
							Name: "output1",
							Step: "step1",
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
						Input: &types.Input{
							Name: "output1",
							Step: "step1",
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
				assert.Equal(t, envVars, tc.expected)
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
			err := runShellStep(tc.step, context.Background(), "", &PipelineRunOptions{}, map[string]Output{}, &buf)
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

	err := runShellStep(step, context.Background(), "", &PipelineRunOptions{}, map[string]Output{}, &buf)
	assert.NoError(t, err)
	assert.Equal(t, buf.String(), "hallo\n")
}
