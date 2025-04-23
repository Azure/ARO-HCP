// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
