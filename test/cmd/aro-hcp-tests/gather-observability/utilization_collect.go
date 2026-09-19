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
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"
)

const utilizationTimeout = 10 * time.Minute

func (o Options) collectUtilization(ctx context.Context, workspaces map[string]*workspaceData) utilizationReport {
	client := &http.Client{Timeout: 30 * time.Second}
	return collectUtilization(ctx, o.TimeWindow.Start, o.TimeWindow.End, time.Now(), func(ctx context.Context, workspace, expression string, start, end time.Time) ([]PrometheusResult, error) {
		ws := workspaces[workspace]
		if ws == nil || ws.PromEndpoint == "" {
			return nil, fmt.Errorf("workspace endpoint unavailable")
		}
		response, err := queryRange(ctx, client, o.cred, ws.PromEndpoint, expression, start, end, "60s")
		if err != nil {
			return nil, err
		}
		return response.Data.Result, nil
	})
}

type utilizationNodeKey struct{ cluster, node string }
type utilizationInstanceKey struct{ cluster, instance string }
type utilizationHistoryNode struct {
	utilizationNode
	inventory        bool
	total, available *float64
	ksmMemory        *float64
}
type utilizationMinute struct {
	nodes    map[utilizationNodeKey]*utilizationHistoryNode
	expected map[string]bool
}

func collectUtilization(ctx context.Context, start, end, now time.Time, query utilizationQueryFunc) utilizationReport {
	ctx, cancel := context.WithTimeout(ctx, utilizationTimeout)
	defer cancel()
	if end.After(now) {
		end = now
	}
	report := utilizationReport{SchemaVersion: utilizationSchemaVersion, GeneratedAt: now.UTC(), Start: start.UTC(), End: end.UTC(), CPUWindow: "2m", MemoryWindow: "1m", Step: "60s", Clusters: []string{}, Snapshots: []utilizationSnapshot{}}
	first := start.UTC().Truncate(time.Minute)
	if first.Before(start) {
		first = first.Add(time.Minute)
	}
	last := end.UTC().Truncate(time.Minute)
	if first.After(last) {
		report.Warnings = append(report.Warnings, "time window contains no UTC minute samples")
		return report
	}
	results := utilizationQueryBatch(ctx, query, utilizationHistoryQueries(), first, last)
	report.Warnings = append(report.Warnings, utilizationQueryWarnings(results)...)
	history, clusters := utilizationBuildHistory(results, first, last)
	report.Clusters = clusters
	if len(clusters) == 0 {
		report.Warnings = append(report.Warnings, "expected underlay cluster inventory unavailable; no peaks selected")
		return report
	}
	report.Snapshots, report.Warnings = utilizationSelectSnapshots(history, clusters, first, last, report.Warnings)
	queries := utilizationSnapshotQueries(clusters)
	for i := range report.Snapshots {
		snapshot := &report.Snapshots[i]
		if ctx.Err() != nil {
			snapshot.Warnings = append(snapshot.Warnings, "workload collection unavailable: "+ctx.Err().Error())
			continue
		}
		results := utilizationQueryBatch(ctx, query, queries, snapshot.Time, snapshot.Time)
		snapshot.Warnings = append(snapshot.Warnings, utilizationQueryWarnings(results)...)
		snapshot.Workloads = utilizationBuildWorkloads(results, clusters, snapshot.Time, &snapshot.Warnings)
	}
	if ctx.Err() != nil {
		report.Warnings = append(report.Warnings, "utilization collection incomplete: "+ctx.Err().Error())
	}
	return report
}

func utilizationValue(value []any) (int64, float64, bool) {
	if len(value) != 2 {
		return 0, 0, false
	}
	parse := func(raw any) (float64, error) {
		switch v := raw.(type) {
		case float64:
			return v, nil
		case string:
			return strconv.ParseFloat(v, 64)
		case json.Number:
			return v.Float64()
		default:
			return 0, fmt.Errorf("invalid sample")
		}
	}
	ts, err := parse(value[0])
	if err != nil {
		return 0, 0, false
	}
	v, err := parse(value[1])
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || math.IsNaN(ts) || math.IsInf(ts, 0) || ts != math.Trunc(ts) {
		return 0, 0, false
	}
	return int64(ts), v, true
}

func utilizationMax(dst **float64, v float64) {
	if *dst == nil || v > **dst {
		n := v
		*dst = &n
	}
}

