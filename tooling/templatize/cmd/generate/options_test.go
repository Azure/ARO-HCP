package generate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	options "github.com/Azure/ARO-HCP/tooling/templatize/cmd"
	"github.com/Azure/ARO-HCP/tooling/templatize/internal/testutil"
)

func TestRawOptions(t *testing.T) {
	tmpdir := t.TempDir()
	opts := &RawGenerationOptions{
		RawOptions: options.RawOptions{
			ConfigFile:  "../../testdata/config.yaml",
			Cloud:       "fairfax",
			DeployEnv:   "prod",
			Region:      "uksouth",
			RegionStamp: "1",
			CXStamp:     "cx",
		},
		Input:  "../../testdata/helm.sh",
		Output: tmpdir,
	}
	assert.NoError(t, generate(opts))
	testutil.CompareFileWithFixture(t, filepath.Join(tmpdir, "helm.sh"))
}
