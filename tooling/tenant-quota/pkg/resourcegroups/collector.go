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
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

// Collector tracks resource groups matching a tag filter across configured
// subscriptions and exposes Prometheus metrics for TTL violations. Azure API
// calls happen only in the background collection loop; scrapes read from
// cached metric values.
type Collector struct {
	collectorCfg CollectorConfig
	config       *config.Config
	logger       *slog.Logger
	credProvider *credentials.Provider
	lister       ResourceGroupLister
	expiredDesc  *prometheus.Desc
	maxAgeDesc   *prometheus.Desc
	activeDesc   *prometheus.Desc
	mu           sync.RWMutex
	metrics      []prometheus.Metric
	now          func() time.Time
}

func NewCollector(collectorCfg CollectorConfig, cfg *config.Config, logger *slog.Logger, credProvider *credentials.Provider, lister ...ResourceGroupLister) *Collector {
	var l ResourceGroupLister
	if len(lister) > 0 {
		l = lister[0]
	}
	return &Collector{
		collectorCfg: collectorCfg,
		config:       cfg,
		logger:       logger,
		credProvider: credProvider,
		now:          time.Now,
		lister:       l,
		expiredDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_expired", collectorCfg.MetricPrefix),
			fmt.Sprintf("Count of expired %s past their TTL", collectorCfg.Name),
			[]string{"subscription_id", "subscription_name", "region"}, nil,
		),
		maxAgeDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_expired_max_age_seconds", collectorCfg.MetricPrefix),
			fmt.Sprintf("Seconds since the oldest expired %s TTL expired", collectorCfg.Name),
			[]string{"subscription_id", "subscription_name", "region"}, nil,
		),
		activeDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_active", collectorCfg.MetricPrefix),
			fmt.Sprintf("Total tagged %s matching the filter in the subscription", collectorCfg.Name),
			[]string{"subscription_id", "subscription_name"}, nil,
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.expiredDesc
	ch <- c.maxAgeDesc
	ch <- c.activeDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.RLock()
	metrics := c.metrics
	c.mu.RUnlock()
	for _, m := range metrics {
		ch <- m
	}
}

// Start runs the background collection loop. It collects immediately on
// startup, then on every interval tick.
func (c *Collector) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	interval := c.config.GetInterval()
	c.logger.Info("Starting resource group collection",
		"collector", c.collectorCfg.Name,
		"interval", interval)

	c.collectAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping resource group collection", "collector", c.collectorCfg.Name)
			return
		case <-ticker.C:
			c.collectAll(ctx)
		}
	}
}

func (c *Collector) collectAll(ctx context.Context) {
	var metrics []prometheus.Metric

	for _, tenant := range c.config.Tenants {
		if len(tenant.Subscriptions) == 0 {
			continue
		}

		var lister ResourceGroupLister
		if c.lister != nil {
			lister = c.lister
		} else {
			cred, err := c.credProvider.GetCredential(tenant)
			if err != nil {
				c.logger.Error("Failed to get credential for tenant",
					"tenant", tenant.GetDisplayName(),
					"error", err)
				continue
			}
			lister = &AzureResourceGroupLister{cred: cred, filter: c.collectorCfg.TagFilter}
		}

		for _, sub := range tenant.Subscriptions {
			subMetrics := c.collectSubscription(ctx, lister, sub)
			metrics = append(metrics, subMetrics...)
		}
	}

	c.mu.Lock()
	c.metrics = metrics
	c.mu.Unlock()
}

func (c *Collector) collectSubscription(ctx context.Context, lister ResourceGroupLister, sub config.SubscriptionConfig) []prometheus.Metric {
	collectCtx, cancel := context.WithTimeout(ctx, c.config.GetTimeout())
	defer cancel()

	rgs, err := lister.ListResourceGroups(collectCtx, sub.SubscriptionID)
	if err != nil {
		c.logger.Error("Failed to list resource groups",
			"collector", c.collectorCfg.Name,
			"subscription", sub.Name,
			"error", err)
		// Return nil so this subscription contributes no metrics to the
		// current cycle. The full metrics slice is replaced atomically in
		// collectAll, so the subscription's gauges disappear until the next
		// successful collection rather than persisting stale values.
		return nil
	}

	now := c.now()
	type regionStats struct {
		expired int
		maxAge  time.Duration
	}
	byRegion := make(map[string]*regionStats)

	ttlKey := c.collectorCfg.TTLTagKey
	for _, rg := range rgs {
		region := ""
		if rg.Location != nil {
			region = *rg.Location
		}
		if byRegion[region] == nil {
			byRegion[region] = &regionStats{}
		}

		expiryTag := rg.Tags[ttlKey]
		if expiryTag == nil {
			continue
		}
		expiry, err := time.Parse(time.RFC3339, *expiryTag)
		if err != nil {
			rgName := ""
			if rg.Name != nil {
				rgName = *rg.Name
			}
			c.logger.Warn("Failed to parse TTL tag",
				"resourceGroup", rgName,
				"tagKey", ttlKey,
				"value", *expiryTag,
				"error", err)
			continue
		}
		if !expiry.After(now) {
			stats := byRegion[region]
			stats.expired++
			age := now.Sub(expiry)
			if age > stats.maxAge {
				stats.maxAge = age
			}
		}
	}

	var metrics []prometheus.Metric

	metrics = append(metrics, prometheus.MustNewConstMetric(
		c.activeDesc, prometheus.GaugeValue, float64(len(rgs)),
		sub.SubscriptionID, sub.Name,
	))

	for region, stats := range byRegion {
		if stats.expired == 0 {
			continue
		}
		metrics = append(metrics, prometheus.MustNewConstMetric(
			c.expiredDesc, prometheus.GaugeValue, float64(stats.expired),
			sub.SubscriptionID, sub.Name, region,
		))
		metrics = append(metrics, prometheus.MustNewConstMetric(
			c.maxAgeDesc, prometheus.GaugeValue, stats.maxAge.Seconds(),
			sub.SubscriptionID, sub.Name, region,
		))
	}

	expiredRegions := 0
	for _, stats := range byRegion {
		if stats.expired > 0 {
			expiredRegions++
		}
	}

	c.logger.Info("Collected resource group metrics",
		"collector", c.collectorCfg.Name,
		"subscription", sub.Name,
		"totalRGs", len(rgs),
		"regionsWithExpired", expiredRegions)

	return metrics
}
