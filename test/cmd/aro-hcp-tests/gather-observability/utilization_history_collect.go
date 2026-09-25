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
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func utilizationRetainHistory(history map[int64]*utilizationMinute, first, last time.Time, warnings []string) []utilizationHistorySample {
	samples := make([]utilizationHistorySample, 0, len(history))
	for at := first; !at.After(last); at = at.Add(time.Minute) {
		minute := history[at.Unix()]
		sample := utilizationHistorySample{Time: at, Expected: []string{}, Nodes: []utilizationHistoryEntry{}, Warnings: slices.Clone(warnings)}
		for cluster := range minute.expected {
			sample.Expected = append(sample.Expected, cluster)
		}
		slices.Sort(sample.Expected)
		for _, node := range minute.nodes {
			entry := utilizationHistoryEntry{
				Cluster: node.Cluster, Name: node.Name, Pool: node.Pool, SKU: node.SKU, Inventory: node.inventory,
				Capacity:    utilizationHistoryResources{CPU: node.Capacity.CPU, Memory: node.ksmMemory, SwiftNIC: node.swiftCapacity},
				Allocatable: utilizationHistoryResources{CPU: node.Allocatable.CPU, Memory: node.Allocatable.Memory, SwiftNIC: node.swiftAllocatable},
				Usage:       utilizationHistoryResources{CPU: node.Usage.CPU, Memory: node.Usage.Memory},
			}
			if node.swiftCapacity != nil || node.swiftAllocatable != nil {
				advertised := true
				entry.SwiftAdvertised = &advertised
			} else if node.inventory && node.Capacity.CPU != nil && node.ksmMemory != nil && node.Allocatable.CPU != nil && node.Allocatable.Memory != nil {
				advertised := false
				entry.SwiftAdvertised = &advertised
			}
			sample.Nodes = append(sample.Nodes, entry)
		}
		sort.Slice(sample.Nodes, func(i, j int) bool {
			a, b := sample.Nodes[i], sample.Nodes[j]
			if a.Cluster != b.Cluster {
				return a.Cluster < b.Cluster
			}
			return a.Name < b.Name
		})
		samples = append(samples, sample)
	}
	return samples
}

func utilizationRequestHistoryQueries(clusters []string) []utilizationQuery {
	parts := make([]string, len(clusters))
	for i, cluster := range clusters {
		parts[i] = regexp.QuoteMeta(cluster)
	}
	selector := `hostedcontrolplane="",cluster=~` + strconv.Quote(strings.Join(parts, "|"))
	var queries []utilizationQuery
	for _, ws := range []string{workspaceSvc, workspaceHcp} {
		metadata := utilizationMetricUnion(selector, "kube_pod_info", "kube_pod_container_info", "kube_pod_status_phase", "kube_pod_status_scheduled")
		if ws == workspaceSvc {
			metadata += ` or on (__name__) kube_state_metrics_list_total{` + selector + `,resource="*v1.Pod",result="success"}`
		}
		queries = append(queries,
			utilizationQuery{"history metadata", ws, `max by (__name__, cluster, namespace, pod, uid, container, node, phase, condition) (` + metadata + `)`},
			utilizationQuery{"history requests", ws, `max by (cluster, namespace, pod, uid, container, resource, node) (kube_pod_container_resource_requests{` + selector + `,resource=~"cpu|memory|aro_openshift_io_swift_nic"})`},
		)
	}
	return queries
}

