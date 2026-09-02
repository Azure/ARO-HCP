// Copyright 2026 Microsoft Corporation
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
	"github.com/Azure/ARO-Tools/config/ev2config"
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

	if opts.ConfigFileOverride != "" {
		schemaBaseDir := filepath.Dir(opts.ConfigFile)
		mergedConfigData, err := types.MergeRawConfigurationFiles(schemaBaseDir, []string{opts.ConfigFile, opts.ConfigFileOverride})
		if err != nil {
			return fmt.Errorf("failed to merge config files: %w", err)
		}
		provider, err = config.NewConfigProviderFromData(mergedConfigData, schemaBaseDir)
		if err != nil {
			return fmt.Errorf("failed to create config provider: %w", err)
		}
	} else {
		provider, err = config.NewConfigProvider(opts.ConfigFile)
		if err != nil {
			return fmt.Errorf("failed to create config provider: %w", err)
		}
	}

	ev2Cloud := opts.Cloud
	if ev2Cloud == "dev" {
		ev2Cloud = "public"
	}
	ev2Cfg, err := ev2config.ResolveConfig(ev2Cloud, opts.Region)
	if err != nil {
		return fmt.Errorf("failed to resolve ev2 config for cloud=%q region=%q: %w", ev2Cloud, opts.Region, err)
	}

	regionShort := ""
	if rs, ok := ev2Cfg["regionShortName"]; ok {
		if rsStr, ok := rs.(string); ok {
			regionShort = rsStr
		}
	}

	// 3. Supply replacements for templated values (e.g. {{ .ctx.region }}, {{ .ev2.geoShortId }})
	replacements := config.ConfigReplacements{
		CloudReplacement:       opts.Cloud,
		EnvironmentReplacement: opts.DeployEnv,
		RegionReplacement:      opts.Region,
		RegionShortReplacement: regionShort,
		StampReplacement:       "1",
		Ev2Config:              ev2Cfg,
	}

	// 4. Resolve
	resolver, err := provider.GetResolver(&replacements)
	if err != nil {
		return fmt.Errorf("failed to get config resolver: %w", err)
	}

	if resolver == nil {
		return fmt.Errorf("resolver is nil!")
	}

	// 5. Evaluate region specific overrides/values and expose globally
	ServiceConfig, err = resolver.GetRegionConfiguration(opts.Region)
	return err
}
