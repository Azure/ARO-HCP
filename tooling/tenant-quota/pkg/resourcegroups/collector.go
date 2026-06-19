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
// subscriptions and exposes a per-resource-group Prometheus gauge with the
// deleteAfter TTL timestamp. Consumers derive expired counts, max staleness,
// etc. via PromQL. Azure API calls happen only in the background collection
// loop; scrapes read from cached metric values.
type Collector struct {
	collectorCfg CollectorConfig
	config       *config.Config
	logger       *slog.Logger
	credProvider *credentials.Provider
	lister       ResourceGroupLister
	expiryDesc   *prometheus.Desc
	mu           sync.RWMutex
	metrics      []prometheus.Metric
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
		lister:       l,
		expiryDesc: prometheus.NewDesc(
			fmt.Sprintf("%s_expiry_timestamp", collectorCfg.MetricPrefix),
			fmt.Sprintf("Unix timestamp of the deleteAfter TTL tag for each %s", collectorCfg.Name),
			[]string{"resource_group", "region", "subscription_id", "subscription_name"}, nil,
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.expiryDesc
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

	var metrics []prometheus.Metric

	ttlKey := c.collectorCfg.TTLTagKey
	for _, rg := range rgs {
		rgName := ""
		if rg.Name != nil {
			rgName = *rg.Name
		}
		region := ""
		if rg.Location != nil {
			region = *rg.Location
		}

		expiryTag := rg.Tags[ttlKey]
		if expiryTag == nil {
			continue
		}

		expiry, err := time.Parse(time.RFC3339, *expiryTag)
		if err != nil {
			c.logger.Warn("Failed to parse TTL tag",
				"resourceGroup", rgName,
				"tagKey", ttlKey,
				"value", *expiryTag,
				"error", err)
			continue
		}

		metrics = append(metrics, prometheus.MustNewConstMetric(
			c.expiryDesc, prometheus.GaugeValue, float64(expiry.Unix()),
			rgName, region, sub.SubscriptionID, sub.Name,
		))
	}

	c.logger.Info("Collected resource group metrics",
		"collector", c.collectorCfg.Name,
		"subscription", sub.Name,
		"totalRGs", len(rgs),
		"rgsWithTTL", len(metrics))

	return metrics
}
