// Copyright 2026 Microsoft Corporation

package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// CachingCollector is an interface for collectors that cache metrics
// This is used, cause it improves the performance of the exporter by avoiding
// adhoc API calls during metrics collection.
type CachingCollector interface {
	// Name returns the name of the collector
	Name() string
	// Describe metrics, according to the Prometheus specification
	Describe(ch chan<- *prometheus.Desc)
	// Collect metrics, according to the Prometheus specification
	Collect(ch chan<- prometheus.Metric)
	// CollectMetricValues is a loop that collects metrics from the source and adds them to the cache
	CollectMetricValues(ctx context.Context)
}
