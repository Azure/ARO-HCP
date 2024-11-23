package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gotest.tools/v3/assert"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestDeepCopy(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetVariables("public", "int", "", config.NewConfigReplacements("r", "sr", "s"))
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

	if diff := cmp.Diff(pipeline, pipelineCopy, cmpopts.IgnoreUnexported(Pipeline{}, Step{})); diff != "" {
		t.Errorf("got diffs after pipeline deep copy: %v", diff)
	}
}

func TestEnterPipelineDir(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetVariables("public", "int", "", config.NewConfigReplacements("r", "sr", "s"))
	if err != nil {
		t.Errorf("failed to get variables: %v", err)
	}
	pipeline, err := NewPipelineFromFile("../../testdata/pipeline.yaml", vars)
	if err != nil {
		t.Errorf("failed to read new pipeline: %v", err)
	}

	originalDir, _ := os.Getwd()

	pipelineDir, cleanup, err := pipeline.EnterPipelineDir()
	if err != nil {
		t.Errorf("failed to enter pipeline dir: %v", err)
	}
	defer cleanup()

	currentDir, _ := os.Getwd()
	pipelineAbsDir, _ := filepath.Abs(pipelineDir)
	assert.Equal(t, pipelineDir, pipelineAbsDir, "expected absolute pipeline dir to be announced")
	assert.Equal(t, currentDir, pipelineAbsDir, "expected to be in pipeline dir")

	cleanup()
	restoredDir, _ := os.Getwd()
	assert.Equal(t, restoredDir, originalDir, "expected to return to original dir")

}
