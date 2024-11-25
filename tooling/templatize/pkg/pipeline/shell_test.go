package pipeline

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestCreateCommand(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name            string
		step            *step
		dryRun          bool
		envVars         map[string]string
		expectedCommand string
		expectedArgs    []string
		expectedEnv     []string
		skipCommand     bool
	}{
		{
			name: "basic",
			step: &step{
				Command: []string{"/usr/bin/echo", "hello"},
			},
			expectedCommand: "/usr/bin/echo",
			expectedArgs:    []string{"hello"},
		},
		{
			name: "dry-run",
			step: &step{
				Command: []string{"/usr/bin/echo", "hello"},
				DryRun: dryRun{
					Command: []string{"/usr/bin/echo", "dry-run"},
				},
			},
			dryRun:          true,
			expectedCommand: "/usr/bin/echo",
			expectedArgs:    []string{"dry-run"},
		},
		{
			name: "dry-run-env",
			step: &step{
				Command: []string{"/usr/bin/echo"},
				DryRun: dryRun{
					EnvVars: []EnvVar{
						{
							Name:  "DRY_RUN",
							Value: "true",
						},
					},
				},
			},
			dryRun:          true,
			expectedCommand: "/usr/bin/echo",
			envVars:         map[string]string{},
			expectedEnv:     []string{"DRY_RUN=true"},
		},
		{
			name: "dry-run fail",
			step: &step{
				Command: []string{"/usr/bin/echo"},
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
				assert.Equal(t, cmd.Path, tc.expectedCommand)
			}
			if tc.expectedArgs != nil {
				assert.DeepEqual(t, cmd.Args[1:], tc.expectedArgs)
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
		step     step
		expected map[string]string
		err      string
	}{
		{
			name: "basic",
			vars: config.Variables{
				"FOO": "bar",
			},
			step: step{
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
			step: step{
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
			step: step{
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
	expectedOutput := "hello\n"
	s := &step{
		Command: []string{"echo", "hello"},
		outputFunc: func(output string) {
			assert.Equal(t, output, expectedOutput)
		},
	}
	err := s.runShellStep(context.Background(), "", &PipelineRunOptions{})
	assert.NilError(t, err)
}
