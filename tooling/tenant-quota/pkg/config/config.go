// Copyright 2026 Microsoft Corporation
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
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	DefaultTimeout       = 30 * time.Second
	DefaultInterval      = 15 * time.Minute
	DefaultCacheTTL      = 24 * time.Hour
	DefaultScope         = "https://graph.microsoft.com/.default"
	DefaultProwInterval  = 5 * time.Minute
	DefaultProwRetention = 24 * time.Hour

	DefaultCIJobOutcomesInterval = 5 * time.Minute
	// DefaultCIJobOutcomesWindow is how much recent history each pass reads.
	// Runs already stored are subtracted, so this only has to be long enough to
	// cover the slowest run completing after faster ones started later, and
	// short enough that a pass does not fetch months of runs in one unpaginated
	// response. A gap longer than this is not backfilled.
	DefaultCIJobOutcomesWindow = 24 * time.Hour
	// DefaultCIJobOutcomesTimeout bounds a single pass, so that one stuck
	// network call cannot hold the collector past its next tick and stall
	// collection indefinitely.
	//
	// It is far longer than the collector-wide timeout because a pass reads one
	// set of artifacts per run it has not seen, and a first pass over an empty
	// table has a day of runs to read. Cutting a pass short is safe - runs
	// already stored are subtracted from the next one, so a long backlog is
	// worked through over several passes rather than lost.
	DefaultCIJobOutcomesTimeout = 10 * time.Minute
)

type Config struct {
	Interval string         `yaml:"interval"`
	Timeout  string         `yaml:"timeout"`
	CacheTTL string         `yaml:"cacheTTL,omitempty"`
	Tenants  []TenantConfig `yaml:"tenants"`
	Prow     ProwConfig     `yaml:"prow,omitempty"`

	CIJobOutcomes CIJobOutcomesConfig `yaml:"ciJobOutcomes,omitempty"`

	intervalDuration time.Duration
	timeoutDuration  time.Duration
	cacheTTLDuration time.Duration
}

// KustoTableConfig names a destination table and the ingestion mapping declared
// alongside it in the table's own .kql file. The two are separate because a
// mapping is a distinct Kusto object whose name is not derivable from the
// table's.
type KustoTableConfig struct {
	Table            string `yaml:"table"`
	IngestionMapping string `yaml:"ingestionMapping"`
}

// CIJobOutcomesConfig configures recording CI job outcomes in Kusto.
type CIJobOutcomesConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ClusterURI   string `yaml:"clusterURI"`
	IngestionURI string `yaml:"ingestionURI"`
	Database     string `yaml:"database"`

	// Outcomes holds one row per run; TestNames is the test dimension; and
	// TestResults joins the two with the per-test outcome and timings.
	Outcomes    KustoTableConfig `yaml:"outcomes"`
	TestNames   KustoTableConfig `yaml:"testNames"`
	TestResults KustoTableConfig `yaml:"testResults"`

	SippyURI  string   `yaml:"sippyURI"`
	Releases  []string `yaml:"releases"`
	JobFilter string   `yaml:"jobFilter"`
	Interval  string   `yaml:"interval,omitempty"`
	Window    string   `yaml:"window,omitempty"`
	Timeout   string   `yaml:"timeout,omitempty"`

	intervalDuration time.Duration
	windowDuration   time.Duration
	timeoutDuration  time.Duration
}

func (c *CIJobOutcomesConfig) GetInterval() time.Duration {
	return c.intervalDuration
}

func (c *CIJobOutcomesConfig) GetWindow() time.Duration {
	return c.windowDuration
}

func (c *CIJobOutcomesConfig) GetTimeout() time.Duration {
	return c.timeoutDuration
}

func (c *CIJobOutcomesConfig) validate() error {
	if err := parseDuration(c.Interval, DefaultCIJobOutcomesInterval, &c.intervalDuration, "ciJobOutcomes.interval"); err != nil {
		return err
	}
	if err := parseDuration(c.Window, DefaultCIJobOutcomesWindow, &c.windowDuration, "ciJobOutcomes.window"); err != nil {
		return err
	}
	if err := parseDuration(c.Timeout, DefaultCIJobOutcomesTimeout, &c.timeoutDuration, "ciJobOutcomes.timeout"); err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}

	required := map[string]string{
		"ciJobOutcomes.database":  c.Database,
		"ciJobOutcomes.jobFilter": c.JobFilter,
	}
	for prefix, table := range map[string]KustoTableConfig{
		"ciJobOutcomes.outcomes":    c.Outcomes,
		"ciJobOutcomes.testNames":   c.TestNames,
		"ciJobOutcomes.testResults": c.TestResults,
	} {
		required[prefix+".table"] = table.Table
		required[prefix+".ingestionMapping"] = table.IngestionMapping
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	// Every endpoint is checked here rather than left to fail on first use: a
	// missing scheme is accepted by url.Parse and only surfaces as a connection
	// error once the collector is already running.
	for name, value := range map[string]string{
		"ciJobOutcomes.clusterURI":   c.ClusterURI,
		"ciJobOutcomes.ingestionURI": c.IngestionURI,
		"ciJobOutcomes.sippyURI":     c.SippyURI,
	} {
		if err := validateHTTPURL(value); err != nil {
			return fmt.Errorf("%s %w", name, err)
		}
	}
	if len(c.Releases) == 0 {
		return fmt.Errorf("ciJobOutcomes.releases requires at least one release")
	}
	for i, release := range c.Releases {
		if strings.TrimSpace(release) == "" {
			return fmt.Errorf("ciJobOutcomes.releases[%d] must not be empty", i)
		}
	}
	return nil
}

