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

	// nodeWedged names the individual wedged nodes so an alert can carry the node
	// and the failure mode it fired on instead of only a count. The node label is
	// deliberately on a gauge and not on detections_total: a counter series is
	// born on first detection and never retires, so a per-node counter would
	// accumulate a series for every node name that ever wedged, including the
	// churned and deleted ones. This vector is rebuilt from the live list of
	// labeled nodes on every resync, so its cardinality is bounded by the nodes
	// wedged right now and a node that recovers or disappears drops out.
	nodeWedged = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "node_wedged",
			Help:           "Set to 1 for each node currently carrying the wedged health label, labeled with the detector that fired and the signature that classified the failure. The series is removed once the node recovers, stops being a detection candidate, or is deleted. Empty while the controller is disabled.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"node", "detector", "signature"},
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
		legacyregistry.MustRegister(nodeWedged)
	})
}
