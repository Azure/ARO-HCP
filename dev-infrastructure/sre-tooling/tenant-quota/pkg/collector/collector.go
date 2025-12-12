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

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/config"
)

// Collector is the orchestrator that manages all collector functions.
// It reads configuration, schedules collector function execution, parses their output,
// and updates Prometheus metrics. Individual collector functions (e.g., tenant-quota)
// are registered separately and executed by this orchestrator.
type Collector struct {
	config           *config.Config
	metrics          map[string]prometheus.Collector
	mutex            sync.RWMutex
	logger           *slog.Logger
	metricTimestamps map[string]time.Time
	registry         *prometheus.Registry
}

const (
	metricTypeGauge   = "gauge"
	metricTypeCounter = "counter"
)

func NewCollector(cfg *config.Config, logger *slog.Logger) *Collector {
	c := &Collector{
		config:           cfg,
		metrics:          make(map[string]prometheus.Collector),
		logger:           logger,
		metricTimestamps: make(map[string]time.Time),
		registry:         prometheus.NewRegistry(),
	}

	// Dynamically register Prometheus metrics from the YAML configuration.
	// Each metric defined in the config (name, type, labels) is created and
	// registered with Prometheus so they can be scraped.
	c.registerMetrics()

	return c
}

// registerMetrics creates and registers Prometheus metrics based on the YAML configuration.
func (c *Collector) registerMetrics() {
	for _, collector := range c.config.Collectors {
		for _, metric := range collector.Metrics {
			switch metric.Type {
			case metricTypeGauge:
				gauge := prometheus.NewGaugeVec(
					prometheus.GaugeOpts{
						Name: metric.Name,
						Help: metric.Help,
					},
					metric.Labels,
				)
				c.registry.MustRegister(gauge)
				c.metrics[metric.Name] = gauge
				c.logger.Debug("Registered gauge metric", "metric", metric.Name)

			case metricTypeCounter:
				counter := prometheus.NewCounterVec(
					prometheus.CounterOpts{
						Name: metric.Name,
						Help: metric.Help,
					},
					metric.Labels,
				)
				c.registry.MustRegister(counter)
				c.metrics[metric.Name] = counter
				c.logger.Debug("Registered counter metric", "metric", metric.Name)
			}
		}
	}
}

func (c *Collector) Start(ctx context.Context) {
	tickers := make(map[string]*time.Ticker)

	// Start metric cleanup goroutine
	go c.cleanupExpiredMetrics(ctx)

	// Start tickers for each collector
	for _, collector := range c.config.Collectors {
		interval, err := time.ParseDuration(collector.Interval)
		if err != nil {
			c.logger.Error("Invalid interval for collector", "collector", collector.Name, "error", err)
			continue
		}

		ticker := time.NewTicker(interval)
		tickers[collector.Name] = ticker

		c.logger.Info("Starting collector", "collector", collector.Name, "interval", collector.Interval)

		go func(col config.Collector) {
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					c.logger.Info("Stopping collector", "collector", col.Name)
					return
				case <-ticker.C:
					c.executeCollector(ctx, col)
				}
			}
		}(collector)
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Stop all tickers
	for _, ticker := range tickers {
		ticker.Stop()
	}
}

func (c *Collector) executeCollector(parentCtx context.Context, collector config.Collector) {
	c.logger.Debug("Executing collector", "collector", collector.Name, "type", collector.Type)

	// Parse timeout from config, default to 30 seconds
	timeout, err := time.ParseDuration(collector.Timeout)
	if err != nil {
		c.logger.Warn("Invalid timeout for collector, using default 30s", "collector", collector.Name, "error", err)
		timeout = 30 * time.Second
	}

	// Create context with timeout derived from parent
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	output, err := c.executeBuiltinCollector(ctx, collector)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			c.logger.Error("Builtin collector timed out", "collector", collector.Name, "timeout", timeout)
		} else {
			c.logger.Error("Builtin collector failed", "collector", collector.Name, "error", err)
		}
		return
	}

	// Check output size limit (prevent memory exhaustion)
	const maxOutputSize = 1024 * 1024 // 1MB limit
	if len(output) > maxOutputSize {
		c.logger.Error("Collector output too large", "collector", collector.Name, "size", len(output), "max", maxOutputSize)
		return
	}

	c.logger.Debug("Collector output", "collector", collector.Name, "output", output)

	// Parse output and update metrics
	c.parseAndUpdateMetrics(collector, output)
}

