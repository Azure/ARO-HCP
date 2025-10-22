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

package options

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Azure/ARO-Tools/pkg/config"
	"github.com/Azure/ARO-Tools/pkg/config/types"
	"github.com/Azure/ARO-Tools/pkg/yamlwrap"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Cloud: "dev",
	}
}

func BindOptions(opts *RawOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigFile, "config-file", opts.ConfigFile, "config file path")
	cmd.Flags().StringVar(&opts.ConfigFileOverride, "config-file-override", opts.ConfigFileOverride, "config file override path")
	cmd.Flags().StringVar(&opts.Cloud, "cloud", opts.Cloud, "the cloud (public, fairfax, dev)")
	cmd.Flags().StringVar(&opts.DeployEnv, "deploy-env", opts.DeployEnv, "the deploy environment")
	cmd.Flags().StringVar(&opts.Ev2Cloud, "ev2-cloud", opts.Ev2Cloud, "the Ev2 cloud to use when resolving config, if different from --cloud")
	return nil
}

// RawOptions holds input values.
type RawOptions struct {
	ConfigFile         string
	ConfigFileOverride string
	Cloud              string
	DeployEnv          string
	Ev2Cloud           string
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	validClouds := sets.NewString("public", "fairfax", "dev")
	if !validClouds.Has(o.Cloud) {
		return nil, fmt.Errorf("invalid cloud %s, must be one of %v", o.Cloud, validClouds.List())
	}

	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

func (o *ValidatedOptions) Complete() (*Options, error) {
	var configProvider config.ConfigProvider
	var err error

	if o.ConfigFileOverride != "" {
		mergedConfigFile, err := os.CreateTemp("", "merged-config-*.yaml")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary file for merged configuration: %w", err)
		}
		mergedConfigFilePath := mergedConfigFile.Name()

		mergedConfig, err := types.MergeRawConfigurationFiles(filepath.Dir(mergedConfigFilePath), []string{o.ConfigFile, o.ConfigFileOverride})
		if err != nil {
			return nil, fmt.Errorf("failed to merge configuration files: %w", err)
		}
		if err := os.WriteFile(mergedConfigFilePath, mergedConfig, 0644); err != nil {
			return nil, fmt.Errorf("failed to write configuration to file %q: %w", mergedConfigFilePath, err)
		}

		configProvider, err = config.NewConfigProvider(mergedConfigFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load config provider from merged configuration %s: %w", mergedConfigFilePath, err)
		}
	} else {
		configProvider, err = config.NewConfigProvider(o.ConfigFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load service config: %v", err)
		}
	}

	return &Options{
		completedOptions: &completedOptions{
			ConfigProvider: configProvider,
			Ev2Cloud:       o.Ev2Cloud,
		},
	}, nil
}

func writeRawConfig(config types.Configuration, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("output file path cannot be empty")
	}

	rawData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal configuration: %w", err)
	}

	data, err := yamlwrap.UnwrapYAML(rawData)
	if err != nil {
		return fmt.Errorf("failed to unwrap configuration: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write configuration to file %q: %w", filePath, err)
	}

	return nil
}

// completedGenerationOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	ConfigProvider config.ConfigProvider
	Ev2Cloud       string
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}
