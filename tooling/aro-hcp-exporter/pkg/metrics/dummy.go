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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// DummyMetricDesc is the descriptor for the dummy metric
	DummyMetricDesc = prometheus.NewDesc(
		"aro_hcp_exporter_dummy_metric",
		"A dummy metric for testing the exporter",
		[]string{"subscription_id", "region"},
		nil,
	)
)

// DummyMetric is a simple Prometheus metric collector
type DummyMetric struct {
	value float64
}

// NewDummyMetric creates a new dummy metric
func NewDummyMetric() *DummyMetric {
	return &DummyMetric{
		value: 1.0,
	}
}

// Describe implements prometheus.Collector
func (m *DummyMetric) Describe(ch chan<- *prometheus.Desc) {
	ch <- DummyMetricDesc
}

// Collect implements prometheus.Collector
func (m *DummyMetric) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(
		DummyMetricDesc,
		prometheus.GaugeValue,
		m.value,
		"test",
		"eastus",
	)
}
