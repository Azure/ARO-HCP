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
