package testutil

import (
	"os"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/templatize/cmd/pipeline/run"
	"github.com/Azure/ARO-HCP/tooling/templatize/pkg/pipeline"
	"gotest.tools/v3/assert"
)

func TestE2EMake(t *testing.T) {
	if !shouldRunE2E() {
		t.Skip("Skipping end-to-end tests")
	}

	tmpDir := t.TempDir()

	e2eImpl := newE2E(tmpDir)

	e2eImpl.SetPipeline(pipeline.Step{
		Name:    "test",
		Action:  "Shell",
		Command: []string{"make", "test"},
		Env: []pipeline.EnvVar{
			{
				Name:      "TEST_ENV",
				ConfigRef: "test_env",
			},
		},
	}, "")

	e2eImpl.makefile = `
test:
	echo ${TEST_ENV} > env.txt
`

	e2eImpl.Persist()

	cmd, err := run.NewCommand()

	assert.NilError(t, err)

	os.Args = []string{"test",
		"--cloud", "public",
		"--pipeline-file", tmpDir + "/pipeline.yaml",
		"--step", "test",
		"--config-file", tmpDir + "/config.yaml",
		"--deploy-env", "dev",
	}

	err = cmd.Execute()

	assert.NilError(t, err)

	fno, err := os.Stat(tmpDir + "/env.txt")

	assert.NilError(t, err)

	assert.Equal(t, fno.Size(), int64(9))
}
