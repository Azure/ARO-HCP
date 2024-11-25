package utils

import (
	"testing"

	"gotest.tools/assert"
)

func TestGetOsVariable(t *testing.T) {
	t.Setenv("FOO", "BAR")
	envVars := GetOsVariable()
	assert.Equal(t, "1", envVars["RUNS_IN_TEMPLATIZE"])
	assert.Equal(t, "BAR", envVars["FOO"])
}

func TestMapToEnvVarArray(t *testing.T) {
	envVars := map[string]string{
		"FOO": "BAR",
	}
	envVarArray := MapToEnvVarArray(envVars)
	assert.DeepEqual(t, []string{"FOO=BAR"}, envVarArray)
}
