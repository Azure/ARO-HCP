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

package tenantquota

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
)

// Collector manages tenant quota collection and Prometheus metrics.
// It collects quota data from multiple tenants and exposes metrics with tenant labels.
type Collector struct {
	config   *config.Config
	logger   *slog.Logger
	registry *prometheus.Registry

	// Prometheus metrics
	usagePercentage   *prometheus.GaugeVec
	quotaTotal        *prometheus.GaugeVec
	quotaUsed         *prometheus.GaugeVec
	remainingCapacity *prometheus.GaugeVec

	mutex sync.RWMutex
}

// NewCollector creates a new tenant quota collector.
func NewCollector(cfg *config.Config, logger *slog.Logger) *Collector {
	c := &Collector{
		config:   cfg,
		logger:   logger,
		registry: prometheus.NewRegistry(),
	}

	// Register Prometheus metrics with tenant labels
	c.usagePercentage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_quota_usage_percentage",
			Help: "Tenant quota usage percentage",
		},
		[]string{"tenant_id", "tenant_name"},
	)

	c.quotaTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_quota_total",
			Help: "Total tenant quota limit",
		},
		[]string{"tenant_id", "tenant_name"},
	)

	c.quotaUsed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_quota_used",
			Help: "Used tenant quota from API",
		},
		[]string{"tenant_id", "tenant_name"},
	)

	c.remainingCapacity = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "tenant_remaining_capacity",
			Help: "Remaining tenant capacity",
		},
		[]string{"tenant_id", "tenant_name"},
	)

	// Register all metrics
	c.registry.MustRegister(
		c.usagePercentage,
		c.quotaTotal,
		c.quotaUsed,
		c.remainingCapacity,
	)

	return c
}

// Start begins collecting tenant quota data at the configured interval.
func (c *Collector) Start(ctx context.Context) {
	interval, err := time.ParseDuration(c.config.Interval)
	if err != nil {
		c.logger.Error("Invalid interval", "error", err, "interval", c.config.Interval)
		return
	}

	timeout, err := time.ParseDuration(c.config.Timeout)
	if err != nil {
		c.logger.Warn("Invalid timeout, using default 30s", "error", err, "timeout", c.config.Timeout)
		timeout = 30 * time.Second
	}

	c.logger.Info("Starting tenant quota collection", "interval", interval, "timeout", timeout, "tenant_count", len(c.config.Tenants))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Collect immediately on start
	c.collect(ctx, timeout)

	// Then collect at intervals
	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping tenant quota collection")
			return
		case <-ticker.C:
			c.collect(ctx, timeout)
		}
	}
}

// collect performs quota collection for all configured tenants.
func (c *Collector) collect(ctx context.Context, timeout time.Duration) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Collect quota for each configured tenant
	for _, tenant := range c.config.Tenants {
		c.collectForTenant(collectCtx, tenant)
	}
}

// collectForTenant collects quota data for a single tenant and updates metrics.
func (c *Collector) collectForTenant(ctx context.Context, tenant config.TenantConfig) {
	c.logger.Debug("Collecting quota for tenant", "tenant_id", tenant.TenantID, "tenant_name", tenant.TenantName)

	quotaData, err := CollectQuota(ctx, tenant)
	if err != nil {
		c.logger.Error("Failed to collect quota for tenant", "tenant_id", tenant.TenantID, "tenant_name", tenant.TenantName, "error", err)
		return
	}

	// Update Prometheus metrics with tenant labels
	c.mutex.Lock()
	defer c.mutex.Unlock()

	labels := prometheus.Labels{
		"tenant_id":   quotaData.TenantID,
		"tenant_name": quotaData.TenantName,
	}

	c.usagePercentage.With(labels).Set(float64(quotaData.UsagePercentage))
	c.quotaTotal.With(labels).Set(float64(quotaData.QuotaTotal))
	c.quotaUsed.With(labels).Set(float64(quotaData.QuotaUsed))
	c.remainingCapacity.With(labels).Set(float64(quotaData.RemainingCapacity))

	c.logger.Info("Updated quota metrics for tenant",
		"tenant_id", quotaData.TenantID,
		"tenant_name", quotaData.TenantName,
		"usage_percentage", quotaData.UsagePercentage,
		"quota_total", quotaData.QuotaTotal,
		"quota_used", quotaData.QuotaUsed,
		"remaining_capacity", quotaData.RemainingCapacity,
	)
}

// Gatherer returns the Prometheus gatherer for metrics scraping.
func (c *Collector) Gatherer() prometheus.Gatherer {
	return c.registry
}
