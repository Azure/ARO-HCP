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
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)

// These fixtures use the collector's normalized evidence contract, including
// independent workspace records, runtime IDs, UID-scoped pod owners, and parent
// controller edges. The two replicas deliberately have very different usage.
func rightSizingFixture() replicaPeakReport {
	end := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	r := replicaPeakReport{Version: 1, Start: end.Add(-time.Hour), End: end,
		GridStep: "60s", CPUWindow: "2m", SizingCPUWindow: "10m"}
	for i, uid := range []string{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"} {
		pod := fmt.Sprintf("api-rs-%d", i)
		for _, query := range []string{"cpu", "cpuSustained", "memory", "requests"} {
			resources := []string{""}
			if query == "requests" {
				resources = []string{"cpu", "memory"}
			}
			for _, resource := range resources {
				maximum := 0.01
				if i == 1 {
					maximum = 0.02
				}
				switch query {
				case "cpu":
					maximum *= 10
				case "memory":
					maximum = float64(10+i*15) * 1024 * 1024
				case "requests":
					maximum = 0.1
					if resource == "memory" {
						maximum = 100 * 1024 * 1024
					}
				}
				labels := map[string]string{"resource": resource, "id": "/kubepods/pod" + uid + "/runtime"}
				summary := map[string]float64{"max": maximum, "first": float64(r.Start.Unix()), "last": float64(r.End.Unix()), "count": 61}
				if query == "requests" {
					summary["min"] = maximum
				}
				r.Containers = append(r.Containers, replicaPeakContainer{Cluster: "cluster", Namespace: "ns", Pod: pod, PodUID: uid,
					Container: "app", Workspace: "svc", QueryName: query, Labels: labels, Summary: summary})
			}
		}
		r.Metadata = append(r.Metadata, replicaPeakMetadata{Workspace: "hcp", Labels: map[string]string{
			"metric": "kube_pod_owner", "cluster": "cluster", "namespace": "ns", "pod": pod, "uid": uid,
			"owner_kind": "replicaset", "owner_name": "api-rs", "owner_is_controller": "true",
		}})
	}
	r.Metadata = append(r.Metadata, replicaPeakMetadata{Workspace: "svc", Labels: map[string]string{
		"metric": "kube_replicaset_owner", "cluster": "cluster", "namespace": "ns", "replicaset": "api-rs",
		"owner_kind": "deployment", "owner_name": "api", "owner_is_controller": "true",
	}})
	return r
}

