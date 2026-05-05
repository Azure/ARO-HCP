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

package controllerutils

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	// BackendOperationsTotal counts total backend operations by type.
	// Labels: type (e.g., "poll_node_pool", "poll_cluster", etc.)
	BackendOperationsTotal = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_operations_total",
			Help: "Total number of backend operations by type.",
		},
		[]string{"type"},
	)

	// BackendFailedOperationsTotal counts failed backend operations by type.
	// Labels: type (e.g., "poll_node_pool", "poll_cluster", etc.)
	BackendFailedOperationsTotal = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_failed_operations_total",
			Help: "Total number of failed backend operations by type.",
		},
		[]string{"type"},
	)

	// BackendOperationsDuration tracks duration of backend operations by type.
	// Labels: type (e.g., "poll_node_pool", "poll_cluster", etc.)
	BackendOperationsDuration = promauto.With(legacyregistry.Registerer()).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "backend_operations_duration_seconds",
			Help:    "Duration of backend operations in seconds.",
			Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"type"},
	)
)

// OperationMetrics provides a helper to track operation metrics.
type OperationMetrics struct {
	operationType string
	startTime     time.Time
}

// NewOperationMetrics creates a new operation metrics tracker.
func NewOperationMetrics(operationType string) *OperationMetrics {
	return &OperationMetrics{
		operationType: operationType,
		startTime:     time.Now(),
	}
}

// RecordSuccess records a successful operation completion.
func (om *OperationMetrics) RecordSuccess() {
	BackendOperationsTotal.WithLabelValues(om.operationType).Inc()
	duration := time.Since(om.startTime).Seconds()
	BackendOperationsDuration.WithLabelValues(om.operationType).Observe(duration)
}

// RecordFailure records a failed operation completion.
func (om *OperationMetrics) RecordFailure() {
	BackendOperationsTotal.WithLabelValues(om.operationType).Inc()
	BackendFailedOperationsTotal.WithLabelValues(om.operationType).Inc()
	duration := time.Since(om.startTime).Seconds()
	BackendOperationsDuration.WithLabelValues(om.operationType).Observe(duration)
}
