package ev2

import (
	"testing"

	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/config"
)

func TestPreprocessFileForEV2(t *testing.T) {
	configProvider := config.NewConfigProvider("../../testdata/config.yaml")
	content, err := PreprocessFileForEV2(configProvider, "public", "int", "../../testdata/pipeline.yaml")
	if err != nil {
		t.Fatalf("PreprocessFileForEV2 failed: %v", err)
	}
	testutil.CompareWithFixture(t, content, testutil.WithExtension(".yaml"))

}
