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

package resourcegroups

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

type fakeLister struct {
	rgs map[string][]*armresources.ResourceGroup
	err error
}

func (f *fakeLister) ListResourceGroups(_ context.Context, subscriptionID string) ([]*armresources.ResourceGroup, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rgs[subscriptionID], nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(subs ...config.SubscriptionConfig) *config.Config {
	cfg := &config.Config{
		Interval: "1m",
		Timeout:  "10s",
		Tenants: []config.TenantConfig{
			{
				TenantID:                 "test-tenant",
				ServicePrincipalClientId: "test-sp",
				KeyVaultSecretName:       "test-secret",
				Subscriptions:            subs,
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		panic(fmt.Sprintf("invalid test config: %v", err))
	}
	return cfg
}

func makeRG(name, location string, tags map[string]*string) *armresources.ResourceGroup {
	return &armresources.ResourceGroup{
		Name:     to.Ptr(name),
		Location: to.Ptr(location),
		Tags:     tags,
	}
}

func collectMetrics(c *Collector) []prometheus.Metric {
	ch := make(chan prometheus.Metric)
	go func() {
		c.Collect(ch)
		close(ch)
	}()
	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	return metrics
}

func TestCollectSubscription_ExpiredRGs(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	twoHoursAgo := now.Add(-2 * time.Hour).Format(time.RFC3339)
	oneHourAgo := now.Add(-1 * time.Hour).Format(time.RFC3339)
	inFuture := now.Add(2 * time.Hour).Format(time.RFC3339)

	ttlKey := E2ECollectorConfig.TTLTagKey
	subCfg := config.SubscriptionConfig{
		Name:           "sub-a",
		SubscriptionID: "sub-id",
		Regions:        []string{"eastus"},
	}

	type testCase struct {
		name             string
		rgs              []*armresources.ResourceGroup
		wantActive       float64
		wantExpiredTotal int
		wantMaxAgeGT     float64
		wantMaxAgeLT     float64
	}

	testCases := []testCase{
		{
			name:             "no resource groups produces only active=0",
			rgs:              nil,
			wantActive:       0,
			wantExpiredTotal: 0,
		},
		{
			name: "all non-expired RGs produce zero expired",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-active-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(inFuture),
				}),
			},
			wantActive:       1,
			wantExpiredTotal: 0,
		},
		{
			name: "expired RGs are counted",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-expired-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(twoHoursAgo),
				}),
				makeRG("rg-expired-2", "eastus", map[string]*string{
					ttlKey: to.Ptr(oneHourAgo),
				}),
				makeRG("rg-active", "eastus", map[string]*string{
					ttlKey: to.Ptr(inFuture),
				}),
			},
			wantActive:       3,
			wantExpiredTotal: 2,
			wantMaxAgeGT:     7100,
			wantMaxAgeLT:     7300,
		},
		{
			name: "RGs missing TTL tag are not counted as expired",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-no-tag", "westus", map[string]*string{}),
			},
			wantActive:       1,
			wantExpiredTotal: 0,
		},
		{
			name: "RGs with unparseable TTL tag are not counted",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-bad-tag", "westus", map[string]*string{
					ttlKey: to.Ptr("not-a-timestamp"),
				}),
			},
			wantActive:       1,
			wantExpiredTotal: 0,
		},
		{
			name: "expired RGs across multiple regions tracked separately",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-east-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(twoHoursAgo),
				}),
				makeRG("rg-west-1", "westus", map[string]*string{
					ttlKey: to.Ptr(oneHourAgo),
				}),
			},
			wantActive:       2,
			wantExpiredTotal: 2,
		},
	}

	prefix := E2ECollectorConfig.MetricPrefix
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeLister{
				rgs: map[string][]*armresources.ResourceGroup{
					"sub-id": tc.rgs,
				},
			}

			cfg := testConfig(subCfg)
			c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil, lister)
			c.now = func() time.Time { return now }
			c.collectAll(context.Background())

			metrics := collectMetrics(c)

			var activeCount float64
			expiredCount := 0
			var maxAge float64

			for _, m := range metrics {
				metric := &dto.Metric{}
				if err := m.Write(metric); err != nil {
					t.Fatalf("write metric: %v", err)
				}

				desc := m.Desc().String()
				switch {
				case strings.Contains(desc, prefix+"_expired_max_age_seconds"):
					if v := metric.GetGauge().GetValue(); v > maxAge {
						maxAge = v
					}
				case strings.Contains(desc, prefix+"_expired"):
					expiredCount += int(metric.GetGauge().GetValue())
				case strings.Contains(desc, prefix+"_active"):
					activeCount = metric.GetGauge().GetValue()
				}
			}

			if activeCount != tc.wantActive {
				t.Errorf("active = %v, want %v", activeCount, tc.wantActive)
			}
			if expiredCount != tc.wantExpiredTotal {
				t.Errorf("expired = %d, want %d", expiredCount, tc.wantExpiredTotal)
			}
			if tc.wantMaxAgeGT > 0 && maxAge < tc.wantMaxAgeGT {
				t.Errorf("max age = %v, want > %v", maxAge, tc.wantMaxAgeGT)
			}
			if tc.wantMaxAgeLT > 0 && maxAge > tc.wantMaxAgeLT {
				t.Errorf("max age = %v, want < %v", maxAge, tc.wantMaxAgeLT)
			}
		})
	}
}

func TestCollectAll_ListError(t *testing.T) {
	subCfg := config.SubscriptionConfig{
		Name:           "sub-a",
		SubscriptionID: "sub-id",
		Regions:        []string{"eastus"},
	}
	lister := &fakeLister{err: fmt.Errorf("azure API error")}
	cfg := testConfig(subCfg)

	c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil, lister)
	c.collectAll(context.Background())

	metrics := collectMetrics(c)
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics on error, got %d", len(metrics))
	}
}

func TestCollectAll_NoRGs(t *testing.T) {
	subCfg := config.SubscriptionConfig{
		Name:           "sub-a",
		SubscriptionID: "sub-id",
		Regions:        []string{"eastus"},
	}
	lister := &fakeLister{rgs: map[string][]*armresources.ResourceGroup{}}
	cfg := testConfig(subCfg)

	c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil, lister)
	c.collectAll(context.Background())

	metrics := collectMetrics(c)
	if len(metrics) != 1 {
		t.Errorf("expected 1 metric (active=0), got %d", len(metrics))
	}
}

func TestCollectAll_SkipsTenantWithoutSubscriptions(t *testing.T) {
	cfg := &config.Config{
		Interval: "1m",
		Timeout:  "10s",
		Tenants: []config.TenantConfig{
			{
				TenantID:                 "test-tenant",
				ServicePrincipalClientId: "test-sp",
				KeyVaultSecretName:       "test-secret",
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid config: %v", err)
	}

	lister := &fakeLister{rgs: map[string][]*armresources.ResourceGroup{}}
	c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil, lister)
	c.collectAll(context.Background())

	metrics := collectMetrics(c)
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics for tenant without subs, got %d", len(metrics))
	}
}

func TestDescribe(t *testing.T) {
	subCfg := config.SubscriptionConfig{
		Name:           "sub-a",
		SubscriptionID: "sub-id",
		Regions:        []string{"eastus"},
	}
	cfg := testConfig(subCfg)
	c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil)

	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}

	if len(descs) != 3 {
		t.Fatalf("expected 3 descriptors, got %d", len(descs))
	}
}
