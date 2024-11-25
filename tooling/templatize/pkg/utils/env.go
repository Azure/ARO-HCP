package utils

import (
	"fmt"
	"maps"
	"os"
	"strings"
)

// GetOsVariable looks up OS environment variables and returns them as a map.
// It also sets a special environment variable RUNS_IN_TEMPLATIZE to 1.
func GetOsVariable() map[string]string {
	envVars := make(map[string]string)
	envVars["RUNS_IN_TEMPLATIZE"] = "1"

	osVars := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envVars[parts[0]] = parts[1]
		}
	}
	maps.Copy(envVars, osVars)

	return envVars
}

// MapToEnvVarArray converts a map of environment variables to an array of strings.
func MapToEnvVarArray(envVars map[string]string) []string {
	envVarArray := make([]string, 0, len(envVars))
	for k, v := range envVars {
		envVarArray = append(envVarArray, fmt.Sprintf("%s=%s", k, v))
	}
	return envVarArray
}
