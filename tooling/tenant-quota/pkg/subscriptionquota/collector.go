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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/Azure/ARO-HCP/tooling/metricscache"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

var (
	metricLabels = []string{"source", "subscription_id", "subscription_name", "region", "quota_name", "localized_name"}

	usageDesc = prometheus.NewDesc(
		"azure_quota_usage",
		"Current usage of an Azure quota",
		metricLabels, nil,
	)
	limitDesc = prometheus.NewDesc(
		"azure_quota_limit",
		"Limit of an Azure quota",
		metricLabels, nil,
	)
)

// Collector collects subscription-level quota metrics from Azure.
// It implements prometheus.Collector so it can be registered on a shared
// registry. Metrics are served from an in-memory cache; Azure API calls
// happen only in the background collection loop.
type Collector struct {
	config       *config.Config
	logger       *slog.Logger
	cache        *metricscache.Cache
	sources      []QuotaSource
	credProvider *credentials.Provider
}

func NewCollector(cfg *config.Config, logger *slog.Logger,
	credProvider *credentials.Provider, cacheTTL time.Duration, sources ...QuotaSource) *Collector {

	if len(sources) == 0 {
		sources = []QuotaSource{
			NewRoleAssignmentSource(cfg.Tenants),
			&ComputeQuotaSource{},
			&NetworkQuotaSource{},
		}
	}

	return &Collector{
		config:       cfg,
		logger:       logger,
		cache:        metricscache.NewCache(cacheTTL),
		sources:      sources,
		credProvider: credProvider,
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- usageDesc
	ch <- limitDesc
}

// Collect implements prometheus.Collector. Reads from cache only.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	for _, m := range c.cache.GetAll() {
		ch <- m
	}
}

// Start runs the background collection loop. It collects immediately on
// startup, then on every interval tick.
func (c *Collector) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	interval := c.config.GetInterval()
	c.logger.Info("Starting subscription quota collection",
		"interval", interval,
		"sources", len(c.sources),
		"cacheTTL", c.config.GetCacheTTL())

	c.collectAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping subscription quota collection")
			return
		case <-ticker.C:
			c.collectAll(ctx)
		}
	}
}

func (c *Collector) collectAll(ctx context.Context) {
	for _, tenant := range c.config.Tenants {
		if len(tenant.Subscriptions) == 0 {
			continue
		}

		cred, err := c.credProvider.GetCredential(tenant)
		if err != nil {
			c.logger.Error("Failed to get credential for subscription quota collection",
				"tenant", tenant.GetDisplayName(),
				"error", err)
			continue
		}

		for _, sub := range tenant.Subscriptions {
			for _, source := range c.sources {
				c.collectSource(ctx, source, cred, sub)
			}
		}
	}
}

func (c *Collector) collectSource(parentCtx context.Context, source QuotaSource,
	cred *azidentity.ClientSecretCredential, sub config.SubscriptionConfig) {

	ctx, cancel := context.WithTimeout(parentCtx, c.config.GetTimeout())
	defer cancel()

	regions := sub.Regions
	if !source.IsRegional() {
		regions = []string{""}
	}

	for _, region := range regions {
		results, errs := source.Collect(ctx, cred, sub.SubscriptionID, region)
		if len(results) == 0 && len(errs) > 0 {
			c.logger.Error("Failed to collect subscription quota",
				"source", source.Name(),
				"subscription", sub.Name,
				"region", region,
				"errorCount", len(errs),
				"error", errors.Join(errs...))
			continue
		}

		for _, r := range results {
			c.cacheResult(source.Name(), sub.Name, r)
		}

		if len(errs) > 0 {
			c.logger.Warn("Collected subscription quota metrics with partial errors",
				"source", source.Name(),
				"subscription", sub.Name,
				"region", region,
				"count", len(results),
				"errorCount", len(errs),
				"error", errors.Join(errs...))
			continue
		}

		c.logger.Info("Collected subscription quota metrics",
			"source", source.Name(),
			"subscription", sub.Name,
			"region", region,
			"count", len(results))
	}
}

func (c *Collector) cacheResult(sourceName string, subscriptionName string, r QuotaResult) {
	labelValues := []string{sourceName, r.SubscriptionID, subscriptionName, r.Region, r.QuotaName, r.LocalizedName}

	usageKey := cacheKey(sourceName, r.SubscriptionID, r.Region, r.QuotaName, "usage")
	usageMetric := prometheus.MustNewConstMetric(usageDesc, prometheus.GaugeValue, r.CurrentValue, labelValues...)
	c.cache.Set(usageKey, usageMetric)

	limitKey := cacheKey(sourceName, r.SubscriptionID, r.Region, r.QuotaName, "limit")
	limitMetric := prometheus.MustNewConstMetric(limitDesc, prometheus.GaugeValue, r.Limit, labelValues...)
	c.cache.Set(limitKey, limitMetric)
}

func cacheKey(source, subscriptionID, region, quotaName, metricType string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", source, subscriptionID, region, quotaName, metricType)
}
