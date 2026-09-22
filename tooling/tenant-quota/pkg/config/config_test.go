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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// minimalValidTenant returns a TenantConfig with all required fields set.
func minimalValidTenant() TenantConfig {
	return TenantConfig{
		TenantID:                 "tenant-id-1",
		ServicePrincipalClientId: "sp-client-id",
		KeyVaultSecretName:       "kv-secret",
	}
}

// minimalValidConfig returns a Config with all required fields and no optional ones.
func minimalValidConfig() Config {
	return Config{
		Tenants: []TenantConfig{minimalValidTenant()},
	}
}

// writeYAML writes content to a temp file and returns the path.
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

// boolPtr is a helper to get a pointer to a bool literal.
func boolPtr(b bool) *bool { return &b }

func TestConfigValidate(t *testing.T) {
	type testCase struct {
		name       string
		cfg        Config
		mutate     func(*Config)
		wantErrSub string
		assertions func(t *testing.T, cfg Config)
	}

	testCases := []testCase{
		{
			name: "defaults",
			cfg:  minimalValidConfig(),
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.GetInterval() != DefaultInterval {
					t.Fatalf("interval: got %v, want %v", cfg.GetInterval(), DefaultInterval)
				}
				if cfg.GetTimeout() != DefaultTimeout {
					t.Fatalf("timeout: got %v, want %v", cfg.GetTimeout(), DefaultTimeout)
				}
				if cfg.GetCacheTTL() != DefaultCacheTTL {
					t.Fatalf("cacheTTL: got %v, want %v", cfg.GetCacheTTL(), DefaultCacheTTL)
				}
				if cfg.Prow.GetInterval() != DefaultProwInterval {
					t.Fatalf("prow interval: got %v, want %v", cfg.Prow.GetInterval(), DefaultProwInterval)
				}
				if cfg.Prow.GetRetention() != DefaultProwRetention {
					t.Fatalf("prow retention: got %v, want %v", cfg.Prow.GetRetention(), DefaultProwRetention)
				}
			},
		},
		{
			name: "explicit durations",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Interval = "5m"
				cfg.Timeout = "10s"
				cfg.CacheTTL = "1h"
			},
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.GetInterval() != 5*time.Minute {
					t.Fatalf("interval: got %v, want 5m", cfg.GetInterval())
				}
				if cfg.GetTimeout() != 10*time.Second {
					t.Fatalf("timeout: got %v, want 10s", cfg.GetTimeout())
				}
				if cfg.GetCacheTTL() != time.Hour {
					t.Fatalf("cacheTTL: got %v, want 1h", cfg.GetCacheTTL())
				}
			},
		},
		{
			name:       "bad interval",
			cfg:        minimalValidConfig(),
			mutate:     func(cfg *Config) { cfg.Interval = "notaduration" },
			wantErrSub: "invalid interval",
		},
		{
			name:       "zero interval",
			cfg:        minimalValidConfig(),
			mutate:     func(cfg *Config) { cfg.Interval = "0s" },
			wantErrSub: "interval must be positive",
		},
		{
			name:       "negative interval",
			cfg:        minimalValidConfig(),
			mutate:     func(cfg *Config) { cfg.Interval = "-1m" },
			wantErrSub: "interval must be positive",
		},
		{
			name:       "bad timeout",
			cfg:        minimalValidConfig(),
			mutate:     func(cfg *Config) { cfg.Timeout = "xyz" },
			wantErrSub: "invalid timeout",
		},
		{
			name:       "bad cacheTTL",
			cfg:        minimalValidConfig(),
			mutate:     func(cfg *Config) { cfg.CacheTTL = "bad" },
			wantErrSub: "invalid cacheTTL",
		},
		{
			name: "valid prow config",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.Interval = "10m"
				cfg.Prow.Retention = "48h"
				cfg.Prow.ExcludeJobs = []string{" excluded-job "}
			},
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.Prow.GetInterval() != 10*time.Minute {
					t.Fatalf("prow interval: got %v, want 10m", cfg.Prow.GetInterval())
				}
				if cfg.Prow.GetRetention() != 48*time.Hour {
					t.Fatalf("prow retention: got %v, want 48h", cfg.Prow.GetRetention())
				}
				if len(cfg.Prow.ExcludeJobs) != 1 || cfg.Prow.ExcludeJobs[0] != "excluded-job" {
					t.Fatalf("prow excludeJobs: got %v", cfg.Prow.ExcludeJobs)
				}
			},
		},
		{
			name: "prow disabled allows empty connection settings",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = ProwConfig{Enabled: false}
			},
		},
		{
			name: "prow invalid base URL",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.BaseURL = "not-a-url"
			},
			wantErrSub: "prow.baseURL",
		},
		{
			name: "prow missing repository org",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.Repository.Org = ""
			},
			wantErrSub: "prow.repository.org is required",
		},
		{
			name: "prow missing repository name",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.Repository.Name = ""
			},
			wantErrSub: "prow.repository.name is required",
		},
		{
			name: "prow empty exclusion",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.ExcludeJobs = []string{""}
			},
			wantErrSub: "prow.excludeJobs[0] must not be empty",
		},
		{
			name: "prow duplicate exclusions are deduplicated",
			cfg:  minimalValidConfig(),
			mutate: func(cfg *Config) {
				cfg.Prow = validProwConfig()
				cfg.Prow.ExcludeJobs = []string{" job-b ", "job-a", "job-b"}
			},
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if !slices.Equal(cfg.Prow.ExcludeJobs, []string{"job-a", "job-b"}) {
					t.Fatalf("prow excludeJobs: got %v, want [job-a job-b]", cfg.Prow.ExcludeJobs)
				}
			},
		},
		{
			name:       "no tenants",
			cfg:        Config{},
			wantErrSub: "at least one tenant must be configured",
		},
		{
			name: "missing tenantId",
			cfg: Config{
				Tenants: []TenantConfig{
					{ServicePrincipalClientId: "sp", KeyVaultSecretName: "kv"},
				},
			},
			wantErrSub: "tenant[0]: tenantId is required",
		},
		{
			name: "missing servicePrincipalClientId",
			cfg: Config{
				Tenants: []TenantConfig{
					{TenantID: "tid", KeyVaultSecretName: "kv"},
				},
			},
			wantErrSub: "tenant[0]: servicePrincipalClientId is required",
		},
		{
			name: "missing keyVaultSecretName",
			cfg: Config{
				Tenants: []TenantConfig{
					{TenantID: "tid", ServicePrincipalClientId: "sp"},
				},
			},
			wantErrSub: "tenant[0]: keyVaultSecretName is required",
		},
		{
			name: "duplicate tenantId",
			cfg: Config{
				Tenants: []TenantConfig{
					minimalValidTenant(),
					minimalValidTenant(),
				},
			},
			wantErrSub: `tenant[1]: duplicate tenantId "tenant-id-1"`,
		},
		{
			name: "missing subscription name",
			cfg: Config{
				Tenants: []TenantConfig{
					func() TenantConfig {
						tenant := minimalValidTenant()
						tenant.Subscriptions = []SubscriptionConfig{{Regions: []string{"eastus"}}}
						return tenant
					}(),
				},
			},
			wantErrSub: "tenant[0].subscriptions[0]: name is required",
		},
		{
			name: "missing subscription regions",
			cfg: Config{
				Tenants: []TenantConfig{
					func() TenantConfig {
						tenant := minimalValidTenant()
						tenant.Subscriptions = []SubscriptionConfig{{Name: "sub"}}
						return tenant
					}(),
				},
			},
			wantErrSub: "tenant[0].subscriptions[0]: at least one region is required",
		},
		{
			name: "valid subscriptions",
			cfg: Config{
				Tenants: []TenantConfig{
					func() TenantConfig {
						tenant := minimalValidTenant()
						tenant.Subscriptions = []SubscriptionConfig{
							{Name: "prod", Regions: []string{"eastus", "westus"}},
						}
						return tenant
					}(),
				},
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}

			err := cfg.Validate()
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.assertions != nil {
				tc.assertions(t, cfg)
			}
		})
	}
}

