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
	"context"
	"fmt"
	"os"
	"os/exec"
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
	CxStamp int `json:"cxStamp"`
	// RegionShortOverride is a shell-ism that, when run through `echo`, outputs a replacement for the short region variable from the EV2 central config.
	RegionShortOverride string `json:"regionShortOverride"`
	// RegionShortSuffix is a shell-ism that, when run through `echo`, outputs a suffix for the short region variable.
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
	Stamp               int
	RegionShortOverride string
	RegionShortSuffix   string
}

func (s *Settings) Resolve(ctx context.Context, cloud, environment string) (EnvironmentParameters, error) {
	var env *Environment
	for _, e := range s.Environments {
		if e.Defaults.Cloud == cloud && e.Name == environment {
			env = &e
		}
	}
	if env == nil {
		return EnvironmentParameters{}, fmt.Errorf("cloud %s environment %s not found", cloud, environment)
	}

	return Resolve(ctx, *env)
}

func Resolve(ctx context.Context, environment Environment) (EnvironmentParameters, error) {
	out := EnvironmentParameters{
		Cloud:       environment.Defaults.Cloud,
		Ev2Cloud:    environment.Defaults.Ev2Cloud,
		Environment: environment.Name,
		Region:      environment.Defaults.Region,
		Stamp:       environment.Defaults.CxStamp,
	}
	if environment.Defaults.RegionShortSuffix != "" {
		evaluator := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("echo %s", environment.Defaults.RegionShortSuffix))
		evaluator.Env = os.Environ()
		evaluated, err := evaluator.CombinedOutput()
		if err != nil {
			return EnvironmentParameters{}, fmt.Errorf("failed to evaluate region short suffix: %w; output: %s", err, string(evaluated))
		}
		out.RegionShortSuffix = strings.TrimSpace(string(evaluated))
	}
	if environment.Defaults.RegionShortOverride != "" {
		evaluator := exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("echo %s", environment.Defaults.RegionShortOverride))
		evaluator.Env = os.Environ()
		evaluated, err := evaluator.CombinedOutput()
		if err != nil {
			return EnvironmentParameters{}, fmt.Errorf("failed to evaluate region short override: %w; output: %s", err, string(evaluated))
		}
		out.RegionShortOverride = strings.TrimSpace(string(evaluated))
	}
	return out, nil
}
