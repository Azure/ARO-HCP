package utils

import (
	"fmt"
	"os"
	"strings"
)

func GetOSEnvVarsAsMap() map[string]string {
	envVars := make(map[string]string)
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envVars[parts[0]] = parts[1]
		}
	}
	return envVars
}

func MapToEnvVarArray(envVars map[string]string) []string {
	envVarArray := make([]string, 0, len(envVars))
	for k, v := range envVars {
		envVarArray = append(envVarArray, fmt.Sprintf("%s=%s", k, v))
	}
	return envVarArray
}