func rightSizingRow(t *testing.T, report rightSizingReport, resource string) rightSizingRecommendation {
	t.Helper()
	var rows []rightSizingRecommendation
	for _, row := range report.Recommendations {
		if row.Resource == resource {
			rows = append(rows, row)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("expected one %s recommendation, got %+v", resource, rows)
	}
	return rows[0]
}

func rightSizingNumber(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9*math.Max(1, math.Abs(want)) {
		t.Fatalf("%s: got %v, want %g", name, got, want)
	}
}

func TestRightSizingReplicaPeaks(t *testing.T) {
	p := rightSizingFixture()
	// A duplicate request source must not double requests, replicas, or coverage.
	for _, record := range slices.Clone(p.Containers) {
		if record.QueryName == "requests" {
			record.Workspace = "hcp"
			p.Containers = append(p.Containers, record)
		}
	}
	r := buildRightSizingReport(p, 0.1)
	if r.Version != 1 || r.Headroom != 1.2 || r.ChangeThreshold != 0.1 || r.CPUWindow != "10m" || r.Start != p.Start || r.End != p.End || len(r.Warnings) != 0 {
		t.Fatalf("unexpected report envelope: %+v", r)
	}
	for _, resource := range []string{"cpu", "memory"} {
		row := rightSizingRow(t, r, resource)
		if row.Kind != "Deployment" || row.Workload != "api" || row.Replicas != 2 || row.MeasuredReplicas != 2 || !row.Eligible || !row.Actionable || row.AlertRisk || len(row.Warnings) != 0 || row.Direction != "over" {
			t.Fatalf("replica ownership/eligibility incorrect: %+v", row)
		}
		if resource == "cpu" {
			rightSizingNumber(t, "CPU peak is maximum, not average", row.Peak, 0.02)
			rightSizingNumber(t, "burst peak", row.BurstPeak, 0.2)
			rightSizingNumber(t, "request min", row.RequestMin, 0.1)
			rightSizingNumber(t, "request max", row.RequestMax, 0.1)
			rightSizingNumber(t, "suggested", row.Suggested, 0.02)
			rightSizingNumber(t, "delta", row.Delta, 0.08)
			if row.SuggestedQuantity != "20m" {
				t.Fatalf("expected 20m, got %s", row.SuggestedQuantity)
			}
		} else {
			rightSizingNumber(t, "memory peak", row.Peak, 25*1024*1024)
			rightSizingNumber(t, "memory suggested", row.Suggested, 30*1024*1024)
			if row.BurstPeak != nil || row.SuggestedQuantity != "30Mi" {
				t.Fatalf("memory must not have CPU burst evidence: %+v", row)
			}
		}
	}
}

func TestRightSizingEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*replicaPeakReport)
		warning  string
		eligible bool
	}{
		{"missing request", func(p *replicaPeakReport) {
			p.Containers = slices.DeleteFunc(p.Containers, func(c replicaPeakContainer) bool {
				return c.Pod == "api-rs-0" && c.QueryName == "requests" && c.Labels["resource"] == "cpu"
			})
		}, "missing requests", false},
		{"missing usage", func(p *replicaPeakReport) {
			p.Containers = slices.DeleteFunc(p.Containers, func(c replicaPeakContainer) bool { return c.Pod == "api-rs-0" && c.QueryName == "cpuSustained" })
		}, "no usage", false},
		{"sparse usage", func(p *replicaPeakReport) { p.Containers[1].Summary["count"] = 54 }, "low sample coverage", false},
		{"sparse request", func(p *replicaPeakReport) { p.Containers[3].Summary["count"] = 54 }, "low sample coverage", false},
		{"incomplete usage", func(p *replicaPeakReport) { delete(p.Containers[1].Summary, "max") }, "low sample coverage", false},
		{"incomplete request", func(p *replicaPeakReport) { delete(p.Containers[3].Summary, "min") }, "missing requests", false},
		{"workspace conflict", func(p *replicaPeakReport) {
			c := p.Containers[3]
			c.Workspace, c.Summary = "hcp", maps.Clone(c.Summary)
			c.Summary["min"] = 0.05 // Same max still conflicts with a different range.
			p.Containers = append(p.Containers, c)
		}, "conflicting workspace request ranges", false},
		{"request change across replicas", func(p *replicaPeakReport) {
			p.Containers[3].Summary["min"], p.Containers[3].Summary["max"] = 0.05, 0.05
		}, "request change", true},
		{"request change over time", func(p *replicaPeakReport) { p.Containers[3].Summary["min"] = 0.05 }, "request change", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := rightSizingFixture()
			tc.mutate(&p)
			r := buildRightSizingReport(p, 0.1)
			cpu := rightSizingRow(t, r, "cpu")
			if cpu.Eligible != tc.eligible || cpu.Actionable != tc.eligible || !strings.Contains(strings.Join(cpu.Warnings, " "), tc.warning) || cpu.Suggested == nil {
				t.Fatalf("wrong eligibility/warnings, or lost inspectable suggestion: %+v", cpu)
			}
			if memory := rightSizingRow(t, r, "memory"); !memory.Eligible {
				t.Fatalf("CPU evidence must not poison memory: %+v", memory)
			}
		})
	}
}