func validProwConfig() ProwConfig {
	return ProwConfig{
		Enabled: true,
		BaseURL: "https://prow.ci.openshift.org",
		Repository: ProwRepositoryConfig{
			Org:  "Azure",
			Name: "ARO-HCP",
		},
	}
}

func TestLoadFromFile(t *testing.T) {
	type testCase struct {
		name       string
		setup      func(t *testing.T) string
		assertions func(t *testing.T, cfg *Config, err error)
	}

	testCases := []testCase{
		{
			name: "valid",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeYAML(t, `
prow:
  enabled: true
  baseURL: "https://prow.ci.openshift.org"
  repository:
    org: "Azure"
    name: "ARO-HCP"
  excludeJobs:
    - "excluded-job"
tenants:
  - tenantId: "tid-1"
    servicePrincipalClientId: "sp-id"
    keyVaultSecretName: "kv-secret"
    subscriptions:
      - name: "prod"
        regions:
          - eastus
`)
			},
			assertions: func(t *testing.T, cfg *Config, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg == nil {
					t.Fatal("expected config, got nil")
					return
				}
				if len(cfg.Tenants) != 1 {
					t.Fatalf("tenants: got %d, want 1", len(cfg.Tenants))
				}
				if cfg.Tenants[0].TenantID != "tid-1" {
					t.Fatalf("tenantId: got %q, want %q", cfg.Tenants[0].TenantID, "tid-1")
				}
				if len(cfg.Tenants[0].Subscriptions) != 1 {
					t.Fatalf("subscriptions: got %d, want 1", len(cfg.Tenants[0].Subscriptions))
				}
				if cfg.Tenants[0].Subscriptions[0].Name != "prod" {
					t.Fatalf("subscription name: got %q, want %q", cfg.Tenants[0].Subscriptions[0].Name, "prod")
				}
				if cfg.Tenants[0].Subscriptions[0].SubscriptionID != "" {
					t.Fatalf("subscriptionId: got %q, want empty runtime-resolved value", cfg.Tenants[0].Subscriptions[0].SubscriptionID)
				}
				if !cfg.Prow.Enabled || cfg.Prow.Repository.Name != "ARO-HCP" {
					t.Fatalf("prow config: got %#v", cfg.Prow)
				}
			},
		},
		{
			name: "not found",
			setup: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "nonexistent.yaml")
			},
			assertions: func(t *testing.T, _ *Config, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error for missing file")
				}
				if !strings.Contains(err.Error(), "read config file") {
					t.Fatalf("expected read error, got %v", err)
				}
			},
		},
		{
			name: "ignores legacy role assignment limit",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeYAML(t, `
tenants:
  - tenantId: "tid-1"
    servicePrincipalClientId: "sp-id"
    keyVaultSecretName: "kv-secret"
    subscriptions:
      - name: "prod"
        roleAssignmentLimit: 8000
        regions:
          - eastus
`)
			},
			assertions: func(t *testing.T, cfg *Config, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(cfg.Tenants[0].Subscriptions) != 1 {
					t.Fatalf("subscriptions: got %d, want 1", len(cfg.Tenants[0].Subscriptions))
				}
			},
		},
		{
			name: "invalid yaml",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeYAML(t, "{ this is: [not valid yaml")
			},
			assertions: func(t *testing.T, _ *Config, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected error for invalid YAML")
				}
				if !strings.Contains(err.Error(), "parse config file") {
					t.Fatalf("expected parse error, got %v", err)
				}
			},
		},
		{
			name: "fails validation",
			setup: func(t *testing.T) string {
				t.Helper()
				return writeYAML(t, "tenants: []\n")
			},
			assertions: func(t *testing.T, _ *Config, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected validation error for empty tenants")
				}
				if !strings.Contains(err.Error(), "at least one tenant must be configured") {
					t.Fatalf("expected validation error, got %v", err)
				}
			},
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			cfg, err := LoadFromFile(path)
			tc.assertions(t, cfg, err)
		})
	}
}

