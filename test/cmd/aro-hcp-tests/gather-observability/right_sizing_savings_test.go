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

package gatherobservability

import (
	"math"
	"reflect"
	"testing"
	"time"

	"k8s.io/utils/ptr"
)

func savingsFixture() (rightSizingReport, utilizationReport) {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	r := rightSizingReport{Version: 3, Headroom: 1, Start: at, End: at.Add(time.Hour), ChangeThreshold: .1,
		Recommendations: []rightSizingRecommendation{{Cluster: "svc", Namespace: "aro-hcp", Kind: "Deployment", Workload: "backend", Container: "aro-hcp-backend",
			Resource: "cpu", Eligible: true, Replicas: 999, MeasuredReplicas: 999, Suggested: ptr.To(.5), Peak: ptr.To(.4), RequestMin: ptr.To(1.0), RequestMax: ptr.To(1.0)}}}
	u := utilizationReport{Start: at, End: at.Add(time.Hour), Clusters: []string{"svc"}, Snapshots: []utilizationSnapshot{{Time: at,
		Workloads: []utilizationWorkload{{Cluster: "svc", Namespace: "aro-hcp", Kind: "Deployment", Name: "backend", Node: "node", Pods: 1,
			Containers: []utilizationContainer{{Name: "aro-hcp-backend", Requests: utilizationResources{CPU: ptr.To(1.0), Memory: ptr.To(0.0)},
				Limits: utilizationResources{CPU: ptr.To(2.0)}, UnlimitedCPU: ptr.To(0)}}}}}}}
	return r, u
}

func TestRightSizingSavingsGuards(t *testing.T) {
	for _, version := range []int{1, 2} {
		r, u := savingsFixture()
		r.Version = version
		if got := buildRightSizingSavings(r, u); got != nil {
			t.Fatalf("incompatible version %d must not produce savings: %+v", version, got)
		}
	}
	for _, tc := range []struct {
		name              string
		mutate            func(*rightSizingReport, *utilizationWorkload)
		after             float64
		changed, excluded int
	}{
		{"concurrent pod not historical replicas", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations[0].Replicas = 5000 }, .5, 1, 0},
		{"pending retains demand", func(r *rightSizingReport, w *utilizationWorkload) { w.PendingPods = 1 }, 1, 0, 1},
		{"unscheduled retains demand", func(r *rightSizingReport, w *utilizationWorkload) { w.Unscheduled = true }, 1, 0, 1},
		{"unknown placement", func(r *rightSizingReport, w *utilizationWorkload) { w.Node = "unknown" }, 1, 0, 1},
		{"unknown limit coverage", func(r *rightSizingReport, w *utilizationWorkload) { w.Containers[0].UnlimitedCPU = nil }, 1, 0, 1},
		{"unknown limit", func(r *rightSizingReport, w *utilizationWorkload) { w.Containers[0].Limits.CPU = nil }, 1, 0, 1},
		{"invalid unlimited count", func(r *rightSizingReport, w *utilizationWorkload) { w.Containers[0].UnlimitedCPU = ptr.To(3) }, 1, 0, 1},
		{"all unlimited", func(r *rightSizingReport, w *utilizationWorkload) {
			w.Containers[0].UnlimitedCPU, w.Containers[0].Limits.CPU = ptr.To(1), ptr.To(0.0)
		}, .5, 1, 0},
		{"finite limit guard", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations[0].Suggested = ptr.To(3.0) }, 1, 0, 1},
		{"increase", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations[0].Suggested = ptr.To(1.5) }, 1.5, 1, 0},
		{"matched tolerance", func(r *rightSizingReport, w *utilizationWorkload) {
			r.Recommendations[0].Suggested = ptr.To(1.0000000000001)
		}, 1, 0, 0},
		{"inclusive deadband", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations[0].Suggested = ptr.To(.9) }, 1, 0, 0},
		{"peak overrides deadband", func(r *rightSizingReport, w *utilizationWorkload) {
			r.Recommendations[0].Suggested, r.Recommendations[0].Peak = ptr.To(1.1), ptr.To(1.3)
		}, 1.1, 1, 0},
		{"no exact identity", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations[0].Workload = "other" }, 1, 0, 1},
		{"uneditable container", func(r *rightSizingReport, w *utilizationWorkload) { w.Containers[0].Name = "sidecar" }, 1, 0, 1},
		{"no recommendations", func(r *rightSizingReport, w *utilizationWorkload) { r.Recommendations = nil }, 1, 0, 1},
		{"stale union gap", func(r *rightSizingReport, w *utilizationWorkload) {
			r.Recommendations[0].RequestMin, r.Recommendations[0].RequestMax = ptr.To(.5), ptr.To(.5)
			other := r.Recommendations[0]
			other.RequestMin, other.RequestMax = ptr.To(2.0), ptr.To(2.0)
			r.Recommendations = append(r.Recommendations, other)
		}, 1, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, u := savingsFixture()
			tc.mutate(&r, &u.Snapshots[0].Workloads[0])
			got := buildRightSizingSavings(r, u)
			want := rightSizingSavingsResource{Cluster: "svc", Resource: "cpu", Before: 1, After: tc.after,
				Reductions: math.Max(0, 1-tc.after), Increases: math.Max(0, tc.after-1), ChangedContainers: tc.changed, ExcludedContainers: tc.excluded}
			if got == nil || len(got.Resources) != 2 || got.Resources[0] != want {
				t.Fatalf("savings = %+v, want CPU %+v", got, want)
			}
		})
	}
}

