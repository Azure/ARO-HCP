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

package customize

import (
	"errors"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// BundleConfig defines configuration for customizing OLM bundles
type BundleConfig struct {
	// Chart metadata
	ChartName        string `yaml:"chartName"`
	ChartDescription string `yaml:"chartDescription"`

	// Operator detection
	OperatorDeploymentNames    []string          `yaml:"operatorDeploymentNames"`    // deployment names that contain these strings
	OperatorDeploymentSelector map[string]string `yaml:"operatorDeploymentSelector"` // label selectors for operator deployments

	// Image parameterization
	OperandImageEnvPrefixes  []string `yaml:"operandImageEnvPrefixes"`  // prefixes for operand image environment variables
	OperandImageEnvSuffixes  []string `yaml:"operandImageEnvSuffixes"`  // suffixes for operand image environment variables
	ImageRegistryParam       string   `yaml:"imageRegistryParam"`       // parameter name for image registry templating
	ImageRepositoryParam     string   `yaml:"imageRepositoryParam"`     // parameter name for image repository templating
	ImageRootRepositoryParam string   `yaml:"imageRootRepositoryParam"` // parameter name for image root repository templating
	ImageTagParam            string   `yaml:"imageTagParam"`            // parameter name for image tag templating (mutually exclusive with ImageDigestParam)
	ImageDigestParam         string   `yaml:"imageDigestParam"`         // parameter name for image digest templating (mutually exclusive with ImageTagParam)
	PerImageCustomization    bool     `yaml:"perImageCustomization"`    // if true, each image reference gets individual parameters with suffixes (default: false)

	// Validation rules
	RequiredEnvVarPrefixes []string `yaml:"requiredEnvVarPrefixes"` // required environment variable prefixes
	RequiredResources      []string `yaml:"requiredResources"`      // required Kubernetes resource types

	// Customization
	AnnotationPrefixesToRemove []string           `yaml:"annotationPrefixesToRemove"`  // annotation prefixes to remove from manifests
	ManifestOverrides          []ManifestOverride `yaml:"manifestOverrides,omitempty"` // manifest-level overrides for fine-grained customization
}

// ManifestOverride defines a set of operations to apply to manifests matching a selector
type ManifestOverride struct {
	Selector   Selector    `yaml:"selector"`
	Operations []Operation `yaml:"operations"`
}

// Selector identifies which manifests to apply operations to
type Selector struct {
	Kind       string `yaml:"kind"`                 // Kubernetes resource kind (required)
	Name       string `yaml:"name,omitempty"`       // Resource name (optional, matches all if empty)
	APIVersion string `yaml:"apiVersion,omitempty"` // API version for disambiguation (optional)
}

// Operation defines a modification to apply to a manifest field
type Operation struct {
	Op    string      `yaml:"op"`              // Operation type: add, replace, remove
	Path  string      `yaml:"path"`            // JSONPath-like path to the field
	Value interface{} `yaml:"value,omitempty"` // Value to set (required for add/replace)
	Merge bool        `yaml:"merge,omitempty"` // For add operations on maps, merge with existing values
}

// LoadFromFile loads configuration from a YAML file
func LoadFromFile(configPath string) (*BundleConfig, error) {
	if configPath == "" {
		return nil, errors.New("a configuration file must be provided")
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file does not exist: %s", configPath)
	}

	// Read file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file: %v", err)
	}

	// Parse YAML
	config := &BundleConfig{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse configuration file: %v", err)
	}

	return config, nil
}

// Validate checks if the configuration is valid
func (c *BundleConfig) Validate() error {
	if c.ChartName == "" {
		return fmt.Errorf("chartName cannot be empty")
	}
	if c.ChartDescription == "" {
		return fmt.Errorf("chartDescription cannot be empty")
	}
	if len(c.OperatorDeploymentNames) == 0 && len(c.OperatorDeploymentSelector) == 0 {
		return fmt.Errorf("at least one of operatorDeploymentNames or operatorDeploymentSelector must be specified")
	}
	// ImageTagParam and ImageDigestParam are mutually exclusive
	if c.ImageTagParam != "" && c.ImageDigestParam != "" {
		return fmt.Errorf("imageTagParam and imageDigestParam are mutually exclusive - only one can be set")
	}
	// ImageRootRepositoryParam and ImageRepositoryParam are mutually exclusive
	if c.ImageRootRepositoryParam != "" && c.ImageRepositoryParam != "" {
		return fmt.Errorf("imageRootRepositoryParam and imageRepositoryParam are mutually exclusive - only one can be set")
	}

	for i, override := range c.ManifestOverrides {
		if override.Selector.Kind == "" {
			return fmt.Errorf("manifestOverrides[%d]: selector kind cannot be empty", i)
		}

		if len(override.Operations) == 0 {
			return fmt.Errorf("manifestOverrides[%d]: at least one operation must be specified", i)
		}

		for j, op := range override.Operations {
			if op.Op != "add" && op.Op != "replace" && op.Op != "remove" {
				return fmt.Errorf("manifestOverrides[%d].operations[%d]: invalid operation type '%s', must be one of: add, replace, remove", i, j, op.Op)
			}

			if op.Path == "" {
				return fmt.Errorf("manifestOverrides[%d].operations[%d]: operation path cannot be empty", i, j)
			}

			if (op.Op == "add" || op.Op == "replace") && op.Value == nil {
				return fmt.Errorf("manifestOverrides[%d].operations[%d]: operation value is required for add and replace operations", i, j)
			}

			if op.Merge && op.Op != "add" {
				return fmt.Errorf("manifestOverrides[%d].operations[%d]: merge flag can only be used with add operation", i, j)
			}
		}
	}

	return nil
}
