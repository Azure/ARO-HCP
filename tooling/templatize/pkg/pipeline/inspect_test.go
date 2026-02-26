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
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"

	configtypes "github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/types"
)

func TestInspectVars(t *testing.T) {
	testCases := []struct {
		name     string
		caseStep types.Step
		options  *InspectOptions
		expected string
		err      string
	}{
		{
			name: "basic",
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
				Variables: []types.Variable{{
					Name: "FOO",
					Value: types.Value{
						ConfigRef: "foo",
					},
				}},
			},
			options: &InspectOptions{
				Configuration: configtypes.Configuration{
					"foo": "bar",
				},
				Format: "shell",
			},
			expected: "export FOO=\"bar\"\n",
		},
		{
			name: "makefile",
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
				Variables: []types.Variable{{
					Name: "FOO",
					Value: types.Value{
						ConfigRef: "foo",
					},
				}},
			},
			options: &InspectOptions{
				Configuration: configtypes.Configuration{
					"foo": "bar",
				},
				Format: "makefile",
			},
			expected: "FOO ?= bar\n",
		},
		{
			name: "failed action",
			caseStep: &types.ARMStep{
				StepMeta: types.StepMeta{
					Name:   "step",
					Action: "ARM",
				},
				Template:        "test.bicep",
				Parameters:      "test.bicepparam",
				DeploymentLevel: "ResourceGroup",
			},
			options: &InspectOptions{},
			err:     "inspecting step variables not implemented for action type ARM",
		},
		{
			name: "failed format",
			caseStep: &types.ShellStep{
				StepMeta: types.StepMeta{
					Action: "Shell",
					Name:   "step",
				},
				Command: "echo hello",
			},
			options: &InspectOptions{Format: "unknown"},
			err:     "unknown output format \"unknown\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			tc.options.OutputFile = buf
			err := inspectVars(map[string]string{})(logr.NewContext(t.Context(), testr.New(t)), &types.Pipeline{}, "", tc.caseStep, tc.options)
			if tc.err == "" {
				assert.NoError(t, err)
				assert.Equal(t, buf.String(), tc.expected)
			} else {
				assert.ErrorContains(t, err, tc.err)
			}
		})
	}
}

func TestInspect(t *testing.T) {
	p := types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{{
			Steps: []types.Step{
				&types.ShellStep{
					StepMeta: types.StepMeta{
						Action: "Shell",
						Name:   "step1",
					},
					Command: "echo hello",
				},
			},
		},
		},
	}

	err := Inspect(&p, logr.NewContext(t.Context(), testr.New(t)), &InspectOptions{
		Scope:         "scope",
		Format:        "format",
		Step:          "step1",
		Region:        "",
		Configuration: configtypes.Configuration{},
		ScopeFunctions: map[string]StepInspectScope{
			"scope": func(ctx context.Context, p *types.Pipeline, serviceGroup string, s types.Step, o *InspectOptions) error {
				assert.Equal(t, s.StepName(), "step1")
				return nil
			},
		},
		OutputFile: new(bytes.Buffer),
	})
	assert.NoError(t, err)
}

func TestInspectWrongScope(t *testing.T) {
	p := types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{{
			Steps: []types.Step{
				&types.ShellStep{
					StepMeta: types.StepMeta{
						Action: "Shell",
						Name:   "step1",
					},
					Command: "echo hello",
				}},
		},
		},
	}

	err := Inspect(&p, logr.NewContext(t.Context(), testr.New(t)), &InspectOptions{
		Scope:         "foo",
		Format:        "format",
		Step:          "step1",
		Region:        "",
		Configuration: configtypes.Configuration{},
		OutputFile:    new(bytes.Buffer),
	})
	assert.Error(t, err, "unknown inspect scope \"foo\"")
}
