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

package cijoboutcomes

import "github.com/prometheus/client_golang/prometheus"

type writerMetrics struct {
	discovery   *prometheus.GaugeVec
	submissions *prometheus.CounterVec
	cacheHits   *prometheus.CounterVec
	artifacts   *prometheus.CounterVec
}

func newWriterMetrics() writerMetrics {
	return writerMetrics{
		discovery:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ci_job_outcomes_discovery_last_success_timestamp_seconds", Help: "Last successful scan by controller."}, []string{"controller"}),
		submissions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ci_job_outcomes_submissions_total", Help: "Synchronous batch submissions, not asynchronous ingestion completion."}, []string{"batch", "result"}),
		cacheHits:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ci_job_outcomes_cache_hits_total", Help: "Cache hits by cache."}, []string{"cache"}),
		artifacts:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "ci_job_outcomes_artifact_problems_total", Help: "Permanent artifact problems, not scheduled for retry."}, []string{"source", "reason"}),
	}
}

// RegisterMetrics is optional; queue metrics are installed by the binary.
func (w *Writer) RegisterMetrics(registerer prometheus.Registerer) {
	registerer.MustRegister(w.metrics.discovery, w.metrics.submissions, w.metrics.cacheHits, w.metrics.artifacts)
	for _, cache := range []struct {
		name string
		size func() int
	}{
		{"batches", w.batches.size},
	} {
		registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "ci_job_outcomes_cache_size", Help: "Unexpired cache entries.", ConstLabels: prometheus.Labels{"cache": cache.name},
		}, func() float64 { return float64(cache.size()) }))
	}
}