func utilizationBuildHistory(results []utilizationQueryResult, start, end time.Time) (map[int64]*utilizationMinute, []string) {
	history := map[int64]*utilizationMinute{}
	for t := start; !t.After(end); t = t.Add(time.Minute) {
		history[t.Unix()] = &utilizationMinute{nodes: map[utilizationNodeKey]*utilizationHistoryNode{}, expected: map[string]bool{}}
	}
	clusters := map[string]bool{}
	mapping := map[int64]map[utilizationInstanceKey]string{}
	// Inventory and mapping are independent of query completion order.
	for _, result := range results {
		if result.err != nil || (result.query.name != "inventory" && result.query.name != "mapping") {
			continue
		}
		for _, series := range result.series {
			cluster := series.Metric["cluster"]
			if cluster == "" {
				continue
			}
			for _, sample := range series.Values {
				ts, v, ok := utilizationValue(sample)
				minute := history[ts]
				if !ok || minute == nil || v == 0 {
					continue
				}
				if result.query.name == "inventory" {
					minute.expected[cluster] = true
					clusters[cluster] = true
					continue
				}
				if mapping[ts] == nil {
					mapping[ts] = map[utilizationInstanceKey]string{}
				}
				key := utilizationInstanceKey{cluster, series.Metric["instance"]}
				name := series.Metric["nodename"]
				if old, found := mapping[ts][key]; found && old != name {
					name = "unknown"
				}
				mapping[ts][key] = name
			}
		}
	}
	for _, result := range results {
		if result.err != nil || result.query.name == "inventory" || result.query.name == "mapping" {
			continue
		}
		for _, series := range result.series {
			m := series.Metric
			if !clusters[m["cluster"]] || m["hostedcontrolplane"] != "" {
				continue
			}
			for _, sample := range series.Values {
				ts, v, ok := utilizationValue(sample)
				minute := history[ts]
				if !ok || minute == nil {
					continue
				}
				name := m["node"]
				if result.query.name != "nodes" {
					name = mapping[ts][utilizationInstanceKey{m["cluster"], m["instance"]}]
					if name == "" {
						name = m["instance"]
					}
				}
				if name == "" {
					continue
				}
				key := utilizationNodeKey{m["cluster"], name}
				node := minute.nodes[key]
				if node == nil {
					node = &utilizationHistoryNode{utilizationNode: utilizationNode{Cluster: key.cluster, Name: name, Pool: "unknown", SKU: "unknown"}}
					minute.nodes[key] = node
				}
				switch result.query.name {
				case "cpu":
					utilizationMax(&node.Usage.CPU, v)
				case "total":
					utilizationMax(&node.total, v)
				case "available":
					utilizationMax(&node.available, v)
				case "nodes":
					switch m["__name__"] {
					case "kube_node_info":
						node.inventory = v > 0
					case "kube_node_labels":
						if label := m["label_node_kubernetes_io_instance_type"]; label != "" {
							node.SKU = label
						}
						if label := m["label_kubernetes_azure_com_agentpool"]; label != "" {
							node.Pool = label
						}
					case "kube_node_status_capacity", "kube_node_status_allocatable":
						resources := &node.Capacity
						if m["__name__"] == "kube_node_status_allocatable" {
							resources = &node.Allocatable
						}
						if m["resource"] == "cpu" {
							utilizationMax(&resources.CPU, v)
						}
						if m["resource"] == "memory" {
							utilizationMax(&resources.Memory, v)
						}
					}
				}
			}
		}
	}
	for _, minute := range history {
		for _, node := range minute.nodes {
			// Keep KSM coverage for selection, but persist the exporter total
			// used by (total-available)/total so replay uses the same denominator.
			node.ksmMemory = node.Capacity.Memory
			node.Capacity.Memory = node.total
			if node.total != nil && node.available != nil && *node.total >= *node.available {
				v := *node.total - *node.available
				node.Usage.Memory = &v
			}
		}
	}
	names := make([]string, 0, len(clusters))
	for name := range clusters {
		names = append(names, name)
	}
	slices.Sort(names)
	return history, names
}

