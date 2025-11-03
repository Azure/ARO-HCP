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
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/clients"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/config"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/updater"
	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/yaml"
)

// RawUpdateOptions contains the raw command-line input
type RawUpdateOptions struct {
	ConfigPath        string
	DryRun            bool
	Components        string
	ExcludeComponents string
}

// ValidatedUpdateOptions contains validated configuration and inputs
type ValidatedUpdateOptions struct {
	*validatedUpdateOptions
}

type validatedUpdateOptions struct {
	*RawUpdateOptions
	Config *config.Config
}

// DefaultUpdateOptions returns a new RawUpdateOptions with defaults
func DefaultUpdateOptions() *RawUpdateOptions {
	return &RawUpdateOptions{}
}

// BindUpdateOptions binds command-line flags to the raw options
func BindUpdateOptions(opts *RawUpdateOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "Path to image-updater configuration file")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be updated without making changes")
	cmd.Flags().StringVar(&opts.Components, "components", "", "Update only specified components (comma-separated, e.g., 'maestro,arohcpfrontend'). If not specified, all components will be updated")
	cmd.Flags().StringVar(&opts.ExcludeComponents, "exclude-components", "", "Exclude specified components from update (comma-separated, e.g., 'arohcpfrontend,arohcpbackend'). Ignored if --components is specified")

	if err := cmd.MarkFlagRequired("config"); err != nil {
		return err
	}

	return nil
}

// Validate validates the raw options and returns validated options
func (o *RawUpdateOptions) Validate(ctx context.Context) (*ValidatedUpdateOptions, error) {
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// --components takes precedence over --exclude-components
	if o.Components != "" {
		components := strings.Split(o.Components, ",")
		for i := range components {
			components[i] = strings.TrimSpace(components[i])
		}
		cfg, err = cfg.FilterByComponents(components)
		if err != nil {
			return nil, fmt.Errorf("failed to filter config by components: %w", err)
		}
	} else if o.ExcludeComponents != "" {
		excludeComponents := strings.Split(o.ExcludeComponents, ",")
		for i := range excludeComponents {
			excludeComponents[i] = strings.TrimSpace(excludeComponents[i])
		}
		cfg, err = cfg.FilterExcludingComponents(excludeComponents)
		if err != nil {
			return nil, fmt.Errorf("failed to filter config excluding components: %w", err)
		}
	}

	return &ValidatedUpdateOptions{
		validatedUpdateOptions: &validatedUpdateOptions{
			RawUpdateOptions: o,
			Config:           cfg,
		},
	}, nil
}

// Complete creates all necessary clients and resources for execution and returns a ready-to-execute Updater
func (v *ValidatedUpdateOptions) Complete(ctx context.Context) (*updater.Updater, error) {
	registryClients := make(map[string]clients.RegistryClient)
	for _, imageConfig := range v.Config.Images {
		registry, _, err := imageConfig.Source.ParseImageReference()
		if err != nil {
			return nil, fmt.Errorf("failed to parse image reference: %w", err)
		}

		if _, exists := registryClients[registry]; !exists {
			client, err := clients.NewRegistryClient(registry)
			if err != nil {
				return nil, fmt.Errorf("failed to create registry client for %s: %w", registry, err)
			}
			registryClients[registry] = client
		}
	}

	// Initialize YAML editors for target files
	yamlEditors := make(map[string]*yaml.Editor)
	for _, imageConfig := range v.Config.Images {
		for _, target := range imageConfig.Targets {
			if _, exists := yamlEditors[target.FilePath]; !exists {
				editor, err := yaml.NewEditor(target.FilePath)
				if err != nil {
					return nil, fmt.Errorf("failed to create YAML editor for %s: %w", target.FilePath, err)
				}
				yamlEditors[target.FilePath] = editor
			}
		}
	}

	return updater.New(v.Config, v.DryRun, registryClients, yamlEditors), nil
}

// validateConfig ensures the configuration is complete and valid
func validateConfig(cfg *config.Config) error {
	if len(cfg.Images) == 0 {
		return fmt.Errorf("no images configured")
	}

	for name, img := range cfg.Images {
		if img.Source.Image == "" {
			return fmt.Errorf("image %s: source image is required", name)
		}

		if len(img.Targets) == 0 {
			return fmt.Errorf("image %s: at least one target is required", name)
		}

		for _, target := range img.Targets {
			if target.JsonPath == "" {
				return fmt.Errorf("image %s: target jsonPath is required", name)
			}
			if target.FilePath == "" {
				return fmt.Errorf("image %s: target filePath is required", name)
			}
		}
	}

	return nil
}
