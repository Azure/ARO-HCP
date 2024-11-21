package ev2

import (
	"fmt"
	"os"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
)

func TestPrecompilePipelineForEV2(t *testing.T) {
	defer func() {
		_ = os.Remove("../../testdata/ev2-precompiled-pipeline.yaml")
		_ = os.Remove("../../testdata/ev2-precompiled-test.bicepparam")
	}()

	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	vars, err := configProvider.GetVariables("public", "int", "", newEv2ConfigReplacements())
	if err != nil {
		t.Errorf("failed to get variables: %v", err)
	}
	newPipelinePath, err := PrecompilePipelineForEV2("../../testdata/pipeline.yaml", vars)
	if err != nil {
		t.Errorf("failed to precompile pipeline: %v", err)
	}

	p, err := pipeline.NewPipelineFromFile(newPipelinePath, vars)
	if err != nil {
		t.Errorf("failed to read new pipeline: %v", err)
	}
	fmt.Println(p)
	expectedParamsPath := "ev2-precompiled-test.bicepparam"
	if p.ResourceGroups[0].Steps[1].Parameters != expectedParamsPath {
		t.Errorf("expected parameters path %v, but got %v", expectedParamsPath, p.ResourceGroups[0].Steps[1].Parameters)
	}
	// TODO improve test, check against fixture
}