func utilizationSelectSnapshots(history map[int64]*utilizationMinute, clusters []string, start, end time.Time, warnings []string) ([]utilizationSnapshot, []string) {
	type peak struct {
		ts    int64
		ratio float64
	}
	peaks := map[string]peak{}
	missing := map[string]int{}
	consider := func(reason string, ts int64, usage, capacity float64, complete bool) {
		if !complete || capacity <= 0 {
			missing[reason]++
			return
		}
		ratio := usage / capacity
		if old, found := peaks[reason]; !found || ratio > old.ratio {
			peaks[reason] = peak{ts, ratio}
		}
	}
	for t := start; !t.After(end); t = t.Add(time.Minute) {
		minute := history[t.Unix()]
		nodeKeys := make([]utilizationNodeKey, 0, len(minute.nodes))
		for key := range minute.nodes {
			nodeKeys = append(nodeKeys, key)
		}
		sort.Slice(nodeKeys, func(i, j int) bool {
			if nodeKeys[i].cluster != nodeKeys[j].cluster {
				return nodeKeys[i].cluster < nodeKeys[j].cluster
			}
			return nodeKeys[i].node < nodeKeys[j].node
		})
		var overallUsage, overallCapacity [2]float64
		overallComplete := [2]bool{true, true}
		for _, cluster := range clusters {
			usage, capacity := [2]float64{}, [2]float64{}
			complete := [2]bool{minute.expected[cluster], minute.expected[cluster]}
			count := 0
			for _, key := range nodeKeys {
				if key.cluster != cluster {
					continue
				}
				node := minute.nodes[key]
				count++
				for i, metric := range []struct{ use, cap, ksm *float64 }{{node.Usage.CPU, node.Capacity.CPU, node.Capacity.CPU}, {node.Usage.Memory, node.Capacity.Memory, node.ksmMemory}} {
					if !node.inventory || metric.use == nil || metric.cap == nil || *metric.cap <= 0 || metric.ksm == nil || *metric.ksm <= 0 {
						complete[i] = false
						continue
					}
					usage[i] += *metric.use
					capacity[i] += *metric.cap
				}
			}
			for i, resource := range []string{"CPU", "memory"} {
				complete[i] = complete[i] && count > 0
				consider(cluster+" "+resource+" peak", t.Unix(), usage[i], capacity[i], complete[i])
				overallComplete[i] = overallComplete[i] && complete[i]
				overallUsage[i] += usage[i]
				overallCapacity[i] += capacity[i]
			}
		}
		for i, resource := range []string{"CPU", "memory"} {
			consider("overall "+resource+" peak", t.Unix(), overallUsage[i], overallCapacity[i], overallComplete[i])
		}
	}
	byTime := map[int64][]string{}
	for reason, peak := range peaks {
		byTime[peak.ts] = append(byTime[peak.ts], reason)
	}
	var historyWarnings []string
	for reason, count := range missing {
		historyWarnings = append(historyWarnings, fmt.Sprintf("%s: incomplete history at %d minute(s); these minutes were excluded", reason, count))
	}
	slices.Sort(historyWarnings)
	warnings = append(warnings, historyWarnings...)
	snapshots := make([]utilizationSnapshot, 0, len(byTime))
	for ts, reasons := range byTime {
		slices.Sort(reasons)
		snapshot := utilizationSnapshot{Time: time.Unix(ts, 0).UTC(), Reasons: reasons, Nodes: []utilizationNode{}, Workloads: []utilizationWorkload{}}
		for _, node := range history[ts].nodes {
			snapshot.Nodes = append(snapshot.Nodes, node.utilizationNode)
			if !node.inventory || node.Usage.CPU == nil || node.Usage.Memory == nil || node.Capacity.CPU == nil || node.Capacity.Memory == nil || node.ksmMemory == nil || node.Allocatable.CPU == nil || node.Allocatable.Memory == nil {
				snapshot.Warnings = append(snapshot.Warnings, node.Cluster+"/"+node.Name+": incomplete node metrics")
			}
			if node.Pool == "unknown" || node.SKU == "unknown" {
				snapshot.Warnings = append(snapshot.Warnings, node.Cluster+"/"+node.Name+": node pool or SKU labels unavailable")
			}
		}
		for _, cluster := range clusters {
			found := false
			for _, node := range snapshot.Nodes {
				if node.Cluster == cluster {
					found = true
					break
				}
			}
			if !found {
				snapshot.Warnings = append(snapshot.Warnings, cluster+": node inventory unavailable at selected minute")
			}
		}
		sort.Slice(snapshot.Nodes, func(i, j int) bool {
			a, b := snapshot.Nodes[i], snapshot.Nodes[j]
			if a.Cluster != b.Cluster {
				return a.Cluster < b.Cluster
			}
			return a.Name < b.Name
		})
		slices.Sort(snapshot.Warnings)
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Time.Before(snapshots[j].Time) })
	return snapshots, warnings
}