func TestRightSizingQueryFailuresAndLegacy(t *testing.T) {
	for _, query := range []string{"cpuSustained", "cpu", "memory", "requests", "initRequests", "metadata", "inventory"} {
		for _, cluster := range []string{"cluster", "other", ""} {
			t.Run(query+"/"+cluster, func(t *testing.T) {
				p := rightSizingFixture()
				p.Queries = []replicaPeakQuery{{Cluster: cluster, Workspace: "hcp", Name: query, Status: "error", Error: "/secret/config.yaml"}}
				r := buildRightSizingReport(p, 0.1)
				for _, resource := range []string{"cpu", "memory"} {
					row := rightSizingRow(t, r, resource)
					failure := cluster != "other" && (query == "metadata" || query == "inventory" || query == "requests" ||
						(resource == "cpu" && (query == "cpu" || query == "cpuSustained")) || (resource == "memory" && query == "memory"))
					if row.Eligible == failure || (failure && !strings.Contains(strings.Join(row.Warnings, " "), "query failure")) {
						t.Fatalf("wrong failure scope for %s: %+v", query, row)
					}
				}
				b, err := json.Marshal(r)
				if err != nil || strings.Contains(string(b), "config.yaml") {
					t.Fatalf("invalid or path-leaking JSON: %s, %v", b, err)
				}
			})
		}
	}
	for _, legacy := range []bool{false, true} {
		p := rightSizingFixture()
		p.Containers = slices.DeleteFunc(p.Containers, func(c replicaPeakContainer) bool { return c.QueryName == "cpuSustained" })
		if legacy {
			p.SizingCPUWindow = ""
		} else {
			p.Queries = []replicaPeakQuery{{Cluster: "cluster", Name: "cpuSustained", Status: "error"}}
		}
		r := buildRightSizingReport(p, 0.1)
		cpu := rightSizingRow(t, r, "cpu")
		if legacy {
			if r.CPUWindow != "2m" || !strings.Contains(strings.Join(r.Warnings, " "), "legacy") || !cpu.Eligible {
				t.Fatalf("legacy fallback must be explicit: %+v", r)
			}
			rightSizingNumber(t, "legacy peak", cpu.Peak, 0.2)
		} else if r.CPUWindow != "10m" || cpu.Peak != nil || cpu.Suggested != nil || cpu.Delta != nil || cpu.Eligible || cpu.Direction != "unknown" || cpu.BurstPeak == nil {
			t.Fatalf("failed sustained query must never fall back to burst: %+v", cpu)
		}
	}
}

func TestRightSizingRestartsAndCoverage(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		p := rightSizingFixture()
		original := &p.Containers[1]
		original.Summary["count"], original.Summary["last"] = 6, float64(p.Start.Add(5*time.Minute).Unix())
		restart := *original
		restart.Labels, restart.Summary = maps.Clone(original.Labels), maps.Clone(original.Summary)
		restart.Labels["id"] = "/runtime-restarted"
		restart.Summary["max"] = 0.04
		if !overlap {
			restart.Summary["first"], restart.Summary["last"] = float64(p.Start.Add(6*time.Minute).Unix()), float64(p.Start.Add(11*time.Minute).Unix())
		}
		p.Containers = append(p.Containers, restart)
		row := rightSizingRow(t, buildRightSizingReport(p, 0.1), "cpu")
		if row.Replicas != 2 || row.MeasuredReplicas != 2 || row.Eligible == overlap {
			t.Fatalf("restarts must not add replicas or overlapping counts (overlap=%v): %+v", overlap, row)
		}
		rightSizingNumber(t, "maximum across runtimes", row.Peak, 0.04)
		if row.SuggestedQuantity != "50m" {
			t.Fatalf("restart peak lost: %+v", row)
		}
	}
	for _, tc := range []struct {
		name           string
		count, minutes float64
		want           bool
	}{
		{"minimum", 10, 9, true}, {"too short", 9, 8, false},
		{"ninety percent", 18, 19, true}, {"below ninety percent", 17, 19, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := rightSizingFixture()
			// A short-lived historical pod is judged by its own span, not the report hour.
			for _, c := range p.Containers {
				c.Summary["count"], c.Summary["last"] = tc.count, float64(p.Start.Unix())+tc.minutes*60
			}
			if row := rightSizingRow(t, buildRightSizingReport(p, 0.1), "cpu"); row.Eligible != tc.want {
				t.Fatalf("own-span coverage incorrect: %+v", row)
			}
		})
	}
}

