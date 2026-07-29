// Copyright 2026 Microsoft Corporation

package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// CachingCollector is an interface for collectors that cache metrics.
// Background collection is decoupled from Prometheus scrapes: CollectMetricValues
// runs on a timer and writes to a cache, while Collect reads from the cache.
type CachingCollector interface {
	Name() string
	Describe(ch chan<- *prometheus.Desc)
	Collect(ch chan<- prometheus.Metric)
	CollectMetricValues(ctx context.Context)
}
