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
	"strings"

	"gopkg.in/yaml.v3"
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
	Image      string `yaml:"image"`
	TagPattern string `yaml:"tagPattern,omitempty"`
}

// Target defines where to update the image digest
type Target struct {
	JsonPath string `yaml:"jsonPath"`
	FilePath string `yaml:"filePath"`
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

// Registry returns the registry part of the image reference
func (s *Source) Registry() (string, error) {
	registry, _, err := s.ParseImageReference()
	return registry, err
}

// Repository returns the repository part of the image reference
func (s *Source) Repository() (string, error) {
	_, repository, err := s.ParseImageReference()
	return repository, err
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

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// validate ensures the configuration is complete and valid
func (c *Config) validate() error {
	if len(c.Images) == 0 {
		return fmt.Errorf("no images configured")
	}

	for name, img := range c.Images {
		if img.Source.Image == "" {
			return fmt.Errorf("image %s: source image is required", name)
		}
		// Validate that the image reference can be parsed
		if _, _, err := img.Source.ParseImageReference(); err != nil {
			return fmt.Errorf("image %s: %w", name, err)
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
