package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecuteTemplate(t *testing.T) {
	opts := DefaultGenerationOptions()
	ctx := context.Background()

	// No config, input and output files
	err := opts.ExecuteTemplate(ctx)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")

	opts.ConfigFile = "testdata/config.yaml"
	opts.Input = "testdata/helm.sh"
	opts.Output = "output"
	err = opts.ExecuteTemplate(ctx)
	assert.NoError(t, err)
}