func utilizationCollectRequestHistory(ctx context.Context, query utilizationQueryFunc, report *utilizationReport, clusters []string) {
	queries := utilizationRequestHistoryQueries(clusters)
	// Release each matrix before fetching the next chunk. Indexing samples once
	// avoids rescanning a full-range matrix for every historical minute.
	for offset := 0; offset < len(report.History); offset += 30 {
		if err := ctx.Err(); err != nil {
			remaining := report.History[offset:]
			warning := fmt.Sprintf("request history %s..%s: remaining collection skipped: %v", remaining[0].Time.Format(time.RFC3339), remaining[len(remaining)-1].Time.Format(time.RFC3339), err)
			report.Warnings = append(report.Warnings, warning)
			for i := range remaining {
				remaining[i].Warnings = append(remaining[i].Warnings, warning)
			}
			return
		}
		chunk := report.History[offset:min(offset+30, len(report.History))]
		first, last := chunk[0].Time, chunk[len(chunk)-1].Time
		results := utilizationQueryBatch(ctx, query, queries, first, last)
		for _, warning := range utilizationQueryWarnings(results) {
			warning = fmt.Sprintf("request history %s..%s: %s", first.Format(time.RFC3339), last.Format(time.RFC3339), warning)
			report.Warnings = append(report.Warnings, warning)
			for i := range chunk {
				chunk[i].Warnings = append(chunk[i].Warnings, warning)
			}
		}
		utilizationBuildRequestHistory(chunk, results, clusters)
	}
}

type utilizationHistoryPod struct {
	uid, node, phase, requestNode    string
	placement, conflict, unscheduled bool
	containers                       map[string]*[3]*float64
}

