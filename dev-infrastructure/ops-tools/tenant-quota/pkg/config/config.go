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

const (
	DefaultTimeout  = 30 * time.Second
	DefaultInterval = 15 * time.Minute
	DefaultScope    = "https://graph.microsoft.com/.default"
)

type Config struct {
	Interval string         `yaml:"interval"`
	Timeout  string         `yaml:"timeout"`
	Tenants  []TenantConfig `yaml:"tenants"`

	intervalDuration time.Duration
	timeoutDuration  time.Duration
}

type TenantConfig struct {
	TenantID                 string `yaml:"tenantId"`
	TenantName               string `yaml:"tenantName,omitempty"`
	ServicePrincipalClientId string `yaml:"servicePrincipalClientId"`
	KeyVaultSecretName       string `yaml:"keyVaultSecretName"`
	Scope                    string `yaml:"scope,omitempty"`
}

func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Interval == "" {
		c.intervalDuration = DefaultInterval
	} else {
		d, err := time.ParseDuration(c.Interval)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", c.Interval, err)
		}
		if d <= 0 {
			return fmt.Errorf("interval must be positive, got %v", d)
		}
		c.intervalDuration = d
	}

	if c.Timeout == "" {
		c.timeoutDuration = DefaultTimeout
	} else {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("invalid timeout %q: %w", c.Timeout, err)
		}
		if d <= 0 {
			return fmt.Errorf("timeout must be positive, got %v", d)
		}
		c.timeoutDuration = d
	}

	if len(c.Tenants) == 0 {
		return fmt.Errorf("at least one tenant must be configured")
	}

	seen := make(map[string]bool)
	for i, t := range c.Tenants {
		if t.TenantID == "" {
			return fmt.Errorf("tenant[%d]: tenantId is required", i)
		}
		if t.ServicePrincipalClientId == "" {
			return fmt.Errorf("tenant[%d]: servicePrincipalClientId is required", i)
		}
		if t.KeyVaultSecretName == "" {
			return fmt.Errorf("tenant[%d]: keyVaultSecretName is required", i)
		}
		if seen[t.TenantID] {
			return fmt.Errorf("tenant[%d]: duplicate tenantId %q", i, t.TenantID)
		}
		seen[t.TenantID] = true
	}

	return nil
}

func (c *Config) GetInterval() time.Duration {
	return c.intervalDuration
}

func (c *Config) GetTimeout() time.Duration {
	return c.timeoutDuration
}

func (t *TenantConfig) GetScope() string {
	if t.Scope != "" {
		return t.Scope
	}
	return DefaultScope
}

func (t *TenantConfig) GetDisplayName() string {
	if t.TenantName != "" {
		return t.TenantName
	}
	return t.TenantID
}
