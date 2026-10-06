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
	"context"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	const self = "10.100.77.5"
	if got := Classify(self, self); got != KindSelf {
		t.Fatalf("Classify(self) = %q, want %q", got, KindSelf)
	}
	if got := Classify("10.100.77.7", self); got != KindPeer {
		t.Fatalf("Classify(peer) = %q, want %q", got, KindPeer)
	}
}

func TestEnumerateEdges(t *testing.T) {
	edges := enumerateEdges(threeRouterMesh())

	// 3 vantages * 3 router targets = 9 directed edges (3 self + 6 peer).
	if len(edges) != 9 {
		t.Fatalf("got %d edges, want 9", len(edges))
	}
	var self, peer int
	for _, e := range edges {
		switch e.target.Kind {
		case KindSelf:
			self++
		case KindPeer:
			peer++
		}
	}
	if self != 3 || peer != 6 {
		t.Fatalf("edge kinds: self=%d peer=%d, want 3/6", self, peer)
	}
}

// fakeDriver reports self as reachable (loopback) unless selfFails, and peers
// reachable only when healthy — the split the reference bash probe encodes.
type fakeDriver struct {
	healthy   bool
	selfFails bool
}

func (d fakeDriver) Probe(_ context.Context, from RouterPod, target Target, _ int) Result {
	res := Result{FromPod: from.Name, FromIP: from.SwiftIP, ToIP: target.IP, Kind: target.Kind}
	switch target.Kind {
	case KindSelf:
		if d.selfFails {
			res.TimedOut = true
		} else {
			res.OK, res.StatusCode, res.ConnectTime = true, 403, 200*time.Microsecond
		}
	case KindPeer:
		if d.healthy {
			res.OK, res.StatusCode, res.ConnectTime = true, 403, 5*time.Millisecond
		} else {
			res.TimedOut = true
		}
	}
	return res
}

func TestRunMeshHealthy(t *testing.T) {
	rep := RunMesh(context.Background(), threeRouterMesh(), fakeDriver{healthy: true}, NewConcurrencyBudget(4), time.Second)

	if !rep.Healthy() {
		t.Fatalf("expected healthy mesh, got peer %d/%d", rep.PeerOK, rep.PeerTotal)
	}
	if rep.PeerOK != 6 || rep.PeerTotal != 6 {
		t.Fatalf("peer mesh = %d/%d, want 6/6", rep.PeerOK, rep.PeerTotal)
	}
	if rep.Pass != 9 || rep.Fail != 0 {
		t.Fatalf("pass/fail = %d/%d, want 9/0", rep.Pass, rep.Fail)
	}
	if rep.SelfOK != 3 || rep.SelfTotal != 3 || !rep.ProbeTrustworthy() {
		t.Fatalf("self = %d/%d trustworthy=%v, want 3/3 true", rep.SelfOK, rep.SelfTotal, rep.ProbeTrustworthy())
	}
	if rep.Namespace != "ocm-int-abc" || rep.ResourceID == "" {
		t.Fatalf("report HCP identity not propagated: ns=%q id=%q", rep.Namespace, rep.ResourceID)
	}
}

func TestRunMeshBroken(t *testing.T) {
	rep := RunMesh(context.Background(), threeRouterMesh(), fakeDriver{healthy: false}, NewConcurrencyBudget(4), time.Second)

	if rep.Healthy() {
		t.Fatal("expected broken mesh to be unhealthy")
	}
	if rep.PeerOK != 0 || rep.PeerTotal != 6 {
		t.Fatalf("peer mesh = %d/%d, want 0/6", rep.PeerOK, rep.PeerTotal)
	}
	// Only the 3 self/loopback edges answer; all peers time out.
	if rep.Pass != 3 || rep.Fail != 6 {
		t.Fatalf("pass/fail = %d/%d, want 3/6", rep.Pass, rep.Fail)
	}
	for _, r := range rep.Results {
		if r.Kind != KindSelf && !r.TimedOut {
			t.Fatalf("expected non-self edge %s->%s to time out", r.FromPod, r.ToIP)
		}
	}
	// Self/loopback still answers, so the probe is trustworthy: this is a genuine
	// data-path fault, not a broken probe.
	if !rep.ProbeTrustworthy() {
		t.Fatalf("broken data path must still be a trustworthy probe: self %d/%d", rep.SelfOK, rep.SelfTotal)
	}
}

// TestRunMeshProbeUntrustworthy models curl missing / wrong container: even the
// self/loopback edges fail, so the probe cannot be trusted and the peer verdict
// must be disregarded.
func TestRunMeshProbeUntrustworthy(t *testing.T) {
	rep := RunMesh(context.Background(), threeRouterMesh(), fakeDriver{healthy: false, selfFails: true}, NewConcurrencyBudget(4), time.Second)

	if rep.ProbeTrustworthy() {
		t.Fatal("expected probe to be untrustworthy when self/loopback fails")
	}
	if rep.SelfOK != 0 || rep.SelfTotal != 3 {
		t.Fatalf("self = %d/%d, want 0/3", rep.SelfOK, rep.SelfTotal)
	}
}

func TestParseCurlOutput(t *testing.T) {
	tests := []struct {
		name, in   string
		wantStatus int
		wantConnMs float64
		wantOK     bool
	}{
		{"healthy 403", "403 0.005000", 403, 5, true},
		{"timeout zeros", "000 0.000000", 0, 0, true},
		{"self loopback", "403 0.000200", 403, 0.2, true},
		{"garbage", "nonsense", 0, 0, false},
		{"empty", "", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, conn, ok := parseCurlOutput(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if gotMs := float64(conn.Microseconds()) / 1000; gotMs != tc.wantConnMs {
				t.Fatalf("connect = %vms, want %vms", gotMs, tc.wantConnMs)
			}
		})
	}
}

func threeRouterMesh() HCPMesh {
	return HCPMesh{
		Namespace:  "ocm-int-abc",
		ResourceID: "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
		Routers: []RouterPod{
			{Name: "router-a", Namespace: "ocm-int-abc", Container: "router", SwiftIP: "10.100.77.5"},
			{Name: "router-b", Namespace: "ocm-int-abc", Container: "router", SwiftIP: "10.100.77.7"},
			{Name: "router-c", Namespace: "ocm-int-abc", Container: "router", SwiftIP: "10.100.77.9"},
		},
		Port: 8443,
	}
}
