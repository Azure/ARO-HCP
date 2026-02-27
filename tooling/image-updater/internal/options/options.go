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
	ForceUpdate       bool
	Components        string
	Groups            string
	ExcludeComponents string
	OutputFile        string
	OutputFormat      string
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
	return &RawUpdateOptions{
		OutputFormat: "table",
	}
}

// BindUpdateOptions binds command-line flags to the raw options
func BindUpdateOptions(opts *RawUpdateOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.ConfigPath, "config", "", "Path to image-updater configuration file")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be updated without making changes")
	cmd.Flags().BoolVar(&opts.ForceUpdate, "force", false, "Force update even if digests match (useful for regenerating version tag comments)")
	cmd.Flags().StringVar(&opts.Components, "components", "", "Update only specified components (comma-separated, e.g., 'maestro,arohcpfrontend'). If not specified, all components will be updated")
	cmd.Flags().StringVar(&opts.Groups, "groups", "", "Update only components in specified groups (comma-separated, e.g., 'hypershift-stack,velero'). Can be combined with --components (union)")
	cmd.Flags().StringVar(&opts.ExcludeComponents, "exclude-components", "", "Exclude specified components from update (comma-separated, e.g., 'arohcpfrontend,arohcpbackend'). Applied after --components/--groups filtering")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", "", "Write update results to specified file instead of stdout")
	cmd.Flags().StringVar(&opts.OutputFormat, "output-format", "table", "Output format: table, markdown, or json (default: table)")

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

	// Set default output format if not specified
	if o.OutputFormat == "" {
		o.OutputFormat = "table"
	}

	// Validate output format
	validFormats := []string{"table", "markdown", "json"}
	isValidFormat := false
	for _, format := range validFormats {
		if o.OutputFormat == format {
			isValidFormat = true
			break
		}
	}
	if !isValidFormat {
		return nil, fmt.Errorf("invalid output format '%s': must be one of: %s", o.OutputFormat, strings.Join(validFormats, ", "))
	}

	// Build inclusion set from --components and --groups (union), then apply --exclude-components
	if o.Components != "" || o.Groups != "" {
		included := make(map[string]config.ImageConfig)

		// Add explicitly listed components
		if o.Components != "" {
			components := strings.Split(o.Components, ",")
			for i := range components {
				components[i] = strings.TrimSpace(components[i])
			}
			componentCfg, err := cfg.FilterByComponents(components)
			if err != nil {
				return nil, fmt.Errorf("failed to filter config by components: %w", err)
			}
			for name, img := range componentCfg.Images {
				included[name] = img
			}
		}

		// Add components from specified groups
		if o.Groups != "" {
			groups := strings.Split(o.Groups, ",")
			for i := range groups {
				groups[i] = strings.TrimSpace(groups[i])
			}
			groupCfg, err := cfg.FilterByGroups(groups)
			if err != nil {
				return nil, fmt.Errorf("failed to filter config by groups: %w", err)
			}
			for name, img := range groupCfg.Images {
				included[name] = img
			}
		}

		cfg = &config.Config{Images: included}

		// Apply exclusions on the filtered set
		if o.ExcludeComponents != "" {
			excludeComponents := strings.Split(o.ExcludeComponents, ",")
			for i := range excludeComponents {
				excludeComponents[i] = strings.TrimSpace(excludeComponents[i])
			}
			cfg, err = cfg.FilterExcludingComponents(excludeComponents)
			if err != nil {
				return nil, fmt.Errorf("failed to filter config excluding components: %w", err)
			}
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
	// Collect unique Key Vault configurations from all images
	// Use a map to deduplicate (same vault+secret combination)
	kvConfigs := make(map[string]clients.KeyVaultConfig)
	for _, imageConfig := range v.Config.Images {
		if imageConfig.Source.KeyVault != nil &&
			imageConfig.Source.KeyVault.URL != "" &&
			imageConfig.Source.KeyVault.SecretName != "" {
			key := imageConfig.Source.KeyVault.URL + "|" + imageConfig.Source.KeyVault.SecretName
			kvConfigs[key] = clients.KeyVaultConfig{
				VaultURL:   imageConfig.Source.KeyVault.URL,
				SecretName: imageConfig.Source.KeyVault.SecretName,
			}
		}
	}

	// Fetch all unique pull secrets from Key Vault
	for _, kvConfig := range kvConfigs {
		if err := clients.FetchAndMergeKeyVaultPullSecret(ctx, kvConfig); err != nil {
			return nil, fmt.Errorf("failed to fetch pull secret %s from Key Vault %s: %w",
				kvConfig.SecretName, kvConfig.VaultURL, err)
		}
	}

	// Create registry clients - one client per registry+auth combination
	// Key format: "registry:useAuth" (e.g., "quay.io:true", "quay.io:false")
	registryClients := make(map[string]clients.RegistryClient)
	for _, imageConfig := range v.Config.Images {
		if imageConfig.Source.GitHubLatestRelease != "" {
			continue
		}
		registry, _, err := imageConfig.Source.ParseImageReference()
		if err != nil {
			return nil, fmt.Errorf("failed to parse image reference: %w", err)
		}

		// Determine useAuth for this specific image - default to false if not specified
		useAuth := false
		if imageConfig.Source.UseAuth != nil {
			useAuth = *imageConfig.Source.UseAuth
		}

		// Create a unique key for this registry+auth combination
		clientKey := fmt.Sprintf("%s:%t", registry, useAuth)
		if _, exists := registryClients[clientKey]; !exists {
			client, err := clients.NewRegistryClient(registry, useAuth)
			if err != nil {
				return nil, fmt.Errorf("failed to create registry client for %s (useAuth=%t): %w", registry, useAuth, err)
			}
			registryClients[clientKey] = client
		}
	}

	// Initialize YAML editors for target files
	yamlEditors := make(map[string]yaml.EditorInterface)
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

	return updater.New(v.Config, v.DryRun, v.ForceUpdate, registryClients, yamlEditors, v.OutputFile, v.OutputFormat), nil
}

// validateConfig ensures the configuration is complete and valid
func validateConfig(cfg *config.Config) error {
	if len(cfg.Images) == 0 {
		return fmt.Errorf("no images configured")
	}

	for name, img := range cfg.Images {
		if img.Source.Image == "" && img.Source.GitHubLatestRelease == "" {
			return fmt.Errorf("image %s: source image or githubLatestRelease is required", name)
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
