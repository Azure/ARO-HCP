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

package config

import (
	"fmt"
	"path/filepath"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/types"
)

var ServiceConfig types.Configuration

type ConfigOptions struct {
	ConfigFile         string
	ConfigFileOverride string
	Cloud              string
	DeployEnv          string
	Region             string
}

// LoadConfig creates a provider, resolves values, and populates ServiceConfig
func LoadConfig(opts ConfigOptions) error {
	var provider config.ConfigProvider
	var err error

	// 1. Merge files if override is provided, or just read the base file
	if opts.ConfigFileOverride != "" {
		schemaBaseDir := filepath.Dir(opts.ConfigFile)
		mergedConfigData, err := types.MergeRawConfigurationFiles(schemaBaseDir, []string{opts.ConfigFile, opts.ConfigFileOverride})
		if err != nil {
			return fmt.Errorf("failed to merge config files: %w", err)
		}
		provider, err = config.NewConfigProviderFromData(mergedConfigData, schemaBaseDir)
	} else {
		provider, err = config.NewConfigProvider(opts.ConfigFile)
	}
	if err != nil {
		return fmt.Errorf("failed to create config provider: %w", err)
	}

	// 2. Supply replacements for templated values (e.g. {{ .ctx.region }})
	replacements := config.ConfigReplacements{
		CloudReplacement:       opts.Cloud,
		EnvironmentReplacement: opts.DeployEnv,
		RegionReplacement:      opts.Region,
		// Add StampReplacement or RegionShortReplacement if your tests require stamp-scoped values
	}

	// 3. Resolve
	resolver, err := provider.GetResolver(&replacements)
	if err != nil {
		return fmt.Errorf("failed to get config resolver: %w", err)
	}

	// 4. Evaluate region specific overrides/values and expose globally
	ServiceConfig, err = resolver.GetRegionConfiguration(opts.Region)
	return err
}
