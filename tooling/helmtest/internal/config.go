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

package internal

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/config/types"
)

func loadConfig(configPath string) (types.Configuration, error) {
	rawCfg, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config, %v", err)
	}

	var cfgYaml types.Configuration
	err = yaml.Unmarshal(rawCfg, &cfgYaml)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling config, %v", err)
	}

	return cfgYaml, nil
}

func LoadConfigAndMerge(configPath string, configOverride map[string]any) (map[string]any, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("error loading config: %v", err)
	}
	return types.MergeConfiguration(cfg, configOverride), nil
}

func ReplaceImageDigest(yamlCfg map[string]any) map[string]any {
	for key, value := range yamlCfg {
		if _, ok := value.(map[string]any); ok {
			yamlCfg[key] = ReplaceImageDigest(value.(map[string]any))
		}
		if key == "digest" {
			yamlCfg[key] = "sha256:1234567890"
		}
	}
	return yamlCfg
}
