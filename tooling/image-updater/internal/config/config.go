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
	"sort"
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
	Group   string   `yaml:"group"`
	Source  Source   `yaml:"source"`
	Targets []Target `yaml:"targets"`
}

// Source defines where to fetch the latest image digest (or version string) from
type Source struct {
	Image               string          `yaml:"image"`
	GitHubLatestRelease string          `yaml:"githubLatestRelease,omitempty"` // If set, fetch latest release tag from GitHub (e.g. "istio/istio"); used for version-only targets, ignores Image for fetch
	Tag                 string          `yaml:"tag,omitempty"`                 // Exact tag to use (mutually exclusive with TagPattern)
	TagPattern          string          `yaml:"tagPattern,omitempty"`          // Regex pattern to filter tags (mutually exclusive with Tag)
	VersionLabel        string          `yaml:"versionLabel,omitempty"`        // Container label to fetch for human-friendly version (defaults to "org.opencontainers.image.revision" when tag is used, empty when tagPattern is used)
	Architecture        string          `yaml:"architecture,omitempty"`        // Specific architecture to use (e.g., "amd64", "arm64"). Mutually exclusive with MultiArch.
	MultiArch           bool            `yaml:"multiArch,omitempty"`           // If true, fetch the multi-arch manifest list digest instead of a specific architecture
	UseAuth             *bool           `yaml:"useAuth,omitempty"`             // true = use auth, nil/false = anonymous (default)
	KeyVault            *KeyVaultConfig `yaml:"keyVault,omitempty"`            // Optional: Azure Key Vault config for fetching pull secrets
}

// KeyVaultConfig holds Azure Key Vault configuration for fetching pull secrets
type KeyVaultConfig struct {
	URL        string `yaml:"url"`        // Azure Key Vault URL (e.g., https://vault.vault.azure.net/)
	SecretName string `yaml:"secretName"` // Name of the pull secret
}

// Target defines where to update the image digest
type Target struct {
	JsonPath string `yaml:"jsonPath"`
	FilePath string `yaml:"filePath"`
}

// Validate checks if the Source configuration is valid
func (s *Source) Validate() error {
	if s.GitHubLatestRelease != "" {
		// GitHub latest release: require "owner/repo" format
		if !strings.Contains(s.GitHubLatestRelease, "/") || strings.Count(s.GitHubLatestRelease, "/") != 1 {
			return fmt.Errorf("githubLatestRelease must be in format owner/repo (e.g. istio/istio)")
		}
		// Registry-specific fields are not allowed with githubLatestRelease
		if s.Image != "" {
			return fmt.Errorf("image must not be set when githubLatestRelease is used")
		}
		if s.Tag != "" || s.TagPattern != "" {
			return fmt.Errorf("tag/tagPattern must not be set when githubLatestRelease is used")
		}
		if s.Architecture != "" || s.MultiArch {
			return fmt.Errorf("architecture/multiArch must not be set when githubLatestRelease is used")
		}
		return nil
	}

	if s.Image == "" {
		return fmt.Errorf("image is required when githubLatestRelease is not set")
	}
	if s.Tag != "" && s.TagPattern != "" {
		return fmt.Errorf("tag and tagPattern are mutually exclusive, only one can be specified")
	}
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

// SourceDescription returns a short, opaque description of the source for logging (image ref or GitHub repo).
func (s *Source) SourceDescription() string {
	if s.GitHubLatestRelease != "" {
		return "github.com/" + s.GitHubLatestRelease
	}
	return s.Image
}

// TagInfo returns the tag or tag pattern for logging (exact tag or tagPattern).
func (s *Source) TagInfo() string {
	if s.Tag != "" {
		return s.Tag
	}
	return s.TagPattern
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

	// Validate each image configuration
	for name, imageConfig := range cfg.Images {
		if imageConfig.Group == "" {
			return nil, fmt.Errorf("image %q: group is required", name)
		}
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

// FilterByGroups returns a new Config containing only images matching the given groups
func (c *Config) FilterByGroups(groupNames []string) (*Config, error) {
	if len(groupNames) == 0 {
		return c, nil
	}

	requestedGroups := sets.NewString(groupNames...)

	filteredImages := make(map[string]ImageConfig)
	matchedGroups := sets.NewString()
	for name, imageConfig := range c.Images {
		if requestedGroups.Has(imageConfig.Group) {
			filteredImages[name] = imageConfig
			matchedGroups.Insert(imageConfig.Group)
		}
	}

	// Check that all requested groups matched at least one image
	unmatchedGroups := requestedGroups.Difference(matchedGroups)
	if unmatchedGroups.Len() > 0 {
		return nil, fmt.Errorf("group %q not found in configuration", unmatchedGroups.List()[0])
	}

	return &Config{
		Images: filteredImages,
	}, nil
}

// Groups returns a sorted list of distinct group names from all images
func (c *Config) Groups() []string {
	groupSet := sets.NewString()
	for _, imageConfig := range c.Images {
		if imageConfig.Group != "" {
			groupSet.Insert(imageConfig.Group)
		}
	}
	groups := groupSet.List()
	sort.Strings(groups)
	return groups
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
