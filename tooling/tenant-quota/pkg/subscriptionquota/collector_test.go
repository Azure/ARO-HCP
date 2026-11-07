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

package subscriptionquota

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

type fakeQuotaSource struct {
	name      string
	regional  bool
	collectFn func(ctx context.Context, cred *azidentity.ClientSecretCredential, subscriptionID, region string) ([]QuotaResult, []error)

	callCount  int
	lastRegion string
	lastCtx    context.Context
}

func (f *fakeQuotaSource) Name() string     { return f.name }
func (f *fakeQuotaSource) IsRegional() bool { return f.regional }
func (f *fakeQuotaSource) Collect(ctx context.Context, cred *azidentity.ClientSecretCredential, subscriptionID, region string) ([]QuotaResult, []error) {
	f.callCount++
	f.lastRegion = region
	f.lastCtx = ctx
	return f.collectFn(ctx, cred, subscriptionID, region)
}

func setupTestCredentials(t *testing.T, secretNames ...string) {
	t.Helper()
	secretsDir := t.TempDir()
	t.Setenv("SECRETS_STORE_PATH", secretsDir)
	if err := os.WriteFile(filepath.Join(secretsDir, "cid"), []byte("11111111-1111-1111-1111-111111111111"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range secretNames {
		if err := os.WriteFile(filepath.Join(secretsDir, name), []byte("fake-secret"), 0o644); err != nil {
			t.Fatalf("write secret file: %v", err)
		}
	}
}

func testConfig(tenants ...config.TenantConfig) *config.Config {
	cfg := &config.Config{
		Interval: "1m",
		Timeout:  "10s",
		CacheTTL: "1h",
		Tenants:  tenants,
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Sprintf("invalid test config: %v", err))
	}
	return cfg
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func collectMetricCount(c *Collector) int {
	ch := make(chan prometheus.Metric, 64)
	c.Collect(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	return count
}

func TestNewCollector(t *testing.T) {
	type testCase struct {
		name            string
		sources         []QuotaSource
		wantSourceCount int
		wantSourceNames []string
	}

	testCases := []testCase{
		{
			name:            "uses injected sources",
			sources:         []QuotaSource{&fakeQuotaSource{name: "injected"}},
			wantSourceCount: 1,
			wantSourceNames: []string{"injected"},
		},
		{
			name:            "defaults to rbac, compute, network when no sources provided",
			sources:         nil,
			wantSourceCount: 3,
			wantSourceNames: []string{"rbac", "compute", "network"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(config.TenantConfig{
				TenantID:                       "tid",
				ClientIDKeyVaultSecretName:     "cid",
				ClientSecretKeyVaultSecretName: "secret",
			})

			var collector *Collector
			if tc.sources != nil {
				collector = NewCollector(cfg, testLogger(), nil, time.Hour, tc.sources...)
			} else {
				collector = NewCollector(cfg, testLogger(), nil, time.Hour)
			}

			if len(collector.sources) != tc.wantSourceCount {
				t.Fatalf("sources count = %d, want %d", len(collector.sources), tc.wantSourceCount)
			}
			for i, want := range tc.wantSourceNames {
				if got := collector.sources[i].Name(); got != want {
					t.Fatalf("source[%d] name = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestCollectAll(t *testing.T) {
	type testCase struct {
		name            string
		subscriptions   []config.SubscriptionConfig
		source          *fakeQuotaSource
		timeout         string
		wantMetricCount int
		wantCallCount   int
		wantLastRegion  string
		wantDeadline    bool
	}

	successResult := func(_ context.Context, _ *azidentity.ClientSecretCredential, subID, region string) ([]QuotaResult, []error) {
		return []QuotaResult{{
			QuotaName:      "cores",
			LocalizedName:  "Total Cores",
			CurrentValue:   10,
			Limit:          100,
			SubscriptionID: subID,
			Region:         region,
		}}, nil
	}

	testCases := []testCase{
		{
			name: "regional source caches usage and limit metrics",
			subscriptions: []config.SubscriptionConfig{
				{Name: "sub-a", SubscriptionID: "sub-a-id", Regions: []string{"eastus"}},
			},
			source: &fakeQuotaSource{
				name:      "test",
				regional:  true,
				collectFn: successResult,
			},
			wantMetricCount: 2,
			wantCallCount:   1,
			wantLastRegion:  "eastus",
		},
		{
			name: "non-regional source called once with empty region",
			subscriptions: []config.SubscriptionConfig{
				{Name: "sub-a", SubscriptionID: "sub-a-id", Regions: []string{"eastus", "westus"}},
			},
			source: &fakeQuotaSource{
				name:      "global",
				regional:  false,
				collectFn: successResult,
			},
			wantMetricCount: 2,
			wantCallCount:   1,
			wantLastRegion:  "",
		},
		{
			name:          "skips tenant without subscriptions",
			subscriptions: nil,
			source: &fakeQuotaSource{
				name:     "test",
				regional: true,
				collectFn: func(_ context.Context, _ *azidentity.ClientSecretCredential, _, _ string) ([]QuotaResult, []error) {
					return nil, nil
				},
			},
			wantMetricCount: 0,
			wantCallCount:   0,
		},
		{
			name: "total failure produces no metrics",
			subscriptions: []config.SubscriptionConfig{
				{Name: "sub-a", SubscriptionID: "sub-a-id", Regions: []string{"eastus"}},
			},
			source: &fakeQuotaSource{
				name:     "failing",
				regional: true,
				collectFn: func(_ context.Context, _ *azidentity.ClientSecretCredential, _, _ string) ([]QuotaResult, []error) {
					return nil, []error{fmt.Errorf("api error")}
				},
			},
			wantMetricCount: 0,
			wantCallCount:   1,
			wantLastRegion:  "eastus",
		},
		{
			name: "partial errors still cache successful results",
			subscriptions: []config.SubscriptionConfig{
				{Name: "sub-a", SubscriptionID: "sub-a-id", Regions: []string{"eastus"}},
			},
			source: &fakeQuotaSource{
				name:     "partial",
				regional: true,
				collectFn: func(_ context.Context, _ *azidentity.ClientSecretCredential, subID, region string) ([]QuotaResult, []error) {
					return []QuotaResult{{
						QuotaName:      "cores",
						LocalizedName:  "Total Cores",
						CurrentValue:   5,
						Limit:          50,
						SubscriptionID: subID,
						Region:         region,
					}}, []error{fmt.Errorf("partial page error")}
				},
			},
			wantMetricCount: 2,
			wantCallCount:   1,
			wantLastRegion:  "eastus",
		},
		{
			name: "source receives context with deadline from configured timeout",
			subscriptions: []config.SubscriptionConfig{
				{Name: "sub-a", SubscriptionID: "sub-a-id", Regions: []string{"eastus"}},
			},
			source: &fakeQuotaSource{
				name:     "slow",
				regional: true,
				collectFn: func(_ context.Context, _ *azidentity.ClientSecretCredential, _, _ string) ([]QuotaResult, []error) {
					return nil, nil
				},
			},
			timeout:         "50ms",
			wantMetricCount: 0,
			wantCallCount:   1,
			wantLastRegion:  "eastus",
			wantDeadline:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			setupTestCredentials(t, "test-secret")

			tenant := config.TenantConfig{
				TenantID:                       "tid",
				ClientIDKeyVaultSecretName:     "cid",
				ClientSecretKeyVaultSecretName: "test-secret",
				Subscriptions:                  tc.subscriptions,
			}
			cfg := testConfig(tenant)
			if tc.timeout != "" {
				cfg.Timeout = tc.timeout
				if err := cfg.Validate(); err != nil {
					t.Fatalf("validate config: %v", err)
				}
			}

			credProvider := credentials.NewProvider(testLogger())
			collector := NewCollector(cfg, testLogger(), credProvider, time.Hour, tc.source)

			collector.collectAll(context.Background())

			if got := collectMetricCount(collector); got != tc.wantMetricCount {
				t.Fatalf("metric count = %d, want %d", got, tc.wantMetricCount)
			}
			if tc.source.callCount != tc.wantCallCount {
				t.Fatalf("source call count = %d, want %d", tc.source.callCount, tc.wantCallCount)
			}
			if tc.wantCallCount > 0 && tc.source.lastRegion != tc.wantLastRegion {
				t.Fatalf("source last region = %q, want %q", tc.source.lastRegion, tc.wantLastRegion)
			}
			if tc.wantDeadline {
				if tc.source.lastCtx == nil {
					t.Fatal("source was never called")
				}
				if _, ok := tc.source.lastCtx.Deadline(); !ok {
					t.Fatal("expected context to have a deadline")
				}
			}
		})
	}
}
