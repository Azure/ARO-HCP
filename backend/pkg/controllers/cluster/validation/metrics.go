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

package validation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	// validationRunDuration observes how long each cluster validation run takes, partitioned by
	// validation name and outcome. Its _count series doubles as the per-outcome run counter, so a
	// validation that stops producing samples is a stalled validation controller.
	validationRunDuration = promauto.With(legacyregistry.Registerer()).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "backend_cluster_validation_run_duration_seconds",
			Help:    "Duration of cluster validation runs, partitioned by validation and outcome.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"validation", "outcome"},
	)

	// validationRunTimeouts counts validation runs that were cut off by validationRunTimeout.
	validationRunTimeouts = promauto.With(legacyregistry.Registerer()).NewCounterVec(
		prometheus.CounterOpts{
			Name: "backend_cluster_validation_run_timeouts_total",
			Help: "Total number of cluster validation runs that exceeded the per-run timeout.",
		},
		[]string{"validation"},
	)
)