func utilizationBuildRequestHistory(samples []utilizationHistorySample, results []utilizationQueryResult, clusters []string) {
	type minute struct {
		pods     map[utilizationPodKey]*utilizationHistoryPod
		coverage map[utilizationInstanceKey]bool // cluster and workspace
	}
	minutes := map[int64]*minute{}
	for _, sample := range samples {
		minutes[sample.Time.Unix()] = &minute{pods: map[utilizationPodKey]*utilizationHistoryPod{}, coverage: map[utilizationInstanceKey]bool{}}
	}
	success := map[utilizationInstanceKey]bool{} // workspace and query name
	for _, result := range results {
		success[utilizationInstanceKey{result.query.workspace, result.query.name}] = result.err == nil
		if result.err != nil {
			continue
		}
		for _, series := range result.series {
			m := series.Metric
			if !slices.Contains(clusters, m["cluster"]) || m["hostedcontrolplane"] != "" {
				continue
			}
			for _, value := range series.Values {
				ts, v, ok := utilizationValue(value)
				state := minutes[ts]
				metadata := result.query.name == "history metadata"
				if !ok || state == nil || (metadata && v == 0) {
					continue
				}
				if metadata && m["__name__"] == "kube_state_metrics_list_total" {
					state.coverage[utilizationInstanceKey{m["cluster"], result.query.workspace}] = true
					continue
				}
				if m["namespace"] == "" || m["pod"] == "" {
					continue
				}
				key := utilizationPodKey{m["cluster"], m["namespace"], m["pod"]}
				pod := state.pods[key]
				if pod == nil {
					pod = &utilizationHistoryPod{containers: map[string]*[3]*float64{}}
					state.pods[key] = pod
				}
				uid := strings.ToLower(m["uid"])
				if uid == "" || (pod.uid != "" && pod.uid != uid) {
					pod.conflict = true
				}
				pod.uid = uid
				if metadata {
					switch m["__name__"] {
					case "kube_pod_info":
						state.coverage[utilizationInstanceKey{key.cluster, result.query.workspace}] = true
						if pod.placement && pod.node != m["node"] {
							pod.conflict = true
						}
						pod.node, pod.placement = m["node"], true
					case "kube_pod_status_phase":
						phase := strings.ToLower(m["phase"])
						if pod.phase != "" && pod.phase != phase {
							pod.conflict = true
						}
						pod.phase = phase
					case "kube_pod_status_scheduled":
						pod.unscheduled = pod.unscheduled || m["condition"] == "false"
					}
					if m["__name__"] != "kube_pod_container_info" {
						continue
					}
				}
				name := m["container"]
				if name == "" || name == "POD" {
					continue
				}
				if pod.containers[name] == nil {
					// As in peak collection, runtime inventory or observed regular-
					// container requests can establish a container. This assumes a
					// covered KSM request family is complete; one spec-backed container
					// alone does not prove that all pod containers were exported.
					// Limits are neither queried nor used as inventory evidence here.
					pod.containers[name] = &[3]*float64{}
				}
				if !metadata {
					if m["node"] != "" {
						if pod.requestNode != "" && pod.requestNode != m["node"] {
							pod.conflict = true
						}
						pod.requestNode = m["node"]
					}
					for i, resource := range []string{"cpu", "memory", "aro_openshift_io_swift_nic"} {
						if m["resource"] == resource {
							utilizationMax(&pod.containers[name][i], v)
						}
					}
				}
			}
		}
	}
	for i := range samples {
		sample := &samples[i]
		state := minutes[sample.Time.Unix()]
		unknownClusters := map[string]bool{}
		unknownNodes := map[utilizationNodeKey]bool{}
		totals := map[utilizationNodeKey][3]float64{}
		warnings := map[string]bool{}
		nodes := map[utilizationNodeKey]bool{}
		for _, node := range sample.Nodes {
			nodes[utilizationNodeKey{node.Cluster, node.Name}] = true
		}
		for _, cluster := range clusters {
			// AKS uses one KSM collector whose pod metrics are split by namespace
			// between svc and hcp; its self metrics and system pods route to svc.
			// Require svc evidence conservatively, even when hcp has pod info.
			// With both query families successful, an empty hcp response is then
			// legitimate (service clusters have no HCP namespaces).
			if !state.coverage[utilizationInstanceKey{cluster, workspaceSvc}] {
				unknownClusters[cluster] = true
				warnings[cluster+": shared KSM request coverage unavailable (svc pod inventory or collector heartbeat required; namespace metrics split across svc/hcp)"] = true
			}
			for _, ws := range []string{workspaceSvc, workspaceHcp} {
				if !success[utilizationInstanceKey{ws, "history metadata"}] || !success[utilizationInstanceKey{ws, "history requests"}] {
					unknownClusters[cluster] = true
					warnings[cluster+": request coverage unavailable in "+ws+" (successful metadata and requests queries required in both workspaces)"] = true
				}
			}
		}
		keys := make([]utilizationPodKey, 0, len(state.pods))
		for key := range state.pods {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if a.cluster != b.cluster {
				return a.cluster < b.cluster
			}
			if a.namespace != b.namespace {
				return a.namespace < b.namespace
			}
			return a.pod < b.pod
		})
		for _, key := range keys {
			pod := state.pods[key]
			if pod.conflict || (pod.requestNode != "" && pod.requestNode != pod.node) {
				unknownClusters[key.cluster] = true
				warnings[key.cluster+": request placement or pod UID/phase ambiguous; cluster requests unknown"] = true
				continue
			}
			if pod.phase == "succeeded" || pod.phase == "failed" {
				continue
			}
			if !pod.placement || (pod.node == "" && pod.phase != "pending" && !pod.unscheduled) || (pod.node != "" && pod.unscheduled) || (pod.node != "" && !nodes[utilizationNodeKey{key.cluster, pod.node}]) {
				unknownClusters[key.cluster] = true
				warnings[key.cluster+": request placement unavailable; cluster requests unknown"] = true
				continue
			}
			if pod.node == "" {
				continue
			}
			node := utilizationNodeKey{key.cluster, pod.node}
			if pod.phase == "" || len(pod.containers) == 0 {
				unknownNodes[node] = true
				warnings[key.cluster+"/"+pod.node+": pod phase or container inventory unavailable; requests unknown"] = true
				continue
			}
			names := make([]string, 0, len(pod.containers))
			for name := range pod.containers {
				names = append(names, name)
			}
			slices.Sort(names)
			total := totals[node]
			for _, name := range names {
				for resource, value := range pod.containers[name] {
					// An absent request is zero only after successful queries and
					// inventory coverage; unknown cluster totals are withheld below.
					if value != nil {
						total[resource] += *value
					}
				}
			}
			totals[node] = total
		}
		for j := range sample.Nodes {
			node := &sample.Nodes[j]
			key := utilizationNodeKey{node.Cluster, node.Name}
			if unknownClusters[node.Cluster] || unknownNodes[key] || !node.Inventory {
				continue
			}
			total := totals[key]
			node.Requests = utilizationHistoryResources{CPU: &total[0], Memory: &total[1], SwiftNIC: &total[2]}
		}
		var sorted []string
		for warning := range warnings {
			sorted = append(sorted, warning)
		}
		slices.Sort(sorted)
		sample.Warnings = append(sample.Warnings, sorted...)
	}
}
