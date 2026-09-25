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
	"cmp"
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

type rightSizingReport struct {
	Version         int                         `json:"version"`
	Start           time.Time                   `json:"start"`
	End             time.Time                   `json:"end"`
	Headroom        float64                     `json:"headroom"`
	ChangeThreshold float64                     `json:"changeThreshold"`
	CPUWindow       string                      `json:"cpuWindow"`
	Warnings        []string                    `json:"warnings"`
	Recommendations []rightSizingRecommendation `json:"recommendations"`
}

// Quantities and deltas are per container, in cores or bytes, never fleet totals.
// Replica counts include historical pod UIDs, not concurrent desired replicas.
// Unmatched evidence is retained separately but excluded from replica counts.
// Consumers must skip non-actionable rows even when a suggestion is available.
type rightSizingRecommendation struct {
	Cluster           string   `json:"cluster"`
	Namespace         string   `json:"namespace"`
	Kind              string   `json:"kind"`
	Workload          string   `json:"workload"`
	Container         string   `json:"container"`
	Resource          string   `json:"resource"`
	InitContainer     bool     `json:"initContainer"`
	Replicas          int      `json:"replicas"`
	MeasuredReplicas  int      `json:"measuredReplicas"`
	Peak              *float64 `json:"peak"`
	BurstPeak         *float64 `json:"burstPeak"`
	RequestMin        *float64 `json:"requestMin"`
	RequestMax        *float64 `json:"requestMax"`
	Suggested         *float64 `json:"suggested"`
	SuggestedQuantity string   `json:"suggestedQuantity"`
	Delta             *float64 `json:"delta"`
	Direction         string   `json:"direction"`
	Eligible          bool     `json:"eligible"`
	Actionable        bool     `json:"actionable"`
	// AlertRisk compares the sizing peak, not burst CPU, with the request. It is
	// not alert state: alerts average ratios over 30m, hold 5m, and use CPU rate5m.
	AlertRisk bool     `json:"alertRisk"`
	Warnings  []string `json:"warnings"`
}

