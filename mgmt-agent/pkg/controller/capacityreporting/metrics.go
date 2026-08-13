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

package capacityreporting

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const metricsSubsystem = "capacity_reporting"

var (
	syncDuration = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      metricsSubsystem,
			Name:           "sync_duration_seconds",
			Help:           "Time taken by a single capacity reporting sync loop iteration, including data collection and status apply.",
			Buckets:        []float64{0.1, 0.5, 1, 2, 5, 10, 15, 20, 25, 30},
			StabilityLevel: metrics.ALPHA,
		},
	)

	syncErrorsTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "sync_errors_total",
			Help:           "Number of capacity reporting sync failures.",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

var registerOnce sync.Once

// RegisterMetrics registers the capacity reporting metrics with the shared
// legacy registry used by the mgmt-agent /metrics endpoint. Safe to call
// multiple times.
func RegisterMetrics() {
	registerOnce.Do(func() {
		legacyregistry.MustRegister(syncDuration)
		legacyregistry.MustRegister(syncErrorsTotal)
	})
}
