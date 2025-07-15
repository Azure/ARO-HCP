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
	OperandImageEnvPrefixes []string `yaml:"operandImageEnvPrefixes"` // prefixes for operand image environment variables
	ImageRegistryParam      string   `yaml:"imageRegistryParam"`      // parameter name for image registry templating

	// Validation rules
	RequiredEnvVarPrefixes []string `yaml:"requiredEnvVarPrefixes"` // required environment variable prefixes
	RequiredResources      []string `yaml:"requiredResources"`      // required Kubernetes resource types

	// Customization
	AnnotationPrefixesToRemove []string `yaml:"annotationPrefixesToRemove"` // annotation prefixes to remove from manifests
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
	if c.ImageRegistryParam == "" {
		return fmt.Errorf("imageRegistryParam cannot be empty")
	}
	return nil
}
