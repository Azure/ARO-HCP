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
	"os"
	"path/filepath"
	"sync"

	"github.com/Azure/ARO-Tools/config"
	"github.com/Azure/ARO-Tools/config/ev2config"
	"github.com/Azure/ARO-Tools/config/types"
)

var ServiceConfig types.Configuration

var (
	configOnce sync.Once
	configErr  error
)

// GetServiceConfig returns the service configuration, loading it from
// environment variables on first call. Subsequent calls return the
// cached result. Required env vars: ARO_HCP_CONFIG_FILE, ARO_HCP_CLOUD,
// DEPLOY_ENV, REGION. Missing vars cause an error.
func GetServiceConfig() (types.Configuration, error) {
	configOnce.Do(func() {
		required := map[string]string{
			"ARO_HCP_CONFIG_FILE": os.Getenv("ARO_HCP_CONFIG_FILE"),
			"ARO_HCP_CLOUD":      os.Getenv("ARO_HCP_CLOUD"),
			"DEPLOY_ENV":         os.Getenv("DEPLOY_ENV"),
			"REGION":             os.Getenv("REGION"),
		}
		var missing []string
		for k, v := range required {
			if v == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			configErr = fmt.Errorf("required environment variables not set: %v", missing)
			return
		}
		opts := ConfigOptions{
			ConfigFile:         required["ARO_HCP_CONFIG_FILE"],
			ConfigFileOverride: os.Getenv("ARO_HCP_CONFIG_FILE_OVERRIDE"),
			Cloud:              required["ARO_HCP_CLOUD"],
			DeployEnv:          required["DEPLOY_ENV"],
			Region:             required["REGION"],
		}
		configErr = LoadConfig(opts)
	})
	return ServiceConfig, configErr
}

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
