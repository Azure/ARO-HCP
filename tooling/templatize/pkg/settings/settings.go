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

package settings

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

type Settings struct {
	Environments []Environment `json:"environments"`
	// Subscriptions holds a mapping of subscription key to the subscription ID.
	Subscriptions map[string]string `json:"subscriptions"`
}

type Environment struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Defaults    Parameters `json:"defaults"`
}

type Parameters struct {
	// Cloud determines the cloud entry under which the environment sits in the service configuration.
	Cloud string `json:"cloud"`
	// CloudOverride is the cloud to use for Ev2 data, if different from the name of the cloud itself.
	Ev2Cloud string `json:"ev2Cloud"`
	// Region is the region for which this environment is rendered.
	Region string `json:"region"`
	// CxStamp is the stamp for which this environment is rendered.
	CxStamp string `json:"cxStamp"`
	// RegionShortOverride is a bash parameter expression that expands to a replacement for the short region variable from the EV2 central config.
	RegionShortOverride string `json:"regionShortOverride"`
	// RegionShortSuffix is a bash parameter expression that expands to a suffix for the short region variable.
	RegionShortSuffix string `json:"regionShortSuffix"`
}

func Load(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var out Settings
	return &out, yaml.Unmarshal(raw, &out)
}

type EnvironmentParameters struct {
	Cloud               string
	Ev2Cloud            string
	Environment         string
	Region              string
	Stamp               string
	RegionShortOverride string
	RegionShortSuffix   string
}

func (s *Settings) Resolve(cloud, environment string) (EnvironmentParameters, error) {
	var env *Environment
	for _, e := range s.Environments {
		if e.Defaults.Cloud == cloud && e.Name == environment {
			env = &e
		}
	}
	if env == nil {
		return EnvironmentParameters{}, fmt.Errorf("cloud %s environment %s not found", cloud, environment)
	}

	return Resolve(*env)
}

func Resolve(environment Environment) (EnvironmentParameters, error) {
	out := EnvironmentParameters{
		Cloud:       environment.Defaults.Cloud,
		Ev2Cloud:    environment.Defaults.Ev2Cloud,
		Environment: environment.Name,
		Region:      environment.Defaults.Region,
		Stamp:       environment.Defaults.CxStamp,
	}
	if environment.Defaults.RegionShortSuffix != "" {
		expanded, err := expandBashSubstring(environment.Defaults.RegionShortSuffix)
		if err != nil {
			return EnvironmentParameters{}, fmt.Errorf("expanding region short suffix %q: %w", environment.Defaults.RegionShortSuffix, err)
		}
		out.RegionShortSuffix = expanded
	}
	if environment.Defaults.RegionShortOverride != "" {
		expanded, err := expandBashSubstring(environment.Defaults.RegionShortOverride)
		if err != nil {
			return EnvironmentParameters{}, fmt.Errorf("expanding region short override %q: %w", environment.Defaults.RegionShortOverride, err)
		}
		out.RegionShortOverride = expanded
	}
	return out, nil
}

// expandBashSubstring expands a limited subset of bash-style parameter
// expansions. It supports plain variable expansion ($VAR, ${VAR}) and
// substring extraction (${VAR:offset}, ${VAR:offset:length}).
// Note: negative offsets require a space after the colon (${VAR: -3});
// ${VAR:-3} is bash default-value syntax and is not supported here.
func expandBashSubstring(s string) (string, error) {
	var expandErr error
	result := os.Expand(s, func(key string) string {
		if expandErr != nil {
			return ""
		}

		colonIdx := strings.IndexByte(key, ':')
		if colonIdx == -1 {
			return os.Getenv(key)
		}

		varName := key[:colonIdx]
		substringExpr := key[colonIdx+1:]
		value := os.Getenv(varName)
		if value == "" {
			return ""
		}

		expanded, err := applySubstring(value, substringExpr)
		if err != nil {
			expandErr = fmt.Errorf("variable %q: %w", varName, err)
			return ""
		}
		return expanded
	})
	if expandErr != nil {
		return "", expandErr
	}
	return result, nil
}

func applySubstring(value, expr string) (string, error) {
	parts := strings.SplitN(expr, ":", 2)

	offset, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return "", fmt.Errorf("invalid offset %q: %w", parts[0], err)
	}

	if offset < 0 {
		offset = len(value) + offset
		if offset < 0 {
			offset = 0
		}
	}
	if offset > len(value) {
		return "", nil
	}

	if len(parts) == 1 {
		return value[offset:], nil
	}

	length, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", fmt.Errorf("invalid length %q: %w", parts[1], err)
	}

	end := offset + length
	if end > len(value) {
		end = len(value)
	}
	if end < offset {
		return "", nil
	}
	return value[offset:end], nil
}
