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

package swiftmesh

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const metricsSubsystem = "swift_mesh"

var (
	// edgeReachable and edgeConnectSeconds carry one series per directed
	// connection edge, per HCP. Cardinality is sum over HCPs of N^2 (router
	// count squared); fine for the handful of router pods per HCP, but the vecs
	// are Reset() each sweep so a departed pod or deleted HCP leaves no stale
	// series.
	edgeReachable = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "edge_reachable",
			Help:           "1 if from_pod reached to_ip (haproxy answered) in the last sweep, else 0.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace", "from_pod", "from_ip", "to_ip", "kind"},
	)

	edgeConnectSeconds = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "edge_connect_seconds",
			Help:           "Last observed TCP connect time for the edge (curl time_connect), in seconds.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace", "from_pod", "from_ip", "to_ip", "kind"},
	)

	peerMeshReachable = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "peer_mesh_reachable",
			Help:           "Number of reachable directed peer-to-peer edges in the HCP's last sweep.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace"},
	)

	peerMeshTotal = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "peer_mesh_total",
			Help:           "Total directed peer-to-peer edges in the HCP's last sweep (N*(N-1)).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace"},
	)

	// selfReachable is the canary: self/loopback edges must answer on any plumbed
	// NIC. selfReachable < selfTotal means the probe itself is broken (curl
	// missing, wrong container), so the peer verdict must not be trusted.
	selfReachable = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "self_reachable",
			Help:           "Number of reachable self/loopback edges in the HCP's last sweep.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace"},
	)

	selfTotal = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "self_total",
			Help:           "Number of router pods (self/loopback edges) probed in the HCP's last sweep.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"hcp_namespace"},
	)

	sweepDuration = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      metricsSubsystem,
			Name:           "sweep_duration_seconds",
			Help:           "Time taken by a single mesh sweep, including discovery and all probes.",
			Buckets:        []float64{0.1, 0.5, 1, 2, 5, 10, 15, 30, 60},
			StabilityLevel: metrics.ALPHA,
		},
	)

	sweepErrorsTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "sweep_errors_total",
			Help:           "Number of mesh sweeps that failed before probing (e.g. discovery errors).",
			StabilityLevel: metrics.ALPHA,
		},
	)

	probeInfraFailuresTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "probe_infra_failures_total",
			Help:           "Number of HCP meshes where a self/loopback edge failed, indicating the probe itself is broken rather than the data path.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	lastSuccessfulSweepTimestamp = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      metricsSubsystem,
			Name:           "last_successful_sweep_timestamp_seconds",
			Help:           "Unix timestamp of the last sweep that completed discovery, so stale series are distinguishable from current ones.",
			StabilityLevel: metrics.ALPHA,
		},
	)
)

var registerOnce sync.Once

// RegisterMetrics registers the swift-mesh metrics with the shared legacy
// registry backing the mgmt-agent /metrics endpoint. Safe to call repeatedly.
func RegisterMetrics() {
	registerOnce.Do(func() {
		legacyregistry.MustRegister(
			edgeReachable,
			edgeConnectSeconds,
			peerMeshReachable,
			peerMeshTotal,
			selfReachable,
			selfTotal,
			sweepDuration,
			sweepErrorsTotal,
			probeInfraFailuresTotal,
			lastSuccessfulSweepTimestamp,
		)
	})
}

// edgeLabels is the per-edge label tuple (from_pod, from_ip, to_ip, kind). It is
// an array so it can key a set for stale-series pruning.
type edgeLabels [4]string

func labelsOf(r Result) edgeLabels {
	return edgeLabels{r.FromPod, r.FromIP, r.ToIP, string(r.Kind)}
}

// metricsRecorder publishes per-HCP mesh metrics and prunes only the series that
// have actually departed, rather than resetting everything each sweep. A global
// reset creates mid-sweep scrape gaps (an HCP not yet re-probed looks absent,
// breaking == 0 alerting); targeted pruning keeps every live series continuously
// present. Access is single-goroutine (the controller's sequential sweep), so no
// locking is needed.
type metricsRecorder struct {
	// seen maps hcp_namespace -> the edge label tuples written last sweep.
	seen map[string]map[edgeLabels]struct{}
}

func newMetricsRecorder() *metricsRecorder {
	return &metricsRecorder{seen: map[string]map[edgeLabels]struct{}{}}
}

// record publishes one HCP mesh's report and prunes that HCP's departed edges.
func (m *metricsRecorder) record(rep MeshReport) {
	ns := rep.Namespace
	current := make(map[edgeLabels]struct{}, len(rep.Results))
	for _, r := range rep.Results {
		lbl := labelsOf(r)
		current[lbl] = struct{}{}

		reachable := 0.0
		if r.OK {
			reachable = 1
		}
		edgeReachable.WithLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3]).Set(reachable)
		// Only record connect time for edges that actually connected; a failed
		// edge would otherwise graph as a misleading 0s connect.
		if r.OK {
			edgeConnectSeconds.WithLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3]).Set(r.ConnectTime.Seconds())
		} else {
			edgeConnectSeconds.DeleteLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3])
		}
	}

	// Drop edges this HCP no longer has (e.g. a router pod was rescheduled).
	for lbl := range m.seen[ns] {
		if _, ok := current[lbl]; !ok {
			edgeReachable.DeleteLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3])
			edgeConnectSeconds.DeleteLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3])
		}
	}
	m.seen[ns] = current

	peerMeshReachable.WithLabelValues(ns).Set(float64(rep.PeerOK))
	peerMeshTotal.WithLabelValues(ns).Set(float64(rep.PeerTotal))
	selfReachable.WithLabelValues(ns).Set(float64(rep.SelfOK))
	selfTotal.WithLabelValues(ns).Set(float64(rep.SelfTotal))
}

// pruneAbsent removes all series for HCP namespaces not present in the given set,
// so a deleted HCP leaves no stale series behind.
func (m *metricsRecorder) pruneAbsent(present map[string]struct{}) {
	for ns, edges := range m.seen {
		if _, ok := present[ns]; ok {
			continue
		}
		for lbl := range edges {
			edgeReachable.DeleteLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3])
			edgeConnectSeconds.DeleteLabelValues(ns, lbl[0], lbl[1], lbl[2], lbl[3])
		}
		peerMeshReachable.DeleteLabelValues(ns)
		peerMeshTotal.DeleteLabelValues(ns)
		selfReachable.DeleteLabelValues(ns)
		selfTotal.DeleteLabelValues(ns)
		delete(m.seen, ns)
	}
}