func TestConfigHasSubscriptions(t *testing.T) {
	withSubs := minimalValidTenant()
	withSubs.Subscriptions = []SubscriptionConfig{
		{Name: "s", Regions: []string{"eastus"}},
	}

	type testCase struct {
		name    string
		tenants []TenantConfig
		want    bool
	}

	testCases := []testCase{
		{name: "no tenants", tenants: nil, want: false},
		{name: "tenant without subs", tenants: []TenantConfig{minimalValidTenant()}, want: false},
		{name: "tenant with subs", tenants: []TenantConfig{withSubs}, want: true},
		{name: "mixed", tenants: []TenantConfig{minimalValidTenant(), withSubs}, want: true},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Tenants: tc.tenants}
			if got := cfg.HasSubscriptions(); got != tc.want {
				t.Fatalf("HasSubscriptions() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTenantConfigGetScope(t *testing.T) {
	type testCase struct {
		name   string
		tenant TenantConfig
		want   string
	}

	testCases := []testCase{
		{
			name:   "custom scope",
			tenant: TenantConfig{Scope: "https://custom.example.com/.default"},
			want:   "https://custom.example.com/.default",
		},
		{
			name:   "default scope",
			tenant: TenantConfig{},
			want:   DefaultScope,
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tenant.GetScope(); got != tc.want {
				t.Fatalf("GetScope() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTenantConfigGetDisplayName(t *testing.T) {
	type testCase struct {
		name   string
		tenant TenantConfig
		want   string
	}

	testCases := []testCase{
		{
			name:   "with name",
			tenant: TenantConfig{TenantID: "tid", TenantName: "My Tenant"},
			want:   "My Tenant",
		},
		{
			name:   "falls back to id",
			tenant: TenantConfig{TenantID: "tid"},
			want:   "tid",
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tenant.GetDisplayName(); got != tc.want {
				t.Fatalf("GetDisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTenantConfigIsDirectoryQuotaEnabled(t *testing.T) {
	type testCase struct {
		name   string
		tenant TenantConfig
		want   bool
	}

	testCases := []testCase{
		{
			name:   "nil defaults to true",
			tenant: TenantConfig{},
			want:   true,
		},
		{
			name:   "explicit true",
			tenant: TenantConfig{DirectoryQuota: boolPtr(true)},
			want:   true,
		},
		{
			name:   "explicit false",
			tenant: TenantConfig{DirectoryQuota: boolPtr(false)},
			want:   false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.tenant.IsDirectoryQuotaEnabled(); got != tc.want {
				t.Fatalf("IsDirectoryQuotaEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A missing scheme is accepted by url.Parse, so without an explicit check it
// only surfaces as a connection error once the collector is already running.
func TestCIJobOutcomesConfigValidatesEndpoints(t *testing.T) {
	valid := func() CIJobOutcomesConfig {
		return CIJobOutcomesConfig{
			Enabled:      true,
			ClusterURI:   "https://hcp-dev-us-2.eastus2.kusto.windows.net",
			IngestionURI: "https://ingest-hcp-dev-us-2.eastus2.kusto.windows.net",
			Database:     "ServiceLogs",
			Outcomes:     KustoTableConfig{Table: "ciJobOutcomes", IngestionMapping: "ciJobOutcomesMapping"},
			TestNames:    KustoTableConfig{Table: "ciTestNames", IngestionMapping: "ciTestNamesMapping"},
			TestResults:  KustoTableConfig{Table: "ciTestResults", IngestionMapping: "ciTestResultsMapping"},
			SippyURI:     "https://sippy.dptools.openshift.org",
			JobFilter:    "e2e-parallel",
			Releases:     []string{"Presubmits"},
		}
	}

	fullySpecified := valid()
	if err := fullySpecified.validate(); err != nil {
		t.Fatalf("a fully specified config must validate, got %v", err)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(*CIJobOutcomesConfig)
	}{
		{
			name:   "cluster URI without a scheme",
			mutate: func(c *CIJobOutcomesConfig) { c.ClusterURI = "hcp-dev-us-2.eastus2.kusto.windows.net" },
		},
		{
			name:   "ingestion URI without a scheme",
			mutate: func(c *CIJobOutcomesConfig) { c.IngestionURI = "ingest-hcp-dev-us-2.eastus2.kusto.windows.net" },
		},
		{
			name:   "sippy URI without a scheme",
			mutate: func(c *CIJobOutcomesConfig) { c.SippyURI = "sippy.dptools.openshift.org" },
		},
		{
			name:   "empty cluster URI",
			mutate: func(c *CIJobOutcomesConfig) { c.ClusterURI = "" },
		},
		{
			name:   "no releases",
			mutate: func(c *CIJobOutcomesConfig) { c.Releases = nil },
		},
		{
			name:   "missing test names table",
			mutate: func(c *CIJobOutcomesConfig) { c.TestNames.Table = "" },
		},
		{
			name:   "missing test results ingestion mapping",
			mutate: func(c *CIJobOutcomesConfig) { c.TestResults.IngestionMapping = "" },
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := valid()
			testCase.mutate(&cfg)
			if err := cfg.validate(); err == nil {
				t.Error("expected validation to fail, got nil")
			}
		})
	}

	// Nothing is reachable while disabled, so an unset config must not block startup.
	disabled := &CIJobOutcomesConfig{Enabled: false}
	if err := disabled.validate(); err != nil {
		t.Errorf("a disabled config must validate, got %v", err)
	}
}

// A pass reads artifacts per run, so it needs a bound of its own: the
// collector-wide timeout is sized for a single API call and would cut off a
// first pass over an empty table.
func TestCIJobOutcomesTimeoutDefaultsIndependently(t *testing.T) {
	cfg := CIJobOutcomesConfig{Enabled: false}
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GetTimeout() != DefaultCIJobOutcomesTimeout {
		t.Errorf("GetTimeout() = %v, want %v", cfg.GetTimeout(), DefaultCIJobOutcomesTimeout)
	}
	if cfg.GetTimeout() <= DefaultTimeout {
		t.Errorf("a pass must be allowed longer than the collector-wide %v, got %v", DefaultTimeout, cfg.GetTimeout())
	}
}
