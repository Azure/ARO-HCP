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
	"time"

	"gopkg.in/yaml.v3"
)

// Config defines the tenant quota collector configuration.
type Config struct {
	Interval string         `yaml:"interval"`
	Timeout  string         `yaml:"timeout"`
	Tenants  []TenantConfig `yaml:"tenants"`
}

// TenantConfig defines configuration for a specific tenant.
type TenantConfig struct {
	TenantID   string `yaml:"tenantId"`
	TenantName string `yaml:"tenantName,omitempty"`
	// Service Principal Client ID for this tenant
	ServicePrincipalClientId string `yaml:"servicePrincipalClientId"`
	// Key Vault secret name for this tenant's service principal
	KeyVaultSecretName string `yaml:"keyVaultSecretName"`
	Scope              string `yaml:"scope,omitempty"`
}

// LoadFromFile loads configuration from a YAML file.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// Validate the configuration.
func (c *Config) Validate() error {
	if c.Interval == "" {
		return fmt.Errorf("interval is required")
	}
	if _, err := time.ParseDuration(c.Interval); err != nil {
		return fmt.Errorf("invalid interval format: %w", err)
	}

	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			return fmt.Errorf("invalid timeout format: %w", err)
		}
	}

	if len(c.Tenants) == 0 {
		return fmt.Errorf("at least one tenant must be configured")
	}

	for i, tenant := range c.Tenants {
		if tenant.TenantID == "" {
			return fmt.Errorf("tenant %d: tenantId is required", i)
		}
		if tenant.ServicePrincipalClientId == "" {
			return fmt.Errorf("tenant %d: servicePrincipalClientId is required", i)
		}
		if tenant.KeyVaultSecretName == "" {
			return fmt.Errorf("tenant %d: keyVaultSecretName is required", i)
		}
	}

	return nil
}
