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

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Azure/ARO-Tools/config/types"
	"github.com/Azure/ARO-Tools/pipelines/topology"
)

func TestRecursiveLoadPipelineReturnHelmSteps(t *testing.T) {
	testCases := []struct {
		name           string
		services       topology.Service
		numSteps       int
		numMirrorSteps []int // expected number of mirror steps per helm step
		expectError    bool
		errorMessage   string
	}{
		{
			name: "simple",
			services: topology.Service{
				PipelinePath: "../tooling/helmtest/testdata/pipeline_with_helmstep.yaml",
			},
			numSteps:       1,
			numMirrorSteps: []int{0},
		},
		{
			name: "broken",
			services: topology.Service{
				PipelinePath: "../tooling/helmtest/testdata/pipeline_broken.yaml",
			},
			expectError:  true,
			errorMessage: "failed to validate pipeline schema",
		},
		{
			name: "with children",
			services: topology.Service{
				PipelinePath: "../tooling/helmtest/testdata/pipeline_without_helmstep.yaml",
				Children: []topology.Service{
					{
						PipelinePath: "../tooling/helmtest/testdata/pipeline_with_helmstep.yaml",
						Children: []topology.Service{
							{
								PipelinePath: "../tooling/helmtest/testdata/pipeline_with_helmstep.yaml",
							},
						},
					},
				},
			},
			numSteps:       2,
			numMirrorSteps: []int{0, 0},
		},
		{
			name: "with mirror steps",
			services: topology.Service{
				PipelinePath: "../tooling/helmtest/testdata/pipeline_with_helmstep_and_mirror.yaml",
			},
			numSteps:       1,
			numMirrorSteps: []int{2},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hs, err := recursiveLoadPipelineReturnHelmSteps("../..", tc.services, types.Configuration{})
			if tc.expectError {
				assert.ErrorContains(t, err, tc.errorMessage)
			} else {
				assert.NoError(t, err)
			}
			assert.Len(t, hs, tc.numSteps)
			for i, expected := range tc.numMirrorSteps {
				if i < len(hs) {
					assert.Len(t, hs[i].MirrorImageSteps, expected, "helm step %d should have %d mirror steps", i, expected)
				}
			}
		})
	}
}
