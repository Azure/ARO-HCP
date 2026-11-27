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
	"bytes"
	"fmt"
	"io"
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
	Bundles map[string][]string    `yaml:"bundles,omitempty"`
	Images  map[string]ImageConfig `yaml:"images"`
}

// ImageConfig defines a single image's source and target configuration
type ImageConfig struct {
	Group   string   `yaml:"group"`
	Source  Source   `yaml:"source"`
	Targets []Target `yaml:"targets"`
}

// Source defines where to fetch the latest image digest (or version string) from
type Source struct {
	Image              string              `yaml:"image"`
	Tag                string              `yaml:"tag,omitempty"`                // Exact tag to use (mutually exclusive with TagPattern)
	TagPattern         string              `yaml:"tagPattern,omitempty"`         // Regex pattern to filter tags (mutually exclusive with Tag)
	VersionLabel       string              `yaml:"versionLabel,omitempty"`       // Container label to fetch for human-friendly version (defaults to "org.opencontainers.image.revision" when tag is used, empty when tagPattern is used)
	Architecture       string              `yaml:"architecture,omitempty"`       // Specific architecture to use (e.g., "amd64", "arm64"). Mutually exclusive with MultiArch.
	MultiArch          bool                `yaml:"multiArch,omitempty"`          // If true, fetch the multi-arch manifest list digest instead of a specific architecture
	UseAuth            *bool               `yaml:"useAuth,omitempty"`            // true = use auth, nil/false = anonymous (default)
	LegacyKeyVault     *yaml.Node          `yaml:"keyVault,omitempty"`           // Rejected during validation; retained only to report a clear migration error
	RepoVersionUpgrade *RepoVersionUpgrade `yaml:"repoVersionUpgrade,omitempty"` // Optional: enables repository version upgrade checks for this component
}

// RepoVersionUpgrade configures repository version upgrade detection for a component.
// When set, the update --repositories mode will check Quay for next-version repos.
type RepoVersionUpgrade struct {
	RepoPrefix string `yaml:"repoPrefix"` // The repo name prefix before the version suffix (e.g. "acm-operator-bundle-acm-")
}

// Target defines where to update the image digest
type Target struct {
	JsonPath string `yaml:"jsonPath"`
	FilePath string `yaml:"filePath"`
}

// Validate checks if the Source configuration is valid
func (s *Source) Validate() error {
	if s.LegacyKeyVault != nil {
		return fmt.Errorf("keyVault is no longer supported; use Azure credentials for private ACRs or Docker config for other private registries")
	}
	if s.Image == "" {
		return fmt.Errorf("image is required")
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

// SourceDescription returns a short, opaque description of the source for logging.
func (s *Source) SourceDescription() string {
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

	if err := cfg.validateImages(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// LoadStrict reads and parses the configuration file, rejecting unknown fields
// so a mistyped or misindented entry cannot be ignored silently.
func LoadStrict(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
		}
		return nil, fmt.Errorf("failed to parse config file %s: multiple YAML documents are not supported", configPath)
	}

	if err := cfg.validateImages(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validateImages checks each image entry.
func (c *Config) validateImages() error {
	for name, imageConfig := range c.Images {
		if imageConfig.Group == "" {
			return fmt.Errorf("image %q: group is required", name)
		}
		if err := validateName("group", imageConfig.Group); err != nil {
			return fmt.Errorf("image %q: %w", name, err)
		}
		if err := imageConfig.Source.Validate(); err != nil {
			return fmt.Errorf("invalid configuration for image %q: %w", name, err)
		}
	}

	return nil
}

// validateName rejects names that update flags cannot select, because
// --groups trims the argument and splits it on commas.
func validateName(kind, name string) error {
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("%s %q has surrounding whitespace", kind, name)
	}
	if strings.Contains(name, ",") {
		return fmt.Errorf("%s %q contains a comma", kind, name)
	}
	return nil
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

// ValidateBundles checks that the bundles partition every component group, so a
// group belongs to exactly one bundle and every bundle names known groups.
func (c *Config) ValidateBundles() error {
	if len(c.Bundles) == 0 {
		return fmt.Errorf("no bundles configured")
	}

	bundleNames := make([]string, 0, len(c.Bundles))
	for name := range c.Bundles {
		bundleNames = append(bundleNames, name)
	}
	sort.Strings(bundleNames)

	groupBundle := make(map[string]string, len(c.Bundles))
	for _, bundle := range bundleNames {
		if strings.TrimSpace(bundle) == "" {
			return fmt.Errorf("bundle name must not be empty")
		}
		if err := validateName("bundle", bundle); err != nil {
			return err
		}
		groups := c.Bundles[bundle]
		if len(groups) == 0 {
			return fmt.Errorf("bundle %q has no groups", bundle)
		}
		for _, group := range groups {
			if err := validateName("group", group); err != nil {
				return fmt.Errorf("bundle %q: %w", bundle, err)
			}
			if previous, ok := groupBundle[group]; ok {
				return fmt.Errorf("group %q is in bundles %q and %q", group, previous, bundle)
			}
			groupBundle[group] = bundle
		}
	}

	knownGroups := sets.NewString(c.Groups()...)
	bundledGroups := sets.NewString()
	for group := range groupBundle {
		bundledGroups.Insert(group)
	}
	for _, group := range bundledGroups.List() {
		if !knownGroups.Has(group) {
			return fmt.Errorf("bundle %q references unknown group %q", groupBundle[group], group)
		}
	}

	if missing := knownGroups.Difference(bundledGroups); missing.Len() > 0 {
		return fmt.Errorf("groups not in any bundle: %s", strings.Join(missing.List(), ", "))
	}

	return nil
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
