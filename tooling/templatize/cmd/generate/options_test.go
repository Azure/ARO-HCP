package generate

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
)

func TestRawOptions(t *testing.T) {
	tmpdir := t.TempDir()
	opts := &RawGenerationOptions{
		RolloutOptions: &options.RawRolloutOptions{
			Region:      "uksouth",
			RegionShort: "abcde",
			Stamp:       "fghij",
			BaseOptions: &options.RawOptions{
				ConfigFile: "../../testdata/config.yaml",
				Cloud:      "public",
				DeployEnv:  "dev",
			},
		},
		Input:  "../../testdata/pipeline.yaml",
		Output: fmt.Sprintf("%s/pipeline.yaml", tmpdir),
	}
	assert.NoError(t, generate(opts))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "pipeline.yaml"))
}
