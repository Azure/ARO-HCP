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
	"fmt"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type utilizationPodKey struct{ cluster, namespace, pod string }
type utilizationOwner struct{ kind, name string }
type utilizationOwnerKey struct{ cluster, namespace, kind, name string }
type utilizationPod struct {
	node                                             string
	placement, pending, terminal, unscheduled, phase bool
	owner                                            utilizationOwner
	workspaces                                       map[string]bool
	containers                                       map[string]*utilizationContainer
	containerInfo                                    map[string]bool
	specContainers                                   map[string]bool
	containerIDs                                     map[string]string
}

// Both cgroupfs pod<UUID>/... and systemd pod<UUID_with_underscores>.slice.
var utilizationCgroupUID = regexp.MustCompile(`(?:^|[/_-])pod([0-9a-fA-F]{8}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{12})(?:/|\.slice(?:/|$))`)

func utilizationAddCount(dst **int, value *int, first bool) {
	if first {
		if value != nil {
			n := *value
			*dst = &n
		} else {
			*dst = nil
		}
	} else if *dst == nil || value == nil {
		*dst = nil
	} else {
		**dst += *value
	}
}

// Unknown propagates through every aggregation level. A partially measured
// workload must never look like a complete (and artificially small) total.
func utilizationAdd(dst **float64, value *float64, first bool) {
	if first {
		if value != nil {
			v := *value
			*dst = &v
		} else {
			*dst = nil
		}
		return
	}
	if *dst == nil || value == nil {
		*dst = nil
		return
	}
	**dst += *value
}

func utilizationAddResources(dst *utilizationResources, value utilizationResources, first bool) {
	utilizationAdd(&dst.CPU, value.CPU, first)
	utilizationAdd(&dst.Memory, value.Memory, first)
}

