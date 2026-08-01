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

package nodehealth

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const metricsSubsystem = "nodehealth"

var (
	detectionsTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "detections_total",
			Help:           "Number of times a detector caused a node to be marked wedged, counted when the node's detection record changes rather than on every re-evaluation. The signature label names the failure mode within the detector's family and is triage detail only.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"detector", "signature"},
	)

	labelActionsTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "label_actions_total",
			Help:           "Number of label/unlabel actions, by action and result.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"action", "result"},
	)

	wedgedNodes = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "wedged_nodes",
			Help:           "Number of nodes currently carrying the wedged health label, as observed by the node-health controller. Reported as 0 while the controller is disabled.",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

var registerOnce sync.Once

// RegisterMetrics registers the node-health metrics with the shared legacy
// registry used by the mgmt-agent /metrics endpoint. Safe to call multiple times.
func RegisterMetrics() {
	registerOnce.Do(func() {
		legacyregistry.MustRegister(detectionsTotal)
		legacyregistry.MustRegister(labelActionsTotal)
		legacyregistry.MustRegister(wedgedNodes)
	})
}
