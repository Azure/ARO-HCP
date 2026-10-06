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

// Package swiftmesh probes the SwiftV2 (SWIFT NIC) full data-path mesh between a
// Hosted Control Plane's router pods. SwiftV2 control-plane signals (PNI Ready,
// MTPNC bound, SAL Succeeded, CNS counts) can all be green while the network
// container carries zero peer traffic: each NIC reaches only itself and every
// peer-to-peer path times out. Only an actual in-pod probe of every edge reveals
// that fault.
//
// On a management cluster each HCP's control plane runs in its own ocm-* / CP
// namespace, created and destroyed as HCPs come and go. The probe is therefore
// scoped per HCP: it discovers router pods across all HCP namespaces, builds and
// probes one mesh per HCP, logs every connection endpoint, and exports a metric
// per connection edge labelled by HCP.
package swiftmesh

import "time"

// Kind labels a probe target relative to the vantage it is probed from.
type Kind string

const (
	// KindSelf is the vantage's own SWIFT NIC IP: a loopback that works even
	// when the data path is dead, so it only proves the NIC is plumbed.
	KindSelf Kind = "self"
	// KindPeer is another router's SWIFT NIC IP within the same HCP. These
	// directed edges form the mesh whose health the verdict is based on.
	KindPeer Kind = "peer"
)

// RouterPod is a single probe vantage: a router pod and its SWIFT NIC IP.
type RouterPod struct {
	Name      string
	Namespace string
	Container string
	SwiftIP   string
}

// Target is a destination probed from a vantage, already classified for that
// vantage.
type Target struct {
	IP   string
	Kind Kind
}

// HCPMesh is the router topology of one Hosted Control Plane: all router pods in
// its control-plane namespace, to be probed as a self-contained mesh.
type HCPMesh struct {
	Namespace  string // the HCP control-plane (ocm-*) namespace.
	ResourceID string // the HCP's Azure resource ID, for metric/log correlation.
	Routers    []RouterPod
	Port       int
}

// Result is the outcome of one directed edge probe (from a vantage to a target).
type Result struct {
	FromPod     string
	FromIP      string
	ToIP        string
	Kind        Kind
	StatusCode  int           // HTTP status; 0 when no HTTP response came back.
	ConnectTime time.Duration // curl time_connect for the edge.
	OK          bool          // true iff haproxy answered (any HTTP response).
	TimedOut    bool          // true when the probe hit the deadline (curl exit 28).
	Err         error
}

// Classify labels targetIP relative to a vantage whose own SWIFT IP is selfIP.
func Classify(targetIP, selfIP string) Kind {
	if targetIP == selfIP {
		return KindSelf
	}
	return KindPeer
}

type edge struct {
	from   RouterPod
	target Target
}

// enumerateEdges builds every directed edge of one HCP mesh: each vantage probes
// every router's SWIFT IP, including its own.
func enumerateEdges(m HCPMesh) []edge {
	edges := make([]edge, 0, len(m.Routers)*len(m.Routers))
	for _, from := range m.Routers {
		for _, to := range m.Routers {
			edges = append(edges, edge{
				from:   from,
				target: Target{IP: to.SwiftIP, Kind: Classify(to.SwiftIP, from.SwiftIP)},
			})
		}
	}
	return edges
}

// MeshReport aggregates one HCP mesh's results.
type MeshReport struct {
	Namespace  string
	ResourceID string
	Results    []Result
	PeerOK     int // reachable directed peer-to-peer edges.
	PeerTotal  int // N*(N-1) for N vantages.
	SelfOK     int // reachable self/loopback edges.
	SelfTotal  int // N self edges (one per vantage).
	Pass       int // reachable edges including self.
	Fail       int
}

// Healthy reports whether the full peer mesh is reachable. It is only meaningful
// when ProbeTrustworthy is true; a failing self/loopback edge means the probe
// itself is broken (e.g. curl missing, wrong container), not the data path.
func (r MeshReport) Healthy() bool { return r.PeerTotal > 0 && r.PeerOK == r.PeerTotal }

// ProbeTrustworthy reports whether every self/loopback edge answered. The self
// edge never leaves the pod, so it must succeed on any plumbed NIC regardless of
// peer data-path health; if it fails, the peer verdict cannot be trusted.
func (r MeshReport) ProbeTrustworthy() bool { return r.SelfTotal > 0 && r.SelfOK == r.SelfTotal }

// BuildReport tallies results. nVantages is the number of router pods probed.
func BuildReport(results []Result, nVantages int) MeshReport {
	rep := MeshReport{Results: results, PeerTotal: nVantages * (nVantages - 1), SelfTotal: nVantages}
	for _, res := range results {
		if res.OK {
			rep.Pass++
			switch res.Kind {
			case KindPeer:
				rep.PeerOK++
			case KindSelf:
				rep.SelfOK++
			}
		} else {
			rep.Fail++
		}
	}
	return rep
}