func TestRightSizingUnknownOwners(t *testing.T) {
	for _, reason := range []string{"missing metadata", "missing UID", "wrong UID", "missing parent", "conflicting parent", "cycle"} {
		t.Run(reason, func(t *testing.T) {
			p := rightSizingFixture()
			switch reason {
			case "missing metadata":
				p.Metadata = nil
			case "missing UID":
				for i := range p.Containers {
					p.Containers[i].PodUID = ""
				}
			case "wrong UID":
				for _, m := range p.Metadata {
					m.Labels["uid"] = "not-the-pod"
				}
			case "missing parent":
				p.Metadata = p.Metadata[:2]
			case "conflicting parent":
				m := p.Metadata[2]
				m.Workspace, m.Labels = "hcp", maps.Clone(m.Labels)
				m.Labels["owner_name"] = "different"
				p.Metadata = append(p.Metadata, m)
			case "cycle":
				p.Metadata[2].Labels["owner_kind"], p.Metadata[2].Labels["owner_name"] = "ReplicaSet", "api-rs"
			}
			r := buildRightSizingReport(p, 0.1)
			wantRows, wantReplicas := 4, 1
			if reason == "missing UID" {
				wantRows, wantReplicas = 2*len(p.Containers), 0
			}
			if len(r.Recommendations) != wantRows {
				t.Fatalf("unresolved pods must not collapse into guessed deployment: %+v", r)
			}
			for _, row := range r.Recommendations {
				if row.Kind != "Pod" || !strings.HasPrefix(row.Workload, "api-rs-") || row.Eligible || row.Replicas != wantReplicas || !strings.Contains(strings.Join(row.Warnings, " "), "no owner") {
					t.Fatalf("unknown identity not retained independently: %+v", row)
				}
			}
		})
	}
	// A reused name with distinct UIDs must not inherit the current pod's owner.
	p := rightSizingFixture()
	old := p.Containers[1]
	old.PodUID = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	p.Containers = append(p.Containers, old)
	r := buildRightSizingReport(p, 0.1)
	if len(r.Recommendations) != 4 {
		t.Fatalf("historical UID incorrectly name-joined: %+v", r)
	}
}

func TestRightSizingUnmatchedEvidence(t *testing.T) {
	for _, query := range []string{"cpuSustained", "memory"} {
		t.Run(query, func(t *testing.T) {
			p := rightSizingFixture()
			known := buildRightSizingReport(p, 0.1)
			var unmatched []replicaPeakContainer
			for i, runtime := range []string{"/unknown-old", "/unknown-new"} {
				c := p.Containers[1]
				c.PodUID, c.QueryName = "", query
				c.Labels = map[string]string{"id": runtime, "node": "node", "instance": "node:10250"}
				// These spans would pass coverage if incorrectly combined by name.
				first, count := float64(p.Start.Unix())+float64(i*4*60), float64(4+i*2)
				c.Summary = map[string]float64{"max": float64(i+1) * 10, "first": first, "last": first + (count-1)*60, "count": count}
				unmatched = append(unmatched, c)
			}
			request := p.Containers[3]
			request.PodUID, request.Labels = "", map[string]string{"resource": "cpu"}
			if query == "memory" {
				request.Labels["resource"] = "memory"
			}
			// Even identical UID-less request records remain independent.
			unmatched = append(unmatched, request, request)
			p.Containers = append(p.Containers, unmatched...)
			before, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			r := buildRightSizingReport(p, 0.1)
			if len(r.Recommendations) != len(known.Recommendations)+2*len(unmatched) {
				t.Fatalf("unmatched evidence was combined: %+v", r)
			}
			seen := map[string]bool{}
			var measured []float64
			for _, row := range r.Recommendations {
				if row.Kind != "Pod" {
					want, _ := json.Marshal(rightSizingRow(t, known, row.Resource))
					got, _ := json.Marshal(row)
					if string(got) != string(want) {
						t.Fatalf("unmatched evidence affected known UID group: %s, want %s", got, want)
					}
					continue
				}
				key := row.Workload + "/" + row.Resource
				if seen[key] || !strings.HasPrefix(row.Workload, "api-rs-0@unknown-evidence-") {
					t.Fatalf("unmatched evidence needs a unique workload identity: %+v", row)
				}
				seen[key] = true
				if row.Eligible || row.Actionable || row.AlertRisk || row.Delta != nil || row.Direction != "unknown" || row.Replicas != 0 || row.MeasuredReplicas != 0 || !strings.Contains(strings.Join(row.Warnings, " "), "unjoinable evidence") {
					t.Fatalf("unmatched evidence must not claim a replica or recommendation eligibility: %+v", row)
				}
				if row.Peak != nil {
					measured = append(measured, *row.Peak)
					if row.RequestMin != nil || row.RequestMax != nil || row.Suggested == nil || !strings.Contains(strings.Join(row.Warnings, " "), "low sample coverage") {
						t.Fatalf("unmatched usage joined request/coverage evidence or lost suggestion: %+v", row)
					}
				}
			}
			slices.Sort(measured)
			if !slices.Equal(measured, []float64{10, 20}) {
				t.Fatalf("independent runtime peaks lost: %v", measured)
			}
			after, err := json.Marshal(p)
			if err != nil || string(after) != string(before) {
				t.Fatalf("raw evidence was mutated: %v", err)
			}
			want, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			for range 5 {
				slices.Reverse(p.Containers)
				slices.Reverse(p.Metadata)
				got, err := json.Marshal(buildRightSizingReport(p, 0.1))
				if err != nil || string(got) != string(want) {
					t.Fatalf("unmatched evidence identity/order changed JSON: %s, %v", got, err)
				}
			}
		})
	}
}