func TestRightSizingSavingsGlobalEvidence(t *testing.T) {
	for _, hcp := range []bool{false, true} {
		for _, blocker := range []string{"none", "ineligible", "missing", "unknown owner", "wrong owner", "init"} {
			t.Run(blocker+"/hcp="+map[bool]string{true: "true", false: "false"}[hcp], func(t *testing.T) {
				r, u := savingsFixture()
				if hcp {
					r.Recommendations[0].Namespace, r.Recommendations[0].Workload, r.Recommendations[0].Container = "ocm-arohcpci-one", "kube-apiserver", "kube-apiserver"
					w := &u.Snapshots[0].Workloads[0]
					w.Namespace, w.Name, w.Containers[0].Name = "ocm-arohcpci-one", "kube-apiserver", "kube-apiserver"
				}
				other := r.Recommendations[0]
				other.Cluster, other.Suggested = "another-cluster", ptr.To(.75)
				if hcp {
					other.Namespace = "ocm-arohcpint-two"
				}
				switch blocker {
				case "ineligible":
					other.Eligible = false
				case "missing":
					other.RequestMin = nil
				case "unknown owner":
					other.Kind, other.Workload = "Pod", "unresolved@uid"
				case "wrong owner":
					other.Kind = "Node"
				case "init":
					other.InitContainer = true
				}
				// Duplicated workspace evidence selects a maximum, never a sum.
				r.Recommendations = append(r.Recommendations, other, other)
				got := buildRightSizingSavings(r, u).Resources[0]
				wantAfter, wantChanged, wantExcluded := .75, 1, 0
				if blocker != "none" {
					wantAfter, wantChanged, wantExcluded = 1, 0, 1
				}
				if blocker == "init" && !hcp {
					wantAfter, wantChanged, wantExcluded = .5, 1, 0
				}
				if got.After != wantAfter || got.ChangedContainers != wantChanged || got.ExcludedContainers != wantExcluded {
					t.Fatalf("global evidence guard: got %+v", got)
				}
			})
		}
	}
}

func TestRightSizingSavingsSnapshot(t *testing.T) {
	r, u := savingsFixture()
	u.Start, u.End = r.Start.Add(-time.Hour), r.End.Add(time.Hour)
	base := u.Snapshots[0].Workloads[0]
	other := base
	other.Cluster, other.Pods = "mgmt", 3
	later := other
	later.Pods = 4
	u.Clusters = append(u.Clusters, "mgmt")
	u.Snapshots = []utilizationSnapshot{
		{Time: r.Start.Add(time.Minute), Workloads: []utilizationWorkload{base, other}},
		{Time: r.Start, Workloads: []utilizationWorkload{base, other}},
		{Time: r.Start.Add(2 * time.Minute), Workloads: []utilizationWorkload{later}},
	}
	out := base
	out.Pods = 1000
	u.Snapshots = append(u.Snapshots, utilizationSnapshot{Time: r.Start.Add(-time.Minute), Workloads: []utilizationWorkload{out}})
	foreign := out
	foreign.Cluster = "not-in-report"
	u.Snapshots = append(u.Snapshots, utilizationSnapshot{Time: r.Start.Add(3 * time.Minute), Workloads: []utilizationWorkload{foreign}})
	got := buildRightSizingSavings(r, u)
	if got == nil || !got.Time.Equal(r.Start) || len(got.Resources) != 4 || got.Resources[2].Before != 1 || got.Resources[2].After != .5 {
		t.Fatalf("expected earliest fleet peak, not summed cluster/historical peaks: %+v", got)
	}
	u.Snapshots = u.Snapshots[3:4]
	if buildRightSizingSavings(r, u) != nil {
		t.Fatal("out-of-sizing-window snapshot must be rejected")
	}
	u.Snapshots[0].Time = r.Start
	u.Start = r.Start.Add(time.Minute)
	if buildRightSizingSavings(r, u) != nil {
		t.Fatal("out-of-utilization-window snapshot must be rejected")
	}
	u.Snapshots = nil
	if buildRightSizingSavings(r, u) != nil {
		t.Fatal("missing snapshot must return nil")
	}
}

