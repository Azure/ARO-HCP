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

package tenantquota

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
)

type Collector struct {
	config *config.Config
	logger *slog.Logger
	client *QuotaClient

	usagePercentage   *prometheus.GaugeVec
	quotaTotal        *prometheus.GaugeVec
	quotaUsed         *prometheus.GaugeVec
	remainingCapacity *prometheus.GaugeVec
}

func NewCollector(cfg *config.Config, logger *slog.Logger, credProvider *credentials.Provider) *Collector {
	labels := []string{"tenant_id", "tenant_name"}

	return &Collector{
		config: cfg,
		logger: logger,
		client: NewQuotaClient(cfg.GetTimeout(), logger, credProvider),
		usagePercentage: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tenant_quota_usage_percentage",
			Help: "Tenant quota usage percentage (0-100)",
		}, labels),
		quotaTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tenant_quota_total",
			Help: "Total tenant quota limit",
		}, labels),
		quotaUsed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tenant_quota_used",
			Help: "Current tenant quota usage",
		}, labels),
		remainingCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "tenant_remaining_capacity",
			Help: "Remaining tenant capacity",
		}, labels),
	}
}

func (c *Collector) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	interval := c.config.GetInterval()
	c.logger.Info("Starting quota collection",
		"interval", interval,
		"timeout", c.config.GetTimeout(),
		"tenants", len(c.config.Tenants))

	c.collectAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping quota collection")
			return
		case <-ticker.C:
			c.collectAll(ctx)
		}
	}
}

func (c *Collector) collectAll(ctx context.Context) {
	for _, tenant := range c.config.Tenants {
		if !tenant.IsDirectoryQuotaEnabled() {
			continue
		}
		c.collectForTenant(ctx, tenant)
	}
}

func (c *Collector) collectForTenant(ctx context.Context, tenant config.TenantConfig) {
	data, err := c.client.GetQuota(ctx, tenant)
	if err != nil {
		c.logger.Error("Failed to collect quota",
			"tenant", tenant.GetDisplayName(),
			"error", err)
		return
	}

	labels := prometheus.Labels{
		"tenant_id":   data.TenantID,
		"tenant_name": data.TenantName,
	}

	c.usagePercentage.With(labels).Set(float64(data.UsagePercentage))
	c.quotaTotal.With(labels).Set(float64(data.QuotaTotal))
	c.quotaUsed.With(labels).Set(float64(data.QuotaUsed))
	c.remainingCapacity.With(labels).Set(float64(data.RemainingCapacity))

	c.logger.Info("Updated quota metrics",
		"tenant", data.TenantName,
		"usage", data.UsagePercentage,
		"used", data.QuotaUsed,
		"total", data.QuotaTotal)
}

// GaugeCollectors returns all gauge collectors for registration on a shared
// prometheus.Registry.
func (c *Collector) GaugeCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		c.usagePercentage,
		c.quotaTotal,
		c.quotaUsed,
		c.remainingCapacity,
	}
}