func TestRightSizingOwnerChainsAndInitContainers(t *testing.T) {
	for _, tc := range []struct{ kind, metric, label, parent string }{
		{"job", "kube_job_owner", "job_name", "CronJob"},
		{"replicationcontroller", "kube_replicationcontroller_owner", "replicationcontroller", "DeploymentConfig"},
		{"StatefulSet", "", "", "StatefulSet"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			p := rightSizingFixture()
			for i := range 2 {
				p.Metadata[i].Labels["owner_kind"] = tc.kind
			}
			if tc.metric == "" {
				p.Metadata = p.Metadata[:2]
			} else {
				m := p.Metadata[2].Labels
				m["metric"], m[tc.label], m["owner_kind"] = tc.metric, "api-rs", strings.ToLower(tc.parent)
			}
			for _, c := range slices.Clone(p.Containers) {
				c.Container = "bootstrap"
				if c.QueryName == "requests" {
					c.QueryName = "initRequests"
				}
				p.Containers = append(p.Containers, c)
			}
			r := buildRightSizingReport(p, 0.1)
			if len(r.Recommendations) != 4 {
				t.Fatalf("init and regular containers must stay separate: %+v", r)
			}
			for _, row := range r.Recommendations {
				eligible := tc.parent != "DeploymentConfig"
				if row.Eligible != eligible || row.Actionable != eligible || row.Kind != tc.parent || row.InitContainer != (row.Container == "bootstrap") || row.Suggested == nil {
					t.Fatalf("owner chain/init classification incorrect: %+v", row)
				}
				if !eligible && !slices.Contains(row.Warnings, "owner unsupported: DeploymentConfig") {
					t.Fatalf("unsupported owner must have an explicit warning: %+v", row)
				}
			}
		})
	}
}

func TestRightSizingOwnerEligibility(t *testing.T) {
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob", "CustomController", "Node", "DeploymentConfig", "ReplicaSet", "Job"} {
		t.Run(kind, func(t *testing.T) {
			p := rightSizingFixture()
			p.Metadata = p.Metadata[:2]
			for _, m := range p.Metadata {
				m.Labels["owner_kind"] = kind
			}
			r := buildRightSizingReport(p, 0.1)
			// No parent edge cannot distinguish a standalone RS/Job from missing
			// metadata. Keep these pod-specific and unresolved, not actionable.
			unresolved := kind == "ReplicaSet" || kind == "Job"
			eligible := slices.Contains([]string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}, kind)
			wantRows := 2
			if unresolved {
				wantRows = 4
			}
			if len(r.Recommendations) != wantRows {
				t.Fatalf("unexpected grouping for %s: %+v", kind, r.Recommendations)
			}
			for _, row := range r.Recommendations {
				if row.Eligible != eligible || row.Actionable != eligible || row.Suggested == nil || row.SuggestedQuantity == "" {
					t.Fatalf("owner eligibility or inspectable suggestion incorrect: %+v", row)
				}
				if unresolved {
					if row.Kind != "Pod" || row.Replicas != 1 || !strings.Contains(strings.Join(row.Warnings, " "), "no owner") {
						t.Fatalf("missing parent metadata must remain unresolved: %+v", row)
					}
				} else if row.Kind != kind || row.Replicas != 2 || (!eligible && !slices.Contains(row.Warnings, "owner unsupported: "+kind)) {
					t.Fatalf("resolved owner identity/warning incorrect: %+v", row)
				}
			}
		})
	}
}

