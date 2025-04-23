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
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/Azure/ARO-Tools/pkg/config"
)

func TestDeepCopy(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetDeployEnvRegionConfiguration("public", "int", "", config.NewConfigReplacements("r", "sr", "s"))
	if err != nil {
		t.Errorf("failed to get variables: %v", err)
	}
	pipeline, err := NewPipelineFromFile("../../testdata/pipeline.yaml", vars)
	if err != nil {
		t.Errorf("failed to read new pipeline: %v", err)
	}

	newPipelinePath := "new-pipeline.yaml"
	pipelineCopy, err := pipeline.DeepCopy(newPipelinePath)
	if err != nil {
		t.Errorf("failed to copy pipeline: %v", err)
	}

	assert.Assert(t, pipeline != pipelineCopy, "expected pipeline and copy to be different")
	assert.Equal(t, pipelineCopy.PipelineFilePath(), newPipelinePath, "expected pipeline copy to have new path")

	if diff := cmp.Diff(pipeline, pipelineCopy, cmpopts.IgnoreUnexported(Pipeline{}, ShellStep{}, ARMStep{})); diff != "" {
		t.Errorf("got diffs after pipeline deep copy: %v", diff)
	}
}

func TestAbsoluteFilePath(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetDeployEnvRegionConfiguration("public", "int", "", config.NewConfigReplacements("r", "sr", "s"))
	if err != nil {
		t.Errorf("failed to get variables: %v", err)
	}
	pipeline, err := NewPipelineFromFile("../../testdata/pipeline.yaml", vars)
	if err != nil {
		t.Errorf("failed to read new pipeline: %v", err)
	}

	abspath := func(path string) string {
		abs, _ := filepath.Abs(path)
		return abs
	}
	testCases := []struct {
		name         string
		relativeFile string
		absoluteFile string
	}{
		{
			name:         "basic",
			relativeFile: "test.bicepparam",
			absoluteFile: abspath("../../testdata/test.bicepparam"),
		},
		{
			name:         "go one lower",
			relativeFile: "../test.bicepparam",
			absoluteFile: abspath("../../test.bicepparam"),
		},
		{
			name:         "subdir",
			relativeFile: "subdir/test.bicepparam",
			absoluteFile: abspath("../../testdata/subdir/test.bicepparam"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			abs, err := pipeline.AbsoluteFilePath(tc.relativeFile)
			if err != nil {
				t.Errorf("failed to get absolute file path: %v", err)
			}
			assert.Equal(t, abs, tc.absoluteFile, "expected absolute file path to be correct")
		})
	}

}
