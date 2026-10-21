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

type metricInfo struct {
	resourceGroup    string
	region           string
	subscriptionID   string
	subscriptionName string
	timestamp        float64
}

func parseMetric(t *testing.T, m prometheus.Metric) metricInfo {
	t.Helper()
	metric := &dto.Metric{}
	if err := m.Write(metric); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	info := metricInfo{timestamp: metric.GetGauge().GetValue()}
	for _, lp := range metric.GetLabel() {
		switch lp.GetName() {
		case "resource_group":
			info.resourceGroup = lp.GetValue()
		case "region":
			info.region = lp.GetValue()
		case "subscription_id":
			info.subscriptionID = lp.GetValue()
		case "subscription_name":
			info.subscriptionName = lp.GetValue()
		}
	}
	return info
}

func TestCollectSubscription_PerRGTimestamps(t *testing.T) {
	twoHoursAgo := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	oneHourAgo := time.Date(2026, 6, 8, 11, 0, 0, 0, time.UTC)
	inFuture := time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)

	ttlKey := E2ECollectorConfig.TTLTagKey
	subCfg := config.SubscriptionConfig{
		Name:           "sub-a",
		SubscriptionID: "sub-id",
		Regions:        []string{"eastus"},
	}

	type expectedMetric struct {
		resourceGroup string
		region        string
		timestamp     float64
	}

	testCases := []struct {
		name        string
		rgs         []*armresources.ResourceGroup
		wantMetrics []expectedMetric
	}{
		{
			name:        "no resource groups produces no metrics",
			rgs:         nil,
			wantMetrics: nil,
		},
		{
			name: "RG with valid TTL tag produces one metric",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-active-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(inFuture.Format(time.RFC3339)),
				}),
			},
			wantMetrics: []expectedMetric{
				{resourceGroup: "rg-active-1", region: "eastus", timestamp: float64(inFuture.Unix())},
			},
		},
		{
			name: "expired and active RGs each produce one metric",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-expired-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(twoHoursAgo.Format(time.RFC3339)),
				}),
				makeRG("rg-expired-2", "eastus", map[string]*string{
					ttlKey: to.Ptr(oneHourAgo.Format(time.RFC3339)),
				}),
				makeRG("rg-active", "eastus", map[string]*string{
					ttlKey: to.Ptr(inFuture.Format(time.RFC3339)),
				}),
			},
			wantMetrics: []expectedMetric{
				{resourceGroup: "rg-expired-1", region: "eastus", timestamp: float64(twoHoursAgo.Unix())},
				{resourceGroup: "rg-expired-2", region: "eastus", timestamp: float64(oneHourAgo.Unix())},
				{resourceGroup: "rg-active", region: "eastus", timestamp: float64(inFuture.Unix())},
			},
		},
		{
			name: "RGs missing TTL tag produce no metric",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-no-tag", "westus", map[string]*string{}),
			},
			wantMetrics: nil,
		},
		{
			name: "RGs with unparseable TTL tag produce no metric",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-bad-tag", "westus", map[string]*string{
					ttlKey: to.Ptr("not-a-timestamp"),
				}),
			},
			wantMetrics: nil,
		},
		{
			name: "RGs across multiple regions each get their own time series",
			rgs: []*armresources.ResourceGroup{
				makeRG("rg-east-1", "eastus", map[string]*string{
					ttlKey: to.Ptr(twoHoursAgo.Format(time.RFC3339)),
				}),
				makeRG("rg-west-1", "westus", map[string]*string{
					ttlKey: to.Ptr(oneHourAgo.Format(time.RFC3339)),
				}),
			},
			wantMetrics: []expectedMetric{
				{resourceGroup: "rg-east-1", region: "eastus", timestamp: float64(twoHoursAgo.Unix())},
				{resourceGroup: "rg-west-1", region: "westus", timestamp: float64(oneHourAgo.Unix())},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeLister{
				rgs: map[string][]*armresources.ResourceGroup{
					"sub-id": tc.rgs,
				},
			}

			cfg := testConfig(subCfg)
			c := NewCollector(E2ECollectorConfig, cfg, testLogger(), nil, lister)
			c.collectAll(context.Background())

			metrics := collectMetrics(c)

			if len(metrics) != len(tc.wantMetrics) {
				t.Fatalf("got %d metrics, want %d", len(metrics), len(tc.wantMetrics))
			}

			actual := make(map[string]metricInfo)
			for _, m := range metrics {
				info := parseMetric(t, m)
				actual[info.resourceGroup] = info
			}

			for _, want := range tc.wantMetrics {
				got, ok := actual[want.resourceGroup]
				if !ok {
					t.Errorf("missing metric for resource_group=%s", want.resourceGroup)
					continue
				}
				if got.region != want.region {
					t.Errorf("resource_group=%s: region=%s, want %s", want.resourceGroup, got.region, want.region)
				}
				if got.timestamp != want.timestamp {
					t.Errorf("resource_group=%s: timestamp=%v, want %v", want.resourceGroup, got.timestamp, want.timestamp)
				}
				if got.subscriptionID != "sub-id" {
					t.Errorf("resource_group=%s: subscription_id=%s, want sub-id", want.resourceGroup, got.subscriptionID)
				}
				if got.subscriptionName != "sub-a" {
					t.Errorf("resource_group=%s: subscription_name=%s, want sub-a", want.resourceGroup, got.subscriptionName)
				}
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
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
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

	if len(descs) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(descs))
	}
}