func TestRightSizingMissingData(t *testing.T) {
	for _, mode := range []string{"request-only", "usage-only", "metadata-only", "init-usage-only"} {
		t.Run(mode, func(t *testing.T) {
			p := rightSizingFixture()
			p.Containers = slices.DeleteFunc(p.Containers, func(c replicaPeakContainer) bool {
				if mode == "metadata-only" {
					return true
				}
				return (c.QueryName == "requests") != (mode == "request-only")
			})
			if mode == "metadata-only" || mode == "init-usage-only" {
				for i := range 2 {
					m := maps.Clone(p.Metadata[i].Labels)
					m["container"], m["metric"] = "app", "kube_pod_container_info"
					if mode == "init-usage-only" {
						m["metric"] = "kube_pod_init_container_info"
					}
					p.Metadata = append(p.Metadata, replicaPeakMetadata{Workspace: "svc", Labels: m})
				}
			}
			r := buildRightSizingReport(p, 0.1)
			for _, row := range r.Recommendations {
				if row.Eligible || row.Actionable || row.AlertRisk || row.Delta != nil || row.Direction != "unknown" || row.Replicas != 2 || len(row.Warnings) == 0 {
					t.Fatalf("missing data must remain unknown/ineligible: %+v", row)
				}
				if mode == "request-only" || mode == "metadata-only" {
					if row.Peak != nil || row.Suggested != nil || row.SuggestedQuantity != "" || row.MeasuredReplicas != 0 {
						t.Fatalf("invented usage: %+v", row)
					}
				} else if row.Peak == nil || row.RequestMin != nil || row.Suggested == nil || row.MeasuredReplicas != 2 {
					t.Fatalf("usage-only measurements lost: %+v", row)
				}
				if mode == "init-usage-only" && !row.InitContainer {
					t.Fatalf("init metadata ignored: %+v", row)
				}
			}
		})
	}
}

func TestRightSizingRounding(t *testing.T) {
	for _, tc := range []struct {
		peak        float64
		cpu, memory string
	}{
		{0, "10m", "10Mi"}, {0.001, "10m", "10Mi"},
		{12, "10m", "10Mi"}, {12.4, "20m", "20Mi"},
		{12.5, "20m", "20Mi"}, // 15 rounds up, not banker's rounding.
		{20, "20m", "20Mi"}, {21, "30m", "30Mi"}, {25, "30m", "30Mi"},
		{187.5, "230m", "230Mi"}, // CPU arithmetic lands just below the 22.5 tie.
		{math.Nextafter(187.5, 0), "230m", "230Mi"},
		{math.Nextafter(187.5, math.Inf(1)), "230m", "230Mi"},
		{187.5 - 1e-8, "220m", "220Mi"}, // Genuine sides outside tie tolerance.
		{187.5 + 1e-8, "230m", "230Mi"},
	} {
		t.Run(fmt.Sprint(tc.peak), func(t *testing.T) {
			p := rightSizingFixture()
			for _, c := range p.Containers {
				if c.QueryName == "cpuSustained" {
					c.Summary["max"] = tc.peak / 1000
				}
				if c.QueryName == "memory" {
					c.Summary["max"] = tc.peak * 1024 * 1024
				}
			}
			r := buildRightSizingReport(p, 0.1)
			if cpu, memory := rightSizingRow(t, r, "cpu"), rightSizingRow(t, r, "memory"); cpu.SuggestedQuantity != tc.cpu || memory.SuggestedQuantity != tc.memory || !cpu.Eligible || !memory.Eligible {
				t.Fatalf("nearest ten with one headroom factor and minimum ten: CPU=%+v memory=%+v", cpu, memory)
			}
		})
	}
	for _, request := range []float64{0, 0.02, 0.1} {
		p := rightSizingFixture()
		for _, c := range p.Containers {
			if c.QueryName == "requests" && c.Labels["resource"] == "cpu" {
				c.Summary["min"], c.Summary["max"] = request, request
			}
		}
		row := rightSizingRow(t, buildRightSizingReport(p, 0.1), "cpu")
		want := "matched"
		if request < 0.02 {
			want = "under"
		} else if request > 0.02 {
			want = "over"
		}
		if row.Direction != want || !row.Eligible {
			t.Fatalf("zero/matched request direction incorrect: %+v", row)
		}
	}
}