func utilizationBuildWorkloads(results []utilizationQueryResult, clusters []string, at time.Time, warnings *[]string) []utilizationWorkload {
	pods := map[utilizationPodKey]*utilizationPod{}
	owners := map[utilizationOwnerKey]utilizationOwner{}
	success := map[string]map[string]bool{}
	warningSet := map[string]bool{}
	warn := func(cluster, namespace, message string) { warningSet[cluster+"/"+namespace+": "+message] = true }
	uids := map[utilizationPodKey]string{}
	ambiguous := map[utilizationPodKey]bool{}
	// Never merge two KSM incarnations of a reused pod name. If the instant
	// metadata itself disagrees, exclude it rather than guessing which is live.
	for _, result := range results {
		if result.err != nil || result.query.name == "cpu" || result.query.name == "memory" {
			continue
		}
		for _, series := range result.series {
			m := series.Metric
			if m["uid"] == "" || m["pod"] == "" || !slices.Contains(clusters, m["cluster"]) || m["hostedcontrolplane"] != "" {
				continue
			}
			for _, sample := range series.Values {
				ts, v, ok := utilizationValue(sample)
				if !ok || ts != at.Unix() || (result.query.name == "metadata" && v == 0) {
					continue
				}
				key := utilizationPodKey{m["cluster"], m["namespace"], m["pod"]}
				uid := strings.ToLower(m["uid"])
				if old := uids[key]; old != "" && old != uid {
					ambiguous[key] = true
				}
				uids[key] = uid
			}
		}
	}
	for _, result := range results {
		if success[result.query.workspace] == nil {
			success[result.query.workspace] = map[string]bool{}
		}
		success[result.query.workspace][result.query.name] = result.err == nil
		if result.err != nil || result.query.name == "cpu" || result.query.name == "memory" {
			continue
		}
		for _, series := range result.series {
			m := series.Metric
			if !slices.Contains(clusters, m["cluster"]) || m["hostedcontrolplane"] != "" {
				continue
			}
			var value *float64
			for _, sample := range series.Values {
				ts, v, ok := utilizationValue(sample)
				if ok && ts == at.Unix() {
					utilizationMax(&value, v)
				}
			}
			if value == nil {
				continue
			}
			metric := m["__name__"]
			owner := utilizationOwner{m["owner_kind"], m["owner_name"]}
			if result.query.name == "metadata" && *value > 0 && (m["owner_is_controller"] == "" || m["owner_is_controller"] == "true") {
				kind, name := "", ""
				switch metric {
				case "kube_replicaset_owner":
					kind, name = "ReplicaSet", m["replicaset"]
				case "kube_job_owner":
					kind, name = "Job", m["job_name"]
				case "kube_replicationcontroller_owner":
					kind, name = "ReplicationController", m["replicationcontroller"]
				}
				if kind != "" && name != "" {
					key := utilizationOwnerKey{m["cluster"], m["namespace"], kind, name}
					if old, exists := owners[key]; exists && old != owner {
						owner = utilizationOwner{"Unknown", "unknown"}
						warn(key.cluster, key.namespace, "conflicting owner metadata")
					}
					owners[key] = owner
					continue
				}
			}
			if m["pod"] == "" || m["namespace"] == "" {
				continue
			}
			key := utilizationPodKey{m["cluster"], m["namespace"], m["pod"]}
			if ambiguous[key] {
				warn(key.cluster, key.namespace, "conflicting pod incarnations; metadata and usage excluded")
				continue
			}
			pod := pods[key]
			if pod == nil {
				pod = &utilizationPod{containers: map[string]*utilizationContainer{}, containerInfo: map[string]bool{}, specContainers: map[string]bool{}, containerIDs: map[string]string{}, workspaces: map[string]bool{}}
				pods[key] = pod
			}
			if result.query.name == "metadata" {
				if *value == 0 {
					continue
				}
				pod.workspaces[result.query.workspace] = true
				switch metric {
				case "kube_pod_info":
					if pod.placement && pod.node != m["node"] {
						pod.node = "unknown"
						warn(key.cluster, key.namespace, "conflicting pod placement")
					} else {
						pod.node = m["node"]
					}
					pod.placement = true
				case "kube_pod_status_phase":
					pod.phase = true
					pod.pending = pod.pending || m["phase"] == "Pending"
					pod.terminal = pod.terminal || m["phase"] == "Succeeded" || m["phase"] == "Failed"
				case "kube_pod_status_scheduled":
					pod.unscheduled = pod.unscheduled || m["condition"] == "false"
				case "kube_pod_owner":
					if m["owner_is_controller"] != "false" && owner.kind != "" && owner.name != "" {
						if pod.owner.kind != "" && pod.owner != owner {
							pod.owner = utilizationOwner{"Unknown", "unknown"}
							warn(key.cluster, key.namespace, "conflicting pod owner")
						} else {
							pod.owner = owner
						}
					}
				case "kube_pod_container_info":
					pod.containerInfo[m["container"]] = true
					id := m["container_id"]
					if _, after, found := strings.Cut(id, "://"); found {
						id = after
					}
					if old := pod.containerIDs[m["container"]]; old != "" && old != id {
						id = "unknown"
					}
					pod.containerIDs[m["container"]] = id
				}
				// All other metadata may inherit the exporter's target container
				// label. It is not a container belonging to the observed pod.
				if metric != "kube_pod_container_info" {
					continue
				}
			}
			name := m["container"]
			if name == "" || name == "POD" {
				continue
			}
			container := pod.containers[name]
			if container == nil {
				container = &utilizationContainer{Name: name}
				pod.containers[name] = container
			}
			switch result.query.name {
			case "requests", "limits":
				pod.specContainers[name] = true
				pod.workspaces[result.query.workspace] = true
				resources := &container.Requests
				if result.query.name == "limits" {
					resources = &container.Limits
				}
				if m["resource"] == "cpu" {
					utilizationMax(&resources.CPU, *value)
				}
				if m["resource"] == "memory" {
					utilizationMax(&resources.Memory, *value)
				}
			}
		}
	}
	// Resolve usage only after metadata. Preserve node and cgroup incarnation
	// through HA dedup; a previous StatefulSet pod can remain in rate2m.
	for _, result := range results {
		if result.err != nil || (result.query.name != "cpu" && result.query.name != "memory") {
			continue
		}
		type usageKey struct {
			pod       utilizationPodKey
			container string
		}
		candidates := map[usageKey]map[string]float64{}
		for _, series := range result.series {
			m := series.Metric
			key := utilizationPodKey{m["cluster"], m["namespace"], m["pod"]}
			pod := pods[key]
			if !slices.Contains(clusters, key.cluster) || m["hostedcontrolplane"] != "" {
				continue
			}
			if pod == nil || !pod.placement || pod.node == "" || pod.node == "unknown" {
				warn(key.cluster, key.namespace, "usage placement unavailable or ambiguous; usage excluded")
				continue
			}
			if pod.terminal {
				continue
			}
			node := m["node"]
			if node == "" {
				node = m["instance"]
				if host, _, err := net.SplitHostPort(node); err == nil {
					node = host
				}
				if net.ParseIP(node) != nil {
					node = ""
				}
			}
			id := m["id"]
			uid := ""
			if match := utilizationCgroupUID.FindStringSubmatch(id); len(match) > 1 {
				uid = strings.ToLower(strings.ReplaceAll(match[1], "_", "-"))
			}
			runtimeID := pod.containerIDs[m["container"]]
			runtimeMatch := runtimeID != "" && runtimeID != "unknown" && (strings.HasSuffix(id, "/"+runtimeID) || strings.HasSuffix(id, "-"+runtimeID+".scope"))
			placementConflict := node != "" && node != pod.node
			uidConflict := uid != "" && uids[key] != "" && uid != uids[key]
			incarnationKnown := runtimeMatch || (uid != "" && uid == uids[key])
			if placementConflict || uidConflict || !incarnationKnown || (runtimeID != "" && !runtimeMatch) {
				warn(key.cluster, key.namespace, "usage incarnation or placement unmatched; stale or ambiguous usage excluded")
				continue
			}
			container := pod.containers[m["container"]]
			if container == nil {
				warn(key.cluster, key.namespace, "usage container absent from spec/runtime metadata; usage excluded")
				continue
			}
			k := usageKey{key, m["container"]}
			for _, sample := range series.Values {
				ts, v, ok := utilizationValue(sample)
				if !ok || ts != at.Unix() {
					continue
				}
				if candidates[k] == nil {
					candidates[k] = map[string]float64{}
				}
				candidates[k][id] = max(candidates[k][id], v)
			}
		}
		for key, values := range candidates {
			if len(values) != 1 {
				warn(key.pod.cluster, key.pod.namespace, "multiple container incarnations; ambiguous usage excluded")
				continue
			}
			container := pods[key.pod].containers[key.container]
			for _, value := range values {
				if result.query.name == "cpu" {
					utilizationMax(&container.Usage.CPU, value)
				} else {
					utilizationMax(&container.Usage.Memory, value)
				}
			}
		}
	}
	type workloadKey struct {
		cluster, namespace, kind, name, node string
		unscheduled                          bool
	}
	rows := map[workloadKey]*utilizationWorkload{}
	containers := map[workloadKey]map[string]*utilizationContainer{}
	incompleteInventory := map[workloadKey]bool{}
	keys := make([]utilizationPodKey, 0, len(pods))
	for key := range pods {
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
		pod := pods[key]
		if pod.terminal {
			continue
		}
		if !pod.phase {
			warn(key.cluster, key.namespace, "pod phase unavailable; workload coverage may include terminal pods")
		}
		owner := pod.owner
		if owner.kind == "" {
			owner = utilizationOwner{"Unknown", "unknown"}
			warn(key.cluster, key.namespace, "owner unavailable; unowned pods aggregated without persisting pod identities")
		}
		seen := map[utilizationOwner]bool{}
		for !seen[owner] {
			seen[owner] = true
			parent, found := owners[utilizationOwnerKey{key.cluster, key.namespace, owner.kind, owner.name}]
			if !found || parent.kind == "" || parent.name == "" {
				break
			}
			owner = parent
		}
		if owner.kind == "ReplicaSet" {
			warn(key.cluster, key.namespace, "ReplicaSet parent unavailable; retaining exact ReplicaSet identity")
		}
		unscheduled := pod.node == "" && (pod.unscheduled || (pod.placement && pod.pending))
		node := pod.node
		if node == "" && !unscheduled {
			node = "unknown"
			warn(key.cluster, key.namespace, "placement unavailable; not classified as unscheduled")
		}
		rowKey := workloadKey{key.cluster, key.namespace, owner.kind, owner.name, node, unscheduled}
		row := rows[rowKey]
		if row == nil {
			row = &utilizationWorkload{Cluster: key.cluster, Namespace: key.namespace, Kind: owner.kind, Name: owner.name, Node: node, Unscheduled: unscheduled, Containers: []utilizationContainer{}}
			if strings.HasPrefix(key.namespace, "ocm-") {
				row.Component = owner.kind + "/" + owner.name
			}
			rows[rowKey] = row
			containers[rowKey] = map[string]*utilizationContainer{}
		}
		row.Pods++
		if pod.pending {
			row.PendingPods++
		}
		for name, container := range pod.containers {
			if !pod.containerInfo[name] && pod.specContainers[name] {
				warn(key.cluster, key.namespace, "runtime container inventory unavailable; retaining observed spec-backed demand, which may omit unobserved containers")
			}
			for _, spec := range []struct {
				query     string
				resources *utilizationResources
			}{{"requests", &container.Requests}, {"limits", &container.Limits}} {
				// Absence means unspecified only if this pod's metadata source
				// successfully answered and confirms this container exists.
				complete := (pod.containerInfo[name] || pod.specContainers[name]) && len(pod.workspaces) > 0
				for ws := range pod.workspaces {
					complete = complete && success[ws][spec.query]
				}
				for i, field := range []**float64{&spec.resources.CPU, &spec.resources.Memory} {
					if spec.query == "limits" && (*field != nil || complete) {
						count := 0
						if *field == nil || **field == 0 {
							count = 1
						}
						if i == 0 {
							container.UnlimitedCPU = &count
						} else {
							container.UnlimitedMemory = &count
						}
					}
					if complete && *field == nil {
						zero := 0.0
						*field = &zero
					}
				}
			}
			if container.Usage.CPU == nil || container.Usage.Memory == nil || container.Requests.CPU == nil || container.Requests.Memory == nil || container.UnlimitedCPU == nil || container.UnlimitedMemory == nil {
				warn(key.cluster, key.namespace, "partial workload metrics; missing coverage propagates as unknown totals")
			}
			aggregate, exists := containers[rowKey][name]
			if !exists {
				aggregate = &utilizationContainer{Name: name}
				containers[rowKey][name] = aggregate
			}
			utilizationAddResources(&aggregate.Usage, container.Usage, !exists)
			utilizationAddResources(&aggregate.Requests, container.Requests, !exists)
			utilizationAddResources(&aggregate.Limits, container.Limits, !exists)
			utilizationAddCount(&aggregate.UnlimitedCPU, container.UnlimitedCPU, !exists)
			utilizationAddCount(&aggregate.UnlimitedMemory, container.UnlimitedMemory, !exists)
		}
		if len(pod.containers) == 0 {
			incompleteInventory[rowKey] = true
			warn(key.cluster, key.namespace, "container inventory unavailable; workload totals unknown")
		}
	}
	workloads := make([]utilizationWorkload, 0, len(rows))
	for key, row := range rows {
		for _, container := range containers[key] {
			row.Containers = append(row.Containers, *container)
		}
		sort.Slice(row.Containers, func(i, j int) bool { return row.Containers[i].Name < row.Containers[j].Name })
		for i, container := range row.Containers {
			utilizationAddResources(&row.Usage, container.Usage, i == 0)
			utilizationAddResources(&row.Requests, container.Requests, i == 0)
			utilizationAddResources(&row.Limits, container.Limits, i == 0)
		}
		if incompleteInventory[key] {
			row.Usage, row.Requests, row.Limits = utilizationResources{}, utilizationResources{}, utilizationResources{}
			for i := range row.Containers {
				row.Containers[i].Usage = utilizationResources{}
				row.Containers[i].Requests = utilizationResources{}
				row.Containers[i].Limits = utilizationResources{}
				row.Containers[i].UnlimitedCPU, row.Containers[i].UnlimitedMemory = nil, nil
			}
		}
		workloads = append(workloads, *row)
	}
	for _, cluster := range clusters {
		found := false
		for _, row := range workloads {
			if row.Cluster == cluster {
				found = true
				break
			}
		}
		if !found {
			warningSet[cluster+": workload inventory unavailable or no nonterminal pods observed"] = true
		}
	}
	sort.Slice(workloads, func(i, j int) bool {
		a, b := workloads[i], workloads[j]
		return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%t", a.Cluster, a.Namespace, a.Kind, a.Name, a.Node, a.Unscheduled) < fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%t", b.Cluster, b.Namespace, b.Kind, b.Name, b.Node, b.Unscheduled)
	})
	var ordered []string
	for warning := range warningSet {
		ordered = append(ordered, warning)
	}
	slices.Sort(ordered)
	*warnings = append(*warnings, ordered...)
	return workloads
}