func (c *Collector) executeBuiltinCollector(parentCtx context.Context, collector config.Collector) (string, error) {
	collectorFunc, ok := Lookup(collector.ID)
	if !ok {
		return "", fmt.Errorf("unknown builtin collector: %s (available: %v)", collector.ID, List())
	}

	// Build CollectorContext with auth config and tenants
	collectorCtx := config.CollectorContext{
		Context: parentCtx,
		Auth:    collector.Auth,
		Tenants: collector.Tenants,
		Logger:  c.logger,
	}

	return collectorFunc(collectorCtx)
}

func (c *Collector) parseAndUpdateMetrics(collector config.Collector, output string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Parse collector output (key=value format)
	data := parseCollectorOutput(output)

	for _, metric := range collector.Metrics {
		if promCollector, exists := c.metrics[metric.Name]; exists {
			value := data[metric.Source]
			if value != "" {
				// Update metric with panic protection
				func() {
					defer func() {
						if r := recover(); r != nil {
							c.logger.Error("Panic updating metric", "metric", metric.Name, "panic", r)
						}
					}()
					c.updateMetric(promCollector, metric, value, data)
				}()
			}
		}
	}
}

func (c *Collector) updateMetric(collector prometheus.Collector, metric config.Metric, value string, data map[string]string) {
	// Create unique key for this metric instance
	labels := extractLabels(metric.Labels, data)
	metricKey := fmt.Sprintf("%s:%v", metric.Name, labels)

	// Update timestamp
	c.metricTimestamps[metricKey] = time.Now()

	switch metric.Type {
	case metricTypeGauge:
		if gaugeVec, ok := collector.(*prometheus.GaugeVec); ok {
			if f, err := parseFloat(value); err == nil {
				gaugeVec.With(labels).Set(f)
				c.logger.Debug("Updated gauge", "metric", metric.Name, "value", f)
			} else {
				c.logger.Warn("Failed to parse value for metric", "metric", metric.Name, "value", value, "error", err)
			}
		}
	case metricTypeCounter:
		if counterVec, ok := collector.(*prometheus.CounterVec); ok {
			if f, err := parseFloat(value); err == nil {
				counterVec.With(labels).Add(f)
				c.logger.Debug("Updated counter", "metric", metric.Name, "value", f)
			} else {
				c.logger.Warn("Failed to parse value for metric", "metric", metric.Name, "value", value, "error", err)
			}
		}
	}
}

func parseCollectorOutput(output string) map[string]string {
	data := make(map[string]string)
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				data[key] = value
			}
		}
	}

	return data
}

func extractLabels(labelNames []string, data map[string]string) prometheus.Labels {
	labels := make(prometheus.Labels)
	for _, labelName := range labelNames {
		if value, exists := data[labelName]; exists {
			labels[labelName] = value
		} else {
			upperKey := strings.ToUpper(labelName)
			if value, exists := data[upperKey]; exists {
				labels[labelName] = value
			} else {
				labels[labelName] = ""
			}
		}
	}
	return labels
}

func (c *Collector) cleanupExpiredMetrics(ctx context.Context) {
	maxInterval := 5 * time.Minute
	for _, collector := range c.config.Collectors {
		if interval, err := time.ParseDuration(collector.Interval); err == nil {
			if interval > maxInterval {
				maxInterval = interval
			}
		}
	}

	expirationTime := maxInterval * 2

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mutex.Lock()
			now := time.Now()
			for metricKey, timestamp := range c.metricTimestamps {
				if now.Sub(timestamp) > expirationTime {
					delete(c.metricTimestamps, metricKey)
					c.logger.Debug("Expired metric", "metric", metricKey)
				}
			}
			c.mutex.Unlock()
		}
	}
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func (c *Collector) Gatherer() prometheus.Gatherer {
	return c.registry
}