func TestRightSizingJSONAndOrder(t *testing.T) {
	p := rightSizingFixture()
	// Multiple owners, tied deltas, and different resources exercise tie breaking.
	for _, c := range slices.Clone(p.Containers) {
		c.Cluster = "other"
		c.Summary = maps.Clone(c.Summary)
		if c.QueryName == "requests" {
			c.Summary["min"] *= 2
			c.Summary["max"] *= 2
		}
		p.Containers = append(p.Containers, c)
	}
	for _, m := range slices.Clone(p.Metadata) {
		m.Labels = maps.Clone(m.Labels)
		m.Labels["cluster"] = "other"
		p.Metadata = append(p.Metadata, m)
	}
	r := buildRightSizingReport(p, 0.1)
	want, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		slices.Reverse(p.Containers)
		slices.Reverse(p.Metadata)
		got, err := json.Marshal(buildRightSizingReport(p, 0.1))
		if err != nil || string(got) != string(want) {
			t.Fatalf("input/map iteration order changed JSON: %s, %v", got, err)
		}
	}
	for i, row := range r.Recommendations {
		if i > 0 {
			previous := r.Recommendations[i-1]
			if row.Resource < previous.Resource || (row.Resource == previous.Resource && math.Abs(*row.Delta) > math.Abs(*previous.Delta)) {
				t.Fatalf("recommendations not ordered by resource then absolute delta: %+v", r.Recommendations)
			}
		}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(want, &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 8 || string(object["warnings"]) != "[]" || string(object["changeThreshold"]) != "0.1" {
		t.Fatalf("unexpected envelope fields: %s", want)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(object["recommendations"], &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows[0]) != 21 || string(rows[0]["warnings"]) != "[]" || string(rows[0]["actionable"]) != "true" || string(rows[0]["alertRisk"]) != "false" {
		t.Fatalf("unexpected row schema: %s", object["recommendations"])
	}
	empty, err := json.Marshal(buildRightSizingReport(replicaPeakReport{SizingCPUWindow: "10m"}, 0.1))
	if err != nil || !strings.Contains(string(empty), `"recommendations":[]`) || !strings.Contains(string(empty), `"warnings":[]`) {
		t.Fatalf("empty arrays must not serialize as null: %s, %v", empty, err)
	}
}

func TestRightSizingActionableAndAlertRisk(t *testing.T) {
	p := rightSizingFixture()
	for _, c := range p.Containers {
		if c.QueryName == "requests" && c.Labels["resource"] == "cpu" {
			c.Summary["min"], c.Summary["max"] = 0.02, 0.02
		}
	}
	if row := rightSizingRow(t, buildRightSizingReport(p, 0.1), "cpu"); row.AlertRisk || row.Actionable || row.Direction != "matched" || *row.BurstPeak <= 1.2*(*row.RequestMin) {
		t.Fatalf("burst-only risk must not make a matched sustained recommendation actionable: %+v", row)
	}
	for _, legacy := range []bool{false, true} {
		for _, tc := range []struct {
			name                         string
			peak, request, threshold     float64 // peaks/requests in m or Mi
			actionable, risk, ineligible bool
			direction                    string
		}{
			{"over below boundary", 75, 99, 0.1, false, false, false, "over"},
			{"over exact boundary", 75, 100, 0.1, false, false, false, "over"},
			{"over float boundary", 75, math.Nextafter(100, math.Inf(1)), 0.1, false, false, false, "over"},
			{"over above boundary", 75, 100.001, 0.1, true, false, false, "over"},
			{"under below boundary", 92, 101, 0.1, false, false, false, "under"},
			{"under exact boundary", 92, 100, 0.1, false, false, false, "under"},
			{"under above boundary", 92, 99.999, 0.1, true, false, false, "under"},
			{"custom threshold", 75, 100, 0.05, true, false, false, "over"},
			{"zero threshold", 75, 90.00001, 0, true, false, false, "over"},
			{"matched", 75, 90, 0, false, false, false, "matched"},
			{"maximum threshold boundary", 0, 5, 1, false, false, false, "under"},
			{"risk bypass", 124, 100, 0.5, true, true, false, "under"},
			{"risk bypass maximum threshold", 124, 100, 1, true, true, false, "under"},
			{"risk does not bypass eligibility", 124, 100, 1, false, true, true, "under"},
			{"risk exact ratio", 12, 10, 0.1, false, false, false, "matched"},
			{"risk above ratio", 12.4, 10, 1, true, true, false, "under"},
			{"zero request positive peak", 1, 0, 1, true, true, false, "under"},
			{"zero request zero peak", 0, 0, 1, true, false, false, "under"},
		} {
			t.Run(fmt.Sprintf("legacy=%v/%s", legacy, tc.name), func(t *testing.T) {
				p := rightSizingFixture()
				if legacy {
					p.SizingCPUWindow = ""
				}
				for _, c := range p.Containers {
					scale := 0.001
					if c.QueryName == "memory" || c.Labels["resource"] == "memory" {
						scale = 1024 * 1024
					}
					if c.QueryName == "requests" {
						c.Summary["min"], c.Summary["max"] = tc.request*scale, tc.request*scale
					} else {
						c.Summary["max"] = tc.peak * scale
					}
					if tc.ineligible {
						c.Summary["count"] = 9
					}
				}
				r := buildRightSizingReport(p, tc.threshold)
				if r.ChangeThreshold != tc.threshold {
					t.Fatalf("threshold not retained: got %g, want %g", r.ChangeThreshold, tc.threshold)
				}
				for _, resource := range []string{"cpu", "memory"} {
					row := rightSizingRow(t, r, resource)
					if row.Actionable != tc.actionable || row.AlertRisk != tc.risk || row.Direction != tc.direction || row.Eligible == tc.ineligible {
						t.Fatalf("unexpected decision for %s: actionable=%v risk=%v direction=%s eligible=%v; want actionable=%v risk=%v direction=%s eligible=%v",
							resource, row.Actionable, row.AlertRisk, row.Direction, row.Eligible, tc.actionable, tc.risk, tc.direction, !tc.ineligible)
					}
				}
			})
		}
	}
}

func TestRightSizingEmptySustainedAndInitSeparation(t *testing.T) {
	p := rightSizingFixture()
	p.Containers = slices.DeleteFunc(p.Containers, func(c replicaPeakContainer) bool { return c.QueryName == "cpuSustained" })
	p.Queries = []replicaPeakQuery{{Cluster: "cluster", Name: "cpuSustained", Status: "empty"}}
	if row := rightSizingRow(t, buildRightSizingReport(p, 0.1), "cpu"); row.Peak != nil || row.Suggested != nil || row.Eligible || row.BurstPeak == nil {
		t.Fatalf("empty sustained collection must not fall back: %+v", row)
	}

	p = rightSizingFixture()
	for i := range p.Containers {
		c := &p.Containers[i]
		if c.Pod == "api-rs-0" && c.QueryName == "requests" {
			c.QueryName = "initRequests"
		}
	}
	r := buildRightSizingReport(p, 0.1)
	if len(r.Recommendations) != 4 {
		t.Fatalf("same container name must remain separated by init flag: %+v", r)
	}
	for _, row := range r.Recommendations {
		if row.Replicas != 1 || row.MeasuredReplicas != 1 || !row.Eligible {
			t.Fatalf("init/regular replica sets were combined: %+v", row)
		}
	}
	// Conflicting metadata cannot silently reclassify the init container.
	m := maps.Clone(p.Metadata[0].Labels)
	m["metric"], m["container"] = "kube_pod_container_info", "app"
	p.Metadata = append(p.Metadata, replicaPeakMetadata{Workspace: "hcp", Labels: m})
	for _, row := range buildRightSizingReport(p, 0.1).Recommendations {
		if row.Eligible || !strings.Contains(strings.Join(row.Warnings, " "), "conflicting init container identity") {
			t.Fatalf("ambiguous init identity must not be eligible: %+v", row)
		}
	}
}

func TestRightSizingCollectedEvidence(t *testing.T) {
	fixture := rightSizingFixture()
	data := map[string][]PrometheusResult{}
	for _, c := range fixture.Containers {
		labels := maps.Clone(c.Labels)
		labels["cluster"], labels["namespace"], labels["pod"], labels["container"] = c.Cluster, c.Namespace, c.Pod, c.Container
		if c.QueryName == "requests" {
			labels["uid"] = c.PodUID
		}
		for stat, value := range c.Summary {
			m := maps.Clone(labels)
			m["statistic"] = stat
			data[c.Workspace+"/"+c.QueryName] = append(data[c.Workspace+"/"+c.QueryName], replicaPeakTestSeries(m, value))
		}
	}
	for _, m := range fixture.Metadata {
		data[m.Workspace+"/metadata"] = append(data[m.Workspace+"/metadata"], replicaPeakTestSeries(m.Labels, "1"))
	}
	peaks := collectReplicaPeaks(context.Background(), fixture.Start, fixture.End, fixture.End, replicaPeakTestQuery(t, data, ""))
	if len(peaks.Warnings) != 0 {
		t.Fatalf("fixture must survive real collector without warnings: %v", peaks.Warnings)
	}
	r := buildRightSizingReport(peaks, 0.1)
	if cpu := rightSizingRow(t, r, "cpu"); !cpu.Eligible || cpu.SuggestedQuantity != "20m" || cpu.Replicas != 2 {
		t.Fatalf("collected evidence did not produce expected sizing: %+v", cpu)
	}
}
