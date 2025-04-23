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

package ev2

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func TestProcessPipelineForEV2(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetDeployEnvRegionConfiguration("public", "int", "", NewEv2ConfigReplacements())
	if err != nil {
		t.Errorf("failed to get variables: %v", err)
	}
	originalPipeline, err := pipeline.NewPipelineFromFile("../../testdata/pipeline.yaml", vars)
	if err != nil {
		t.Errorf("failed to read new pipeline: %v", err)
	}
	files := make(map[string][]byte)
	files["test.bicepparam"] = []byte(
		strings.Join(
			[]string{
				"param regionRG = '{{ .regionRG }}'",
				"param replicas = {{ .clusterService.replicas }}",
			},
			"\n",
		),
	)

	newPipeline, newFiles, err := processPipelineForEV2(originalPipeline, files, vars)
	if err != nil {
		t.Errorf("failed to precompile pipeline: %v", err)
	}

	// verify pipeline
	pipelineContent, err := yaml.Marshal(newPipeline)
	if err != nil {
		t.Errorf("failed to marshal processed pipeline: %v", err)
	}
	testutil.CompareWithFixture(t, pipelineContent, testutil.WithExtension("pipeline.yaml"))

	// verify referenced files
	for filePath, content := range newFiles {
		testutil.CompareWithFixture(t, content, testutil.WithExtension(filePath))
	}
}
