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

package cosmosratelimit

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosmetrics"
)

var defaultMetrics = newRateLimitMetrics(legacyregistry.Registerer())

type rateLimitMetrics struct {
	requests     *prometheus.CounterVec
	waits        *prometheus.CounterVec
	waiting      *prometheus.GaugeVec
	waitDuration *prometheus.HistogramVec
}

func newRateLimitMetrics(registerer prometheus.Registerer) *rateLimitMetrics {
	metrics := &rateLimitMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cosmos_rate_limiter_requests_total",
			Help: "Cosmos HTTP attempts dispatched through an RU limiter, counted once per attempt by outcome; unknown status means no response.",
		}, cosmosmetrics.LabelNames()),
		waits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cosmos_rate_limiter_waits_total",
			Help: "Calls that had to wait for a Cosmos RU budget, counted when waiting begins, including canceled waits.",
		}, cosmosmetrics.LabelNames()),
		waiting: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cosmos_rate_limiter_waiting_requests",
			Help: "Calls currently waiting for a Cosmos RU budget at the CRUD, transaction, or HTTP attempt boundary.",
		}, cosmosmetrics.LabelNames()),
		waitDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cosmos_rate_limiter_wait_duration_seconds",
			Help:    "Time spent waiting for a Cosmos RU budget, including canceled waits; excludes calls that did not wait.",
			Buckets: []float64{0.01, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, cosmosmetrics.LabelNames()),
	}
	registerer.MustRegister(metrics.collectors()...)
	return metrics
}

func (m *rateLimitMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{m.requests, m.waits, m.waiting, m.waitDuration}
}

// RegisterMetrics exposes the shared limiter metrics through an additional
// registerer. They are always registered with the legacy registry used by the
// backend, frontend, fleet, and kube-applier metrics endpoints.
func RegisterMetrics(registerer prometheus.Registerer) error {
	for _, collector := range defaultMetrics.collectors() {
		if err := registerer.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

// waitRequestKey supplies HTTP classification only for waits inside the policy.
// Earlier CRUD/transaction waits retain the same source labels but cannot yet
// know the eventual request's container or operation. Waits have no HTTP status.
type waitRequestKey struct{}

func (m *rateLimitMetrics) beginWait(ctx context.Context) func() {
	req, _ := ctx.Value(waitRequestKey{}).(*http.Request)
	labels := cosmosmetrics.LabelValues(ctx, req, nil)
	m.waits.WithLabelValues(labels...).Inc()
	gauge := m.waiting.WithLabelValues(labels...)
	gauge.Inc()
	started := time.Now()
	return func() {
		gauge.Dec()
		m.waitDuration.WithLabelValues(labels...).Observe(time.Since(started).Seconds())
	}
}
