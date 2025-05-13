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

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/types"
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
			caseStep: types.NewShellStep("step", "echo hello").WithVariables(types.Variable{
				Name:      "FOO",
				ConfigRef: "foo",
			}),
			options: &InspectOptions{
				Configuration: config.Configuration{
					"foo": "bar",
				},
				Format: "shell",
			},
			expected: "export FOO=\"bar\"\n",
		},
		{
			name: "makefile",
			caseStep: types.NewShellStep("step", "echo hello").WithVariables(types.Variable{
				Name:      "FOO",
				ConfigRef: "foo",
			}),
			options: &InspectOptions{
				Configuration: config.Configuration{
					"foo": "bar",
				},
				Format: "makefile",
			},
			expected: "FOO ?= bar\n",
		},
		{
			name:     "failed action",
			caseStep: types.NewARMStep("step", "test.bicep", "test.bicepparam", "ResourceGroup"),
			options:  &InspectOptions{},
			err:      "inspecting step variables not implemented for action type ARM",
		},
		{
			name:     "failed format",
			caseStep: types.NewShellStep("step", "echo hello"),
			options:  &InspectOptions{Format: "unknown"},
			err:      "unknown output format \"unknown\"",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := new(bytes.Buffer)
			tc.options.OutputFile = buf
			err := inspectVars(context.Background(), &types.Pipeline{}, tc.caseStep, tc.options)
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
				types.NewShellStep("step1", "echo hello"),
			},
		},
		},
	}
	opts := NewInspectOptions(config.Configuration{}, "", "step1", "scope", "format", new(bytes.Buffer))

	opts.ScopeFunctions = map[string]StepInspectScope{
		"scope": func(ctx context.Context, p *types.Pipeline, s types.Step, o *InspectOptions) error {
			assert.Equal(t, s.StepName(), "step1")
			return nil
		},
	}

	err := Inspect(&p, context.Background(), opts)
	assert.NoError(t, err)
}

func TestInspectWrongScope(t *testing.T) {
	p := types.Pipeline{
		ResourceGroups: []*types.ResourceGroup{{
			Steps: []types.Step{
				types.NewShellStep("step1", "echo hello"),
			},
		},
		},
	}
	opts := NewInspectOptions(config.Configuration{}, "", "step1", "foo", "format", new(bytes.Buffer))

	err := Inspect(&p, context.Background(), opts)
	assert.Error(t, err, "unknown inspect scope \"foo\"")
}
