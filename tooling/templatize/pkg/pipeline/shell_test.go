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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/graph"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

func TestCreateCommand(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name           string
		step           *types.ShellStep
		envVars        map[string]string
		expectedScript string
		expectedEnv    string
		configuration  configtypes.Configuration
	}{
		{
			name: "basic",
			step: &types.ShellStep{
				Command: "/bin/echo hello",
			},
			expectedScript: buildBashScript("/bin/echo hello"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := createCommand(ctx, tc.step.Command, "", tc.envVars)
			assert.Equal(t, strings.Join(cmd.Args, " "), fmt.Sprintf("/bin/bash -c %s", tc.expectedScript))
			if tc.expectedEnv != "" {
				assert.Contains(t, cmd.Env, tc.expectedEnv)
			}
		})
	}

}

func TestMapStepVariables(t *testing.T) {
	testCases := []struct {
		name     string
		cfg      configtypes.Configuration
		input    Outputs
		step     *types.ShellStep
		expected map[string]string
		err      string
	}{
		{
			name: "basic",
			cfg: configtypes.Configuration{
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
			cfg:  configtypes.Configuration{},
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
			cfg: configtypes.Configuration{
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
			cfg:  configtypes.Configuration{},
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
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
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
			},
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
			expected: map[string]string{
				"BAZ": "bar",
			},
		},
		{
			name: "output chaining step missing",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
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
			},
			err: "step step1 not found in provided outputs",
		},
		{
			name: "output chaining output missing",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Variables: []types.Variable{
					{
						Name: "BAZ",
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
			},
			input: Outputs{
				"Microsoft.Azure.ARO.Whatever": map[string]map[string]Output{
					"rg": {
						"step1": ArmOutput{
							"anotheroutput": map[string]any{
								"type":  "String",
								"value": "bar",
							},
						},
					},
				},
			},
			err: "failed to get value for input step1.output1: key \"output1\" not found",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			envVars, err := mapStepVariables("Microsoft.Azure.ARO.Whatever", tc.step.Variables, tc.cfg, tc.input)
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
		cfg  configtypes.Configuration
		step *types.ShellStep
		err  string
	}{
		{
			name: "basic",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Command: "echo hello",
			},
		},
		{
			name: "test nounset",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Command: "echo $DOES_NOT_EXIST",
			},
			err: "DOES_NOT_EXIST: unbound variable\n exit status 1",
		},
		{
			name: "test errexit",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Command: "false ; echo hello",
			},
			err: "failed to execute shell command:  exit status 1",
		},
		{
			name: "test pipefail",
			cfg:  configtypes.Configuration{},
			step: &types.ShellStep{
				Command: "false | echo",
			},
			err: "failed to execute shell command: \n exit status 1",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := runShellStep(graph.Identifier{
				ServiceGroup: "Microsoft.Azure.ARO.Whatever",
				StepDependency: types.StepDependency{
					ResourceGroup: "group",
					Step:          "step",
				},
			}, tc.step, context.Background(), "", "", &StepRunOptions{}, NewExecutionState(), &buf)
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

	err := runShellStep(graph.Identifier{
		ServiceGroup: "Microsoft.Azure.ARO.Whatever",
		StepDependency: types.StepDependency{
			ResourceGroup: "group",
			Step:          "step",
		},
	}, step, context.Background(), "", "", &StepRunOptions{}, NewExecutionState(), &buf)
	assert.NoError(t, err)
	assert.Equal(t, buf.String(), "hallo\n")
}

// Azure CLI rewrites azureProfile.json non-atomically and without locking, so steps that share a
// config directory can corrupt each other's view of the logged-in subscriptions. Each step must get
// a private copy of the template that is cleaned up afterwards and never written to.
func TestRunShellStepIsolatesAzureConfigDir(t *testing.T) {
	const original = "original-profile"
	template := t.TempDir()
	profile := filepath.Join(template, "azureProfile.json")
	assert.NoError(t, os.WriteFile(profile, []byte(original), 0o600))

	type result struct {
		dir, before, after string
		err                error
	}
	const steps = 8
	results := make([]result, steps)
	var wg sync.WaitGroup
	// Release all steps together so their profile writes overlap.
	start := make(chan struct{})
	for i := range steps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf bytes.Buffer
			step := &types.ShellStep{
				Command: fmt.Sprintf(`echo "$AZURE_CONFIG_DIR"
cat "$AZURE_CONFIG_DIR/azureProfile.json"; echo
printf 'step-%d' > "$AZURE_CONFIG_DIR/azureProfile.json"
sleep 0.2
cat "$AZURE_CONFIG_DIR/azureProfile.json"`, i),
			}
			<-start
			err := runShellStep(graph.Identifier{
				ServiceGroup: "Microsoft.Azure.ARO.Whatever",
				StepDependency: types.StepDependency{
					ResourceGroup: "group",
					Step:          fmt.Sprintf("step-%d", i),
				},
			}, step, context.Background(), template, "", &StepRunOptions{}, NewExecutionState(), &buf)
			lines := strings.Split(buf.String(), "\n")
			if err == nil && len(lines) == 3 {
				results[i] = result{dir: lines[0], before: lines[1], after: lines[2]}
			} else {
				results[i].err = fmt.Errorf("err=%v output=%q", err, buf.String())
			}
		}()
	}
	close(start)
	wg.Wait()

	dirs := map[string]bool{}
	for i, r := range results {
		assert.NoError(t, r.err)
		assert.Equal(t, original, r.before, "step %d must start from the template profile", i)
		assert.Equal(t, fmt.Sprintf("step-%d", i), r.after, "step %d must not see another step's write", i)
		assert.NotEqual(t, template, r.dir, "step %d must not be handed the template", i)
		assert.False(t, dirs[r.dir], "step %d must get its own directory", i)
		dirs[r.dir] = true
		_, err := os.Stat(r.dir)
		assert.True(t, os.IsNotExist(err), "step %d config dir must be removed after the step", i)
	}

	contents, err := os.ReadFile(profile)
	assert.NoError(t, err)
	assert.Equal(t, original, string(contents), "template must never be written by a step")
}