func TestRightSizingSavingsUnknownAndZero(t *testing.T) {
	r, u := savingsFixture()
	w := &u.Snapshots[0].Workloads[0]
	w.Containers = append(w.Containers, utilizationContainer{Name: "unknown"}, utilizationContainer{Name: "invalid", Requests: utilizationResources{CPU: ptr.To(math.NaN())}},
		utilizationContainer{Name: "known-zero", Requests: utilizationResources{CPU: ptr.To(0.0)}})
	got := buildRightSizingSavings(r, u)
	want := rightSizingSavingsResource{Cluster: "svc", Resource: "cpu", Before: 1, After: .5, Reductions: .5, ChangedContainers: 1, ExcludedContainers: 1, UnknownContainers: 2}
	if !reflect.DeepEqual(got.Resources[0], want) {
		t.Fatalf("known lower bound = %+v, want %+v", got.Resources[0], want)
	}
	// Valid zero requests can increase without dividing by zero.
	r.Recommendations[0].RequestMin, r.Recommendations[0].RequestMax = ptr.To(0.0), ptr.To(0.0)
	w.Containers[0].Requests.CPU = ptr.To(0.0)
	got = buildRightSizingSavings(r, u)
	if got.Resources[0].Before != 0 || got.Resources[0].After != .5 || got.Resources[0].Increases != .5 {
		t.Fatalf("zero-request increase = %+v", got.Resources[0])
	}
}

func TestRightSizingSavingsMultiplePodsExcluded(t *testing.T) {
	for _, heterogeneous := range []bool{false, true} {
		r, u := savingsFixture()
		w := &u.Snapshots[0].Workloads[0]
		w.Pods = 2
		w.Containers[0].Requests.CPU, w.Containers[0].Limits.CPU = ptr.To(2.0), ptr.To(2.0)
		if heterogeneous {
			// Requests .25 + 1.75 and limits .25 + 1.75 permit the aggregate
			// proposal (.5 * 2), but the first pod cannot accept .5.
			r.Recommendations[0].RequestMin, r.Recommendations[0].RequestMax = ptr.To(.25), ptr.To(1.75)
		}
		got := buildRightSizingSavings(r, u).Resources[0]
		want := rightSizingSavingsResource{Cluster: "svc", Resource: "cpu", Before: 2, After: 2, ExcludedContainers: 2}
		if got != want {
			t.Fatalf("multi-pod requests (heterogeneous=%t) = %+v, want %+v", heterogeneous, got, want)
		}
	}
}

func TestRightSizingSavingsExactIdentityRanges(t *testing.T) {
	for _, different := range []string{"cluster", "namespace", "workload", "same identity"} {
		t.Run(different, func(t *testing.T) {
			r, u := savingsFixture()
			if different == "namespace" {
				r.Recommendations[0].Namespace, r.Recommendations[0].Workload, r.Recommendations[0].Container = "ocm-arohcpci-one", "kube-apiserver", "kube-apiserver"
				w := &u.Snapshots[0].Workloads[0]
				w.Namespace, w.Name, w.Containers[0].Name = "ocm-arohcpci-one", "kube-apiserver", "kube-apiserver"
			}
			other := r.Recommendations[0] // Only this row's range contains current=1.
			r.Recommendations[0].RequestMin, r.Recommendations[0].RequestMax = ptr.To(2.0), ptr.To(2.0)
			switch different {
			case "cluster":
				other.Cluster = "other"
			case "namespace":
				other.Namespace = "ocm-arohcpci-two"
			case "workload":
				other.Workload = "other"
			}
			r.Recommendations = append(r.Recommendations, other)
			got := buildRightSizingSavings(r, u).Resources[0]
			want := rightSizingSavingsResource{Cluster: "svc", Resource: "cpu", Before: 1, After: 1, ExcludedContainers: 1}
			if different == "same identity" {
				want.After, want.Reductions, want.ChangedContainers, want.ExcludedContainers = .5, .5, 1, 0
			}
			if got != want {
				t.Fatalf("exact-identity range guard = %+v, want %+v", got, want)
			}
		})
	}
}
