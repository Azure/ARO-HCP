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
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// DefaultVersionLabel is the default container label used for version extraction when using 'tag' field
	DefaultVersionLabel = "org.opencontainers.image.revision"
)

// Config represents the image updater configuration
type Config struct {
	Images map[string]ImageConfig `yaml:"images"`
}

// ImageConfig defines a single image's source and target configuration
type ImageConfig struct {
	Source  Source   `yaml:"source"`
	Targets []Target `yaml:"targets"`
}

// Source defines where to fetch the latest image digest from
type Source struct {
	Image        string          `yaml:"image"`
	Tag          string          `yaml:"tag,omitempty"`          // Exact tag to use (mutually exclusive with TagPattern)
	TagPattern   string          `yaml:"tagPattern,omitempty"`   // Regex pattern to filter tags (mutually exclusive with Tag)
	VersionLabel string          `yaml:"versionLabel,omitempty"` // Container label to fetch for human-friendly version (defaults to "org.opencontainers.image.revision" when tag is used, empty when tagPattern is used)
	Architecture string          `yaml:"architecture,omitempty"` // Specific architecture to use (e.g., "amd64", "arm64"). Mutually exclusive with MultiArch.
	MultiArch    bool            `yaml:"multiArch,omitempty"`    // If true, fetch the multi-arch manifest list digest instead of a specific architecture
	UseAuth      *bool           `yaml:"useAuth,omitempty"`      // true = use auth, nil/false = anonymous (default)
	KeyVault     *KeyVaultConfig `yaml:"keyVault,omitempty"`     // Optional: Azure Key Vault config for fetching pull secrets
}

// KeyVaultConfig holds Azure Key Vault configuration for fetching pull secrets
type KeyVaultConfig struct {
	URL        string `yaml:"url"`        // Azure Key Vault URL (e.g., https://vault.vault.azure.net/)
	SecretName string `yaml:"secretName"` // Name of the pull secret
}

// Target defines where to update the image digest
type Target struct {
	JsonPath  string `yaml:"jsonPath"`
	FilePath  string `yaml:"filePath"`
	Env       string `yaml:"env,omitempty"` // Environment (dev, int, stg, prod)
	ValueType string `yaml:"valueType,omitempty"`
}

// Validate checks if the Source configuration is valid
func (s *Source) Validate() error {
	// Tag and TagPattern are mutually exclusive
	if s.Tag != "" && s.TagPattern != "" {
		return fmt.Errorf("tag and tagPattern are mutually exclusive, only one can be specified")
	}

	// Architecture and MultiArch are mutually exclusive
	if s.Architecture != "" && s.MultiArch {
		return fmt.Errorf("architecture and multiArch are mutually exclusive")
	}

	return nil
}

// GetEffectiveTagPattern returns the effective tag pattern to use
// If Tag is specified, it returns an exact match pattern
// If TagPattern is specified, it returns TagPattern
// Otherwise, it returns an empty string (match all tags)
func (s *Source) GetEffectiveTagPattern() string {
	if s.Tag != "" {
		// Create an exact match pattern for the specific tag
		return "^" + regexp.QuoteMeta(s.Tag) + "$"
	}
	return s.TagPattern
}

// GetEffectiveVersionLabel returns the effective version label to use
// If VersionLabel is explicitly set, it returns that value
// If Tag is set (not TagPattern), it defaults to DefaultVersionLabel
// Otherwise, it returns an empty string
func (s *Source) GetEffectiveVersionLabel() string {
	if s.VersionLabel != "" {
		return s.VersionLabel
	}
	// Default to DefaultVersionLabel when using a specific tag
	if s.Tag != "" {
		return DefaultVersionLabel
	}
	return ""
}

// ParseImageReference splits an image reference into registry and repository parts
func (s *Source) ParseImageReference() (registry, repository string, err error) {
	if s.Image == "" {
		return "", "", fmt.Errorf("image reference is empty")
	}

	// Split on the first '/' to separate registry from repository
	parts := strings.SplitN(s.Image, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid image reference %q: must be in format registry/repository", s.Image)
	}

	registry = parts[0]
	repository = parts[1]

	if registry == "" {
		return "", "", fmt.Errorf("invalid image reference %q: registry part is empty", s.Image)
	}
	if repository == "" {
		return "", "", fmt.Errorf("invalid image reference %q: repository part is empty", s.Image)
	}

	return registry, repository, nil
}

// Load reads and parses the configuration file
func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	// Validate each image source configuration
	for name, imageConfig := range cfg.Images {
		if err := imageConfig.Source.Validate(); err != nil {
			return nil, fmt.Errorf("invalid configuration for image %q: %w", name, err)
		}
	}

	return &cfg, nil
}

// FilterByComponent returns a new Config containing only the specified component
func (c *Config) FilterByComponent(componentName string) (*Config, error) {
	if componentName == "" {
		return c, nil
	}

	imageConfig, exists := c.Images[componentName]
	if !exists {
		return nil, fmt.Errorf("component %q not found in configuration", componentName)
	}

	return &Config{
		Images: map[string]ImageConfig{
			componentName: imageConfig,
		},
	}, nil
}

// FilterByComponents returns a new Config containing only the specified components
func (c *Config) FilterByComponents(componentNames []string) (*Config, error) {
	if len(componentNames) == 0 {
		return c, nil
	}

	filteredImages := make(map[string]ImageConfig)
	for _, componentName := range componentNames {
		imageConfig, exists := c.Images[componentName]
		if !exists {
			return nil, fmt.Errorf("component %q not found in configuration", componentName)
		}
		filteredImages[componentName] = imageConfig
	}

	return &Config{
		Images: filteredImages,
	}, nil
}

// FilterExcludingComponents returns a new Config excluding the specified components
func (c *Config) FilterExcludingComponents(componentNames []string) (*Config, error) {
	if len(componentNames) == 0 {
		return c, nil
	}

	// Validate all excluded components exist (catch typos early)
	for _, componentName := range componentNames {
		if _, exists := c.Images[componentName]; !exists {
			return nil, fmt.Errorf("excluded component %q not found in configuration", componentName)
		}
	}

	// Build set of excluded components for O(1) lookup
	excluded := sets.NewString(componentNames...)

	// Filter images, excluding those in the exclusion list
	filteredImages := make(map[string]ImageConfig)
	for name, imageConfig := range c.Images {
		if !excluded.Has(name) {
			filteredImages[name] = imageConfig
		}
	}

	return &Config{
		Images: filteredImages,
	}, nil
}

// FilterByEnvironments returns a new Config with targets filtered by environment
func (c *Config) FilterByEnvironments(environments []string) (*Config, error) {
	if len(environments) == 0 {
		return c, nil
	}

	// Build map for O(1) lookup
	envMap := make(map[string]bool, len(environments))
	for _, env := range environments {
		envMap[env] = true
	}

	filteredImages := make(map[string]ImageConfig)
	for name, imageConfig := range c.Images {
		filteredTargets := make([]Target, 0)
		for _, target := range imageConfig.Targets {
			// Only include targets that match one of the requested environments
			if envMap[target.Env] {
				filteredTargets = append(filteredTargets, target)
			}
		}

		// Only include the image if it has at least one matching target
		if len(filteredTargets) > 0 {
			filteredImageConfig := imageConfig
			filteredImageConfig.Targets = filteredTargets
			filteredImages[name] = filteredImageConfig
		}
	}

	// Return error if no targets match the specified environments
	if len(filteredImages) == 0 {
		return nil, fmt.Errorf("no targets found for environments: %v", environments)
	}

	return &Config{
		Images: filteredImages,
	}, nil
}
