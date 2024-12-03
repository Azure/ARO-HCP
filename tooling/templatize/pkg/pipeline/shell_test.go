package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestCreateCommand(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name           string
		step           *Step
		dryRun         bool
		envVars        map[string]string
		expectedScript string
		expectedEnv    []string
		skipCommand    bool
	}{
		{
			name: "basic",
			step: &Step{
				Command: "/usr/bin/echo hello",
			},
			expectedScript: buildBashScript("/usr/bin/echo hello"),
		},
		{
			name: "dry-run",
			step: &Step{
				Command: "/usr/bin/echo hello",
				DryRun: DryRun{
					Command: "/usr/bin/echo dry-run",
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/usr/bin/echo dry-run"),
		},
		{
			name: "dry-run-env",
			step: &Step{
				Command: "/usr/bin/echo",
				DryRun: DryRun{
					EnvVars: []EnvVar{
						{
							Name:  "DRY_RUN",
							Value: "true",
						},
					},
				},
			},
			dryRun:         true,
			expectedScript: buildBashScript("/usr/bin/echo"),
			envVars:        map[string]string{},
			expectedEnv:    []string{"DRY_RUN=true"},
		},
		{
			name: "dry-run fail",
			step: &Step{
				Command: "/usr/bin/echo",
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
		step     Step
		expected map[string]string
		err      string
	}{
		{
			name: "basic",
			vars: config.Variables{
				"FOO": "bar",
			},
			step: Step{
				Env: []EnvVar{
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
			step: Step{
				Env: []EnvVar{
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
			step: Step{
				Env: []EnvVar{
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
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			envVars, err := tc.step.mapStepVariables(tc.vars)
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
		step *Step
		err  string
	}{
		{
			name: "basic",
			vars: config.Variables{},
			step: &Step{
				Command: "echo hello",
			},
		},
		{
			name: "test nounset",
			vars: config.Variables{},
			step: &Step{
				Command: "echo $DOES_NOT_EXIST",
			},
			err: "DOES_NOT_EXIST: unbound variable\n exit status 1",
		},
		{
			name: "test errexit",
			vars: config.Variables{},
			step: &Step{
				Command: "false ; echo hello",
			},
			err: "failed to execute shell command:  exit status 1",
		},
		{
			name: "test pipefail",
			vars: config.Variables{},
			step: &Step{
				Command: "false | echo",
			},
			err: "failed to execute shell command: \n exit status 1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.step.runShellStep(context.Background(), "", &PipelineRunOptions{}, map[string]output{})
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestAddInputVars(t *testing.T) {
	mapOutput := map[string]output{}
	mapOutput["step1"] = armOutput{
		"output1": map[string]any{
			"type":  "String",
			"value": "value1",
		},
	}
	s := &Step{
		Name: "step2",
		Inputs: []Input{
			{
				Name:   "input1",
				Step:   "step1",
				Output: "output1",
			},
		},
	}

	envVars := s.addInputVars(mapOutput)
	assert.DeepEqual(t, envVars, map[string]string{"input1": "value1"})
}
