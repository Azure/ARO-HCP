package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func TestCreateCommand(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name           string
		step           *ShellStep
		dryRun         bool
		envVars        map[string]string
		expectedScript string
		expectedEnv    []string
		skipCommand    bool
	}{
		{
			name: "basic",
			step: &ShellStep{
				Command: "/bin/echo hello",
			},
			expectedScript: buildBashScript("/bin/echo hello"),
		},
		{
			name: "dry-run",
			step: &ShellStep{
				Command: "/bin/echo hello",
				DryRun: DryRun{
					Command: "/bin/echo dry-run",
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/bin/echo dry-run"),
		},
		{
			name: "dry-run-env",
			step: &ShellStep{
				Command: "/bin/echo",
				DryRun: DryRun{
					Variables: []Variable{
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
			expectedEnv:    []string{"DRY_RUN=true"},
		},
		{
			name: "dry-run fail",
			step: &ShellStep{
				Command: "/bin/echo",
			},
			dryRun:      true,
			skipCommand: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, skipCommand := tc.step.createCommand(ctx, tc.dryRun, tc.envVars)
			assert.Equal(t, skipCommand, tc.skipCommand)
			if !tc.skipCommand {
				assert.Equal(t, strings.Join(cmd.Args, " "), fmt.Sprintf("/bin/bash -c %s", tc.expectedScript))
			}
			if tc.expectedEnv != nil {
				assert.DeepEqual(t, cmd.Env, tc.expectedEnv)
			}
		})
	}

}

func TestMapStepVariables(t *testing.T) {
	testCases := []struct {
		name     string
		vars     config.Variables
		input    map[string]output
		step     *ShellStep
		expected map[string]string
		err      string
	}{
		{
			name: "basic",
			vars: config.Variables{
				"FOO": "bar",
			},
			step: &ShellStep{
				Variables: []Variable{
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
			vars: config.Variables{},
			step: &ShellStep{
				Variables: []Variable{
					{
						ConfigRef: "FOO",
					},
				},
			},
			err: "failed to lookup config reference FOO for ",
		},
		{
			name: "type conversion",
			vars: config.Variables{
				"FOO": 42,
			},
			step: &ShellStep{
				Variables: []Variable{
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
			vars: config.Variables{},
			step: &ShellStep{
				Variables: []Variable{
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
			vars: config.Variables{},
			step: &ShellStep{
				Variables: []Variable{
					{
						Name: "BAZ",
						Input: &Input{
							Name: "output1",
							Step: "step1",
						},
					},
				},
			},
			input: map[string]output{
				"step1": armOutput{
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
			vars: config.Variables{},
			step: &ShellStep{
				Variables: []Variable{
					{
						Name: "BAZ",
						Input: &Input{
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
			vars: config.Variables{},
			step: &ShellStep{
				Variables: []Variable{
					{
						Name: "BAZ",
						Input: &Input{
							Name: "output1",
							Step: "step1",
						},
					},
				},
			},
			input: map[string]output{
				"step1": armOutput{
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
			envVars, err := tc.step.mapStepVariables(tc.vars, tc.input)
			t.Log(envVars)
			if tc.err != "" {
				assert.Error(t, err, tc.err)
			} else {
				assert.NilError(t, err)
				assert.DeepEqual(t, envVars, tc.expected)
			}
		})
	}
}

func TestRunShellStep(t *testing.T) {
	testCases := []struct {
		name string
		vars config.Variables
		step *ShellStep
		err  string
	}{
		{
			name: "basic",
			vars: config.Variables{},
			step: &ShellStep{
				Command: "echo hello",
			},
		},
		{
			name: "test nounset",
			vars: config.Variables{},
			step: &ShellStep{
				Command: "echo $DOES_NOT_EXIST",
			},
			err: "DOES_NOT_EXIST: unbound variable\n exit status 1",
		},
		{
			name: "test errexit",
			vars: config.Variables{},
			step: &ShellStep{
				Command: "false ; echo hello",
			},
			err: "failed to execute shell command:  exit status 1",
		},
		{
			name: "test pipefail",
			vars: config.Variables{},
			step: &ShellStep{
				Command: "false | echo",
			},
			err: "failed to execute shell command: \n exit status 1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := runShellStep(tc.step, context.Background(), "", &PipelineRunOptions{}, map[string]output{})
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
