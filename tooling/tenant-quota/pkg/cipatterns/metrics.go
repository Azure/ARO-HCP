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

package cipatterns

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type controllerMetrics struct {
	scans     *prometheus.CounterVec
	accepted  prometheus.Counter
	visible   prometheus.Counter
	cacheHits prometheus.Counter
	failures  *prometheus.CounterVec

	uncoveredInputs   *prometheus.GaugeVec
	consideredInputs  *prometheus.GaugeVec
	lastCoverageCheck prometheus.Gauge
	controlRows       prometheus.Counter
}

func newControllerMetrics() controllerMetrics {
	return controllerMetrics{
		scans:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ci_failure_patterns_scans_total", Help: "Completed build discovery steps: newest-build polls (head) and history sweep pages (sweep)."}, []string{"kind"}),
		accepted:  prometheus.NewCounter(prometheus.CounterOpts{Name: "ci_failure_patterns_accepted_batches_total", Help: "Queued batches accepted, not proof of result visibility."}),
		visible:   prometheus.NewCounter(prometheus.CounterOpts{Name: "ci_failure_patterns_visible_reconciles_total", Help: "Reconciles with every expected computation query-visible and valid."}),
		cacheHits: prometheus.NewCounter(prometheus.CounterOpts{Name: "ci_failure_patterns_submission_cache_hits_total", Help: "Reconciles where every missing computation was recently submitted and is awaiting visibility."}),
		failures:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ci_failure_patterns_failures_total", Help: "Retryable failures by bounded operation."}, []string{"operation"}),

		uncoveredInputs:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ci_failure_patterns_uncovered_inputs", Help: "Raw failed inputs without a result of the compiled extractor version at the last successful coverage check, by source."}, []string{"source"}),
		consideredInputs:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ci_failure_patterns_considered_inputs", Help: "Raw failed inputs considered by the last successful coverage check, by source."}, []string{"source"}),
		lastCoverageCheck: prometheus.NewGauge(prometheus.GaugeOpts{Name: "ci_failure_patterns_last_successful_coverage_check_timestamp_seconds", Help: "Unix time of the last coverage check that completed without error."}),
		controlRows:       prometheus.NewCounter(prometheus.CounterOpts{Name: "ci_failure_patterns_control_rows_written_total", Help: "Control rows (activation or heartbeat) accepted for queued ingestion."}),
	}
}

// RegisterMetrics returns registration errors to the binary instead of panicking.
func (c *Controller) RegisterMetrics(registerer prometheus.Registerer) error {
	for _, collector := range []prometheus.Collector{c.metrics.scans, c.metrics.accepted, c.metrics.visible, c.metrics.cacheHits, c.metrics.failures, c.metrics.uncoveredInputs, c.metrics.consideredInputs, c.metrics.lastCoverageCheck, c.metrics.controlRows} {
		if err := registerer.Register(collector); err != nil {
			return fmt.Errorf("register failure pattern metrics: %w", err)
		}
	}
	return nil
}
