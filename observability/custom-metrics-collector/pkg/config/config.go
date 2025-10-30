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
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Collectors []Collector `yaml:"collectors"`
}

type Collector struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type,omitempty"`
	ID       string   `yaml:"id"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Metrics  []Metric `yaml:"metrics"`
	// Per-collector authentication (optional, falls back to global/default)
	Auth *CollectorAuth `yaml:"auth,omitempty"`
	// The collector detects which tenant it's running in and uses the matching entry.
	Tenants []TenantConfig `yaml:"tenants,omitempty"`
}

// CollectorAuth defines authentication configuration for a collector function.
// If not specified, the collector will use global/default authentication.
type CollectorAuth struct {
	// Service Principal Client ID for this collector (overrides global)
	ServicePrincipalClientId string `yaml:"servicePrincipalClientId,omitempty"`
	// Key Vault secret name for this collector (overrides global)
	KeyVaultSecretName string `yaml:"keyVaultSecretName,omitempty"`
	// Tenant ID (if different from global)
	TenantID string `yaml:"tenantId,omitempty"`
	// API scope (e.g., "https://graph.microsoft.com/.default" or "https://management.azure.com/.default")
	Scope string `yaml:"scope,omitempty"`
	// Authentication method: "servicePrincipal", "managedIdentity", "default"
	Method string `yaml:"method,omitempty"`
}

// TenantConfig defines configuration for a specific tenant.
type TenantConfig struct {
	// Tenant ID (required) - used to match against the detected tenant
	TenantID string `yaml:"tenantId"`
	// Tenant name (optional, for display/logging)
	TenantName string `yaml:"tenantName,omitempty"`
	// Service Principal Client ID for this tenant
	ServicePrincipalClientId string `yaml:"servicePrincipalClientId"`
	// Key Vault secret name for this tenant's service principal
	KeyVaultSecretName string `yaml:"keyVaultSecretName"`
	// API scope (optional, defaults to collector's auth.scope or global)
	Scope string `yaml:"scope,omitempty"`
}

// CollectorContext provides context and configuration to collector functions.
// It includes the execution context, authentication configuration, and logger.
// This is passed to collector functions when they are executed.
type CollectorContext struct {
	Context interface{}
	Auth    *CollectorAuth
	Tenants []TenantConfig
	Logger  interface{}
}

// Takes a CollectorContext (which includes context, auth config, and tenants)
// and returns key=value formatted output and an error.
type CollectorFunc func(ctx CollectorContext) (string, error)

type Metric struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Help      string   `yaml:"help"`
	Labels    []string `yaml:"labels"`
	Source    string   `yaml:"source"`
	Transform string   `yaml:"transform,omitempty"`
}

const (
	metricTypeGauge   = "gauge"
	metricTypeCounter = "counter"
)

var labelNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

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

func (c *Config) Validate() error {
	if len(c.Collectors) == 0 {
		return fmt.Errorf("no collectors configured")
	}

	for i, collector := range c.Collectors {
		if collector.Name == "" {
			return fmt.Errorf("collector %d: name is required", i)
		}
		if collector.ID == "" {
			return fmt.Errorf("collector %d: id is required", i)
		}
		// Default type to "builtin" if not specified
		if c.Collectors[i].Type == "" {
			c.Collectors[i].Type = "builtin"
		}
		if c.Collectors[i].Type != "builtin" {
			return fmt.Errorf("collector %d: type must be 'builtin' (script execution not supported)", i)
		}
		if collector.Interval == "" {
			return fmt.Errorf("collector %d: interval is required", i)
		}
		if _, err := time.ParseDuration(collector.Interval); err != nil {
			return fmt.Errorf("collector %d: invalid interval format: %w", i, err)
		}
		if collector.Timeout != "" {
			if _, err := time.ParseDuration(collector.Timeout); err != nil {
				return fmt.Errorf("collector %d: invalid timeout format: %w", i, err)
			}
		}
		if len(collector.Metrics) == 0 {
			return fmt.Errorf("collector %d: no metrics configured", i)
		}

		for j, metric := range collector.Metrics {
			if metric.Name == "" {
				return fmt.Errorf("collector %d, metric %d: name is required", i, j)
			}
			if metric.Type == "" {
				return fmt.Errorf("collector %d, metric %d: type is required", i, j)
			}
			if metric.Type != metricTypeGauge && metric.Type != metricTypeCounter {
				return fmt.Errorf("collector %d, metric %d: type must be 'gauge' or 'counter'", i, j)
			}
			if metric.Help == "" {
				return fmt.Errorf("collector %d, metric %d: help is required", i, j)
			}
			if metric.Source == "" {
				return fmt.Errorf("collector %d, metric %d: source is required", i, j)
			}
			// Validate label names (Prometheus-compatible)
			for k, label := range metric.Labels {
				if !labelNameRe.MatchString(label) {
					return fmt.Errorf("collector %d, metric %d, label %d: invalid label name '%s'", i, j, k, label)
				}
			}
		}
	}

	return nil
}