type ProwConfig struct {
	Enabled     bool                 `yaml:"enabled"`
	BaseURL     string               `yaml:"baseURL"`
	Interval    string               `yaml:"interval,omitempty"`
	Retention   string               `yaml:"retention,omitempty"`
	Repository  ProwRepositoryConfig `yaml:"repository"`
	ExcludeJobs []string             `yaml:"excludeJobs,omitempty"`

	intervalDuration  time.Duration
	retentionDuration time.Duration
}

type ProwRepositoryConfig struct {
	Org  string `yaml:"org"`
	Name string `yaml:"name"`
}

type TenantConfig struct {
	TenantID                 string               `yaml:"tenantId"`
	TenantName               string               `yaml:"tenantName,omitempty"`
	ServicePrincipalClientId string               `yaml:"servicePrincipalClientId"`
	KeyVaultSecretName       string               `yaml:"keyVaultSecretName"`
	Scope                    string               `yaml:"scope,omitempty"`
	DirectoryQuota           *bool                `yaml:"directoryQuota,omitempty"`
	Subscriptions            []SubscriptionConfig `yaml:"subscriptions,omitempty"`
}

type SubscriptionConfig struct {
	Name    string   `yaml:"name"`
	Regions []string `yaml:"regions"`

	// SubscriptionID is resolved at runtime from the Name field using the
	// Azure subscriptions API. Not parsed from config YAML.
	SubscriptionID string `yaml:"-"`
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
	if err := parseDuration(c.Interval, DefaultInterval, &c.intervalDuration, "interval"); err != nil {
		return err
	}
	if err := parseDuration(c.Timeout, DefaultTimeout, &c.timeoutDuration, "timeout"); err != nil {
		return err
	}
	if err := parseDuration(c.CacheTTL, DefaultCacheTTL, &c.cacheTTLDuration, "cacheTTL"); err != nil {
		return err
	}
	if err := c.Prow.validate(); err != nil {
		return err
	}
	if err := c.CIJobOutcomes.validate(); err != nil {
		return err
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

		for j, s := range t.Subscriptions {
			if s.Name == "" {
				return fmt.Errorf("tenant[%d].subscriptions[%d]: name is required", i, j)
			}
			if len(s.Regions) == 0 {
				return fmt.Errorf("tenant[%d].subscriptions[%d]: at least one region is required", i, j)
			}
		}
	}

	return nil
}

// validateHTTPURL reports whether value is a usable HTTP or HTTPS endpoint.
func validateHTTPURL(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("must be a valid HTTP or HTTPS URL, got %q", trimmed)
	}
	return nil
}

func (c *ProwConfig) validate() error {
	if err := parseDuration(c.Interval, DefaultProwInterval, &c.intervalDuration, "prow.interval"); err != nil {
		return err
	}
	if err := parseDuration(c.Retention, DefaultProwRetention, &c.retentionDuration, "prow.retention"); err != nil {
		return err
	}
	if !c.Enabled {
		return nil
	}

	parsedURL, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return fmt.Errorf("prow.baseURL must be a valid HTTP or HTTPS URL")
	}
	if strings.TrimSpace(c.Repository.Org) == "" {
		return fmt.Errorf("prow.repository.org is required")
	}
	if strings.TrimSpace(c.Repository.Name) == "" {
		return fmt.Errorf("prow.repository.name is required")
	}

	excludedJobs := sets.New[string]()
	for i, jobName := range c.ExcludeJobs {
		normalized := strings.TrimSpace(jobName)
		if normalized == "" {
			return fmt.Errorf("prow.excludeJobs[%d] must not be empty", i)
		}
		excludedJobs.Insert(normalized)
	}
	c.ExcludeJobs = sets.List(excludedJobs)
	return nil
}

func parseDuration(raw string, defaultVal time.Duration, dst *time.Duration, name string) error {
	if raw == "" {
		*dst = defaultVal
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return fmt.Errorf("%s must be positive, got %v", name, d)
	}
	*dst = d
	return nil
}

func (c *Config) GetInterval() time.Duration {
	return c.intervalDuration
}

func (c *Config) GetTimeout() time.Duration {
	return c.timeoutDuration
}

func (c *Config) GetCacheTTL() time.Duration {
	return c.cacheTTLDuration
}

func (c *ProwConfig) GetInterval() time.Duration {
	return c.intervalDuration
}

func (c *ProwConfig) GetRetention() time.Duration {
	return c.retentionDuration
}

// HasSubscriptions returns true if any tenant has subscription quota monitoring configured.
func (c *Config) HasSubscriptions() bool {
	for _, t := range c.Tenants {
		if len(t.Subscriptions) > 0 {
			return true
		}
	}
	return false
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

// IsDirectoryQuotaEnabled returns true if directory quota collection is enabled.
// Defaults to true when not explicitly set, for backward compatibility.
func (t *TenantConfig) IsDirectoryQuotaEnabled() bool {
	if t.DirectoryQuota == nil {
		return true
	}
	return *t.DirectoryQuota
}