// changeThreshold is a CLI-validated fraction in [0, 1], normally 0.1.
func buildRightSizingReport(peaks replicaPeakReport, changeThreshold float64) rightSizingReport {
	r := rightSizingReport{Version: 2, Start: peaks.Start, End: peaks.End, Headroom: 1.2,
		ChangeThreshold: changeThreshold, CPUWindow: peaks.SizingCPUWindow, Warnings: []string{}, Recommendations: []rightSizingRecommendation{}}
	cpuQuery := "cpuSustained"
	if peaks.SizingCPUWindow == "" {
		r.CPUWindow, cpuQuery = peaks.CPUWindow, "cpu"
		r.Warnings = append(r.Warnings, "legacy report: sizing CPU window unavailable; using burst CPU window "+peaks.CPUWindow)
	}
	// Do not copy raw collection errors: they can contain endpoint/config paths.
	if len(peaks.Warnings) != 0 {
		r.Warnings = append(r.Warnings, "replica peak collection reported warnings; inspect the source report")
	}
	normalizeKind := func(kind string) string {
		for _, known := range []string{"Pod", "ReplicaSet", "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "ReplicationController", "DeploymentConfig", "Node"} {
			if strings.EqualFold(kind, known) {
				return known
			}
		}
		return kind
	}
	type owner struct{ kind, name string }
	type ownerKey struct{ cluster, namespace, kind, name, uid string }
	type replicaKey struct{ cluster, namespace, pod, uid, container, unmatched string }
	type replica struct {
		records []replicaPeakContainer
		roles   int // 1: regular; 2: init; 3: conflicting evidence
	}
	owners := map[ownerKey]map[owner]bool{}
	replicas := map[replicaKey]*replica{}
	getReplica := func(k replicaKey) *replica {
		if replicas[k] == nil {
			replicas[k] = &replica{}
		}
		return replicas[k]
	}
	for _, metadata := range peaks.Metadata {
		m := metadata.Labels
		if m["hostedcontrolplane"] != "" {
			continue
		}
		uid := strings.ToLower(m["uid"])
		if (m["metric"] == "kube_pod_container_info" || m["metric"] == "kube_pod_init_container_info") && uid != "" && m["pod"] != "" && m["container"] != "" {
			p := getReplica(replicaKey{m["cluster"], m["namespace"], m["pod"], uid, m["container"], ""})
			if m["metric"] == "kube_pod_init_container_info" {
				p.roles |= 2
			} else {
				p.roles |= 1
			}
		}
		if m["owner_is_controller"] != "" && m["owner_is_controller"] != "true" {
			continue
		}
		k := ownerKey{cluster: m["cluster"], namespace: m["namespace"]}
		switch m["metric"] {
		case "kube_pod_owner":
			k.kind, k.name, k.uid = "Pod", m["pod"], uid
			if uid == "" {
				continue
			}
		case "kube_replicaset_owner":
			k.kind, k.name = "ReplicaSet", m["replicaset"]
		case "kube_job_owner":
			k.kind, k.name = "Job", m["job_name"]
		case "kube_replicationcontroller_owner":
			k.kind, k.name = "ReplicationController", m["replicationcontroller"]
		default:
			continue
		}
		if k.name == "" {
			continue
		}
		if owners[k] == nil {
			owners[k] = map[owner]bool{}
		}
		owners[k][owner{normalizeKind(m["owner_kind"]), m["owner_name"]}] = true
	}
	unmatchedCounts := map[string]int{}
	for _, record := range peaks.Containers {
		if !slices.Contains([]string{"requests", "initRequests", "cpu", "cpuSustained", "memory"}, record.QueryName) {
			continue
		}
		k := replicaKey{record.Cluster, record.Namespace, record.Pod, strings.ToLower(record.PodUID), record.Container, ""}
		if k.uid == "" {
			// This identifies evidence, not an incarnation. Never join UID-less
			// records, even duplicate requests or matching runtime labels. fmt sorts
			// map keys and supports nonfinite summaries that JSON cannot encode.
			identity := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", record))))
			unmatchedCounts[identity]++
			k.unmatched = fmt.Sprintf("%s-%d", identity, unmatchedCounts[identity])
		}
		p := getReplica(k)
		p.records = append(p.records, record)
		switch record.QueryName {
		case "requests":
			p.roles |= 1
		case "initRequests":
			p.roles |= 2
		}
	}
	type groupKey struct {
		cluster, namespace, kind, workload, container string
		init                                          bool
	}
	type member struct {
		records   []replicaPeakContainer
		conflict  bool
		unmatched bool
	}
	groups := map[groupKey][]member{}
	for k, p := range replicas {
		resolved := false
		current := ownerKey{k.cluster, k.namespace, "Pod", k.pod, k.uid}
		seen := map[ownerKey]bool{}
		for k.uid != "" && !seen[current] {
			seen[current] = true
			edges := owners[current]
			if len(edges) != 1 {
				break
			}
			var next owner
			for edge := range edges {
				next = edge
			}
			if next.kind == "" || next.name == "" {
				break
			}
			current = ownerKey{k.cluster, k.namespace, next.kind, next.name, ""}
			if !slices.Contains([]string{"Pod", "ReplicaSet", "Job", "ReplicationController"}, next.kind) {
				resolved = true
				break
			}
		}
		kind, workload := current.kind, current.name
		if !resolved {
			kind, workload = "Pod", k.pod+"@"+k.uid
			if k.uid == "" {
				workload += "unknown-evidence-" + k.unmatched
			}
		}
		roles := []bool{p.roles == 2}
		if p.roles == 3 {
			roles = []bool{false, true}
		}
		for _, init := range roles {
			g := groupKey{k.cluster, k.namespace, kind, workload, k.container, init}
			groups[g] = append(groups[g], member{p.records, p.roles == 3, k.unmatched != ""})
		}
	}
	value := func(summary map[string]float64, stat string) *float64 {
		v, ok := summary[stat]
		if !ok || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	}
	maximum := func(dst **float64, v *float64) {
		if v != nil && (*dst == nil || *v > **dst) {
			copy := *v
			*dst = &copy
		}
	}
	step, err := time.ParseDuration(peaks.GridStep)
	if err != nil || step <= 0 {
		step = 0
	}
	for g, members := range groups {
		for _, resource := range []string{"cpu", "memory"} {
			row := rightSizingRecommendation{Cluster: g.cluster, Namespace: g.namespace, Kind: g.kind, Workload: g.workload,
				Container: g.container, Resource: resource, InitContainer: g.init, Replicas: len(members),
				Eligible: true, Direction: "unknown", Warnings: []string{}}
			warn := func(message string, ineligible bool) {
				row.Warnings = append(row.Warnings, message)
				if ineligible {
					row.Eligible = false
				}
			}
			if g.kind == "Pod" {
				warn("no owner: exact pod UID owner chain unavailable or conflicting", true)
			} else if !slices.Contains([]string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob"}, g.kind) {
				warn("owner unsupported: "+g.kind, true)
			}
			usageQuery, requestQuery := resource, "requests"
			if resource == "cpu" {
				usageQuery = cpuQuery
			}
			if g.init {
				requestQuery = "initRequests"
			}
			for _, q := range peaks.Queries {
				if q.Status == "error" && (q.Cluster == "" || q.Cluster == g.cluster) &&
					(q.Name == "inventory" || q.Name == "metadata" || q.Name == requestQuery || q.Name == usageQuery || (resource == "cpu" && q.Name == "cpu")) {
					warn("query failure: "+q.Name, true)
				}
			}
			for _, p := range members {
				if p.unmatched {
					row.Replicas--
					warn("pod UID unavailable: unjoinable evidence; replica count unknown and excluded from counts", true)
				}
				if p.conflict {
					warn("conflicting init container identity", true)
				}
				var usage, requests []map[string]float64
				var peak, requestMin, requestMax *float64
				type requestRange struct{ min, max float64 }
				ranges := map[string]requestRange{}
				for _, record := range p.records {
					s := record.Summary
					if resource == "cpu" && record.QueryName == "cpu" {
						maximum(&row.BurstPeak, value(s, "max"))
					}
					if record.QueryName == usageQuery {
						usage = append(usage, s)
						maximum(&peak, value(s, "max"))
					}
					if record.QueryName != requestQuery || record.Labels["resource"] != resource {
						continue
					}
					requests = append(requests, s)
					lo, hi := value(s, "min"), value(s, "max")
					if lo == nil || hi == nil || *lo > *hi {
						warn("missing requests: incomplete request range", true)
						continue
					}
					if requestMin == nil || *lo < *requestMin {
						requestMin = lo
					}
					maximum(&requestMax, hi)
					rangeValue := requestRange{*lo, *hi}
					for _, previous := range ranges {
						if previous != rangeValue {
							warn("conflicting workspace request ranges", true)
						}
					}
					ranges[record.Workspace] = rangeValue
				}
				if peak == nil {
					warn("no usage: one or more replicas have no measured peak", true)
				} else {
					if !p.unmatched {
						row.MeasuredReplicas++
					}
					maximum(&row.Peak, peak)
				}
				if requestMin == nil {
					warn("missing requests: one or more replicas have no request range", true)
				} else {
					if row.RequestMin == nil || *requestMin < *row.RequestMin {
						row.RequestMin = requestMin
					}
					maximum(&row.RequestMax, requestMax)
				}
				if !rightSizingCoverage(usage, step) || !rightSizingCoverage(requests, step) {
					warn("low sample coverage: require 10 observations and 90% of each replica's own first..last span for usage and requests", true)
				}
			}
			if row.RequestMin != nil && row.RequestMax != nil && *row.RequestMin != *row.RequestMax {
				warn("request change: observed request range varies across time or replicas", false)
			}
			if row.Peak != nil {
				row.AlertRisk = row.RequestMin != nil && *row.Peak > 1.2*(*row.RequestMin)
				unit, suffix, scale := 0.01, "m", 1000.0
				if resource == "memory" {
					unit, suffix, scale = 10*1024*1024, "Mi", 1.0/(1024*1024)
				}
				suggested := math.Max(1, math.Ceil(*row.Peak*r.Headroom/unit)) * unit
				if math.IsInf(suggested, 0) {
					warn("suggestion exceeds finite numeric range", true)
				} else {
					row.Suggested = &suggested
					row.SuggestedQuantity = fmt.Sprintf("%.0f%s", suggested*scale, suffix)
					if row.RequestMin != nil {
						delta := *row.RequestMin - suggested
						row.Delta, row.Direction = &delta, "matched"
						if delta > 0 {
							row.Direction = "over"
						} else if delta < 0 {
							row.Direction = "under"
						}
						// Strict threshold, with tolerance for float noise at e.g. 10%.
						// Zero requests need no division; threshold zero means any change.
						row.Actionable = row.Eligible && delta != 0 && (row.AlertRisk || *row.RequestMin == 0 ||
							changeThreshold == 0 || math.Abs(delta) / *row.RequestMin > changeThreshold+1e-12)
					}
				}
			}
			slices.Sort(row.Warnings)
			row.Warnings = slices.Compact(row.Warnings)
			r.Recommendations = append(r.Recommendations, row)
		}
	}
	slices.Sort(r.Warnings)
	slices.SortFunc(r.Recommendations, func(a, b rightSizingRecommendation) int {
		if c := cmp.Compare(a.Resource, b.Resource); c != 0 {
			return c
		}
		absDelta := func(v *float64) float64 {
			if v == nil {
				return -1
			}
			return math.Abs(*v)
		}
		if c := cmp.Compare(absDelta(b.Delta), absDelta(a.Delta)); c != 0 {
			return c
		}
		for _, pair := range [][2]string{{a.Cluster, b.Cluster}, {a.Namespace, b.Namespace}, {a.Kind, b.Kind}, {a.Workload, b.Workload}, {a.Container, b.Container}} {
			if c := cmp.Compare(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		if a.InitContainer != b.InitContainer {
			if a.InitContainer {
				return 1
			}
			return -1
		}
		return 0
	})
	return r
}

// Summary counts cannot reveal exactly which observations overlap. Subtract the
// entire possible overlap to prove a lower bound, rather than inventing coverage
// by summing workspaces, exporters, or restarted runtimes.
func rightSizingCoverage(summaries []map[string]float64, step time.Duration) bool {
	if len(summaries) == 0 || step <= 0 {
		return false
	}
	type span struct{ first, last, count float64 }
	spans := make([]span, 0, len(summaries))
	for _, summary := range summaries {
		for _, stat := range []string{"first", "last", "count", "max"} {
			v, ok := summary[stat]
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return false
			}
		}
		s := span{summary["first"], summary["last"], summary["count"]}
		if s.last < s.first || s.count < 1 || s.count != math.Trunc(s.count) || s.count > (s.last-s.first)/step.Seconds()+1 {
			return false
		}
		spans = append(spans, s)
	}
	slices.SortFunc(spans, func(a, b span) int {
		return cmp.Or(cmp.Compare(a.first, b.first), cmp.Compare(a.last, b.last), cmp.Compare(a.count, b.count))
	})
	first, last, count := spans[0].first, spans[0].last, spans[0].count
	for _, s := range spans[1:] {
		overlap := math.Max(0, (math.Min(last, s.last)-s.first)/step.Seconds()+1)
		count = math.Max(math.Max(count, s.count), count+s.count-overlap)
		last = math.Max(last, s.last)
	}
	return count >= 10 && count >= 0.9*((last-first)/step.Seconds()+1)
}
