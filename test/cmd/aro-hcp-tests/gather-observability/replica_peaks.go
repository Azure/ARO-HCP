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
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
)

type replicaPeakReport struct {
	Version         int                    `json:"version"`
	Start           time.Time              `json:"start"`
	End             time.Time              `json:"end"`
	GeneratedAt     time.Time              `json:"generatedAt"`
	GridStep        string                 `json:"gridStep"`
	CPUWindow       string                 `json:"cpuWindow"`
	SizingCPUWindow string                 `json:"sizingCPUWindow,omitempty"`
	MemoryWindow    string                 `json:"memoryWindow"`
	Clusters        []string               `json:"clusters"`
	Warnings        []string               `json:"warnings,omitempty"`
	Queries         []replicaPeakQuery     `json:"queries"`
	Containers      []replicaPeakContainer `json:"containers"`
	Metadata        []replicaPeakMetadata  `json:"metadata"`
}

type replicaPeakQuery struct {
	Cluster   string `json:"cluster,omitempty"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	Status    string `json:"status"` // success, empty, or error; partial evidence may accompany error
	Series    int    `json:"series"` // returned series, before validation/deduplication
	Error     string `json:"error,omitempty"`
}

// Containers are normalized summary records, not additive pod totals. Missing
// usage/requests are unknown, never zero. Labels exclude only statistic/__name__;
// workspace and queryName remain part of the identity, including request counts.
// Summary first/last are observed grid Unix seconds, NOT timestamps of peaks.
type replicaPeakContainer struct {
	Cluster   string             `json:"cluster"`
	Namespace string             `json:"namespace"`
	Pod       string             `json:"pod"`
	PodUID    string             `json:"podUID,omitempty"`
	Container string             `json:"container"`
	Workspace string             `json:"workspace"`
	QueryName string             `json:"queryName"`
	Labels    map[string]string  `json:"labels"`
	Summary   map[string]float64 `json:"summary"`
}

// Offline pod metadata joins require cluster/namespace/pod AND PodUID (and
// container for runtime metadata). Empty PodUID is unmatched, never name-joined.
// Controller families have no pod UID: retain all candidate edges, not a guessed
// owner chain when controller names were reused during the collection window.
type replicaPeakMetadata struct {
	Workspace string            `json:"workspace"`
	Labels    map[string]string `json:"labels"`
}

func (o Options) collectReplicaPeaks(ctx context.Context, workspaces map[string]*workspaceData) replicaPeakReport {
	client := &http.Client{Timeout: 30 * time.Second}
	return collectReplicaPeaks(ctx, o.TimeWindow.Start, o.TimeWindow.End, time.Now(), func(ctx context.Context, workspace, expression string, start, end time.Time) ([]PrometheusResult, error) {
		ws := workspaces[workspace]
		if ws == nil || ws.PromEndpoint == "" {
			return nil, fmt.Errorf("workspace endpoint unavailable")
		}
		response, err := queryRange(ctx, client, o.cred, ws.PromEndpoint, expression, start, end, "60s")
		if err != nil {
			return nil, err
		}
		if len(response.Warnings) != 0 {
			return nil, fmt.Errorf("prometheus warnings: %s", strings.Join(response.Warnings, "; "))
		}
		if response.Data.ResultType != "matrix" {
			return nil, fmt.Errorf("unexpected Prometheus result type %q", response.Data.ResultType)
		}
		return response.Data.Result, nil
	})
}

func collectReplicaPeaks(ctx context.Context, start, end, now time.Time, query utilizationQueryFunc) replicaPeakReport {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if end.After(now) {
		end = now
	}
	r := replicaPeakReport{Version: 1, Start: start.UTC(), End: end.UTC(), GeneratedAt: now.UTC(), GridStep: "60s", CPUWindow: "2m", SizingCPUWindow: "10m", MemoryWindow: "60s", Clusters: []string{}, Queries: []replicaPeakQuery{}, Containers: []replicaPeakContainer{}, Metadata: []replicaPeakMetadata{}}
	first, last := start.UTC().Truncate(time.Minute), end.UTC().Truncate(time.Minute)
	if first.Before(start) {
		first = first.Add(time.Minute)
	}
	if start.IsZero() || end.IsZero() || now.IsZero() || start.After(end) || first.After(last) {
		r.Warnings = append(r.Warnings, "invalid time window or no UTC minute grid points")
		return r
	}
	// Discovery covers the full requested window, including its sub-minute edges,
	// while the HTTP query still has exactly one evaluation at the last grid point.
	discovery := utilizationQuery{"inventory", workspaceSvc, fmt.Sprintf(`max by (cluster) (max_over_time(underlay_clusters{source="bicep"}[%dms] @ %d.%03d))`, end.Sub(start).Milliseconds()+1, end.Unix(), end.Nanosecond()/1e6)}
	metadata := map[string]replicaPeakMetadata{}
	records := map[string]*replicaPeakContainer{}
	conflicts := map[string]bool{}
	key := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	consume := func(cluster string, results []utilizationQueryResult) {
		for _, result := range results {
			q := replicaPeakQuery{Cluster: cluster, Workspace: result.query.workspace, Name: result.query.name, Status: "success", Series: len(result.series)}
			if len(result.series) == 0 {
				q.Status = "empty"
			}
			invalid := func() { q.Status, q.Error = "error", "malformed, nonfinite, or conflicting observations skipped" }
			if result.err != nil {
				q.Status, q.Error = "error", result.err.Error()
			} else {
				for _, series := range result.series {
					m := series.Metric
					if len(series.Values) != 1 || m["cluster"] == "" || (cluster != "" && m["cluster"] != cluster) || m["hostedcontrolplane"] != "" {
						invalid()
						continue
					}
					ts, v, ok := utilizationValue(series.Values[0])
					if !ok || ts != last.Unix() {
						invalid()
						continue
					}
					if q.Name == "inventory" {
						if v > 0 {
							r.Clusters = append(r.Clusters, m["cluster"])
						}
						continue
					}
					if q.Name == "metadata" {
						if v != 1 || m["metric"] == "" {
							invalid()
							continue
						}
						entry := replicaPeakMetadata{q.Workspace, maps.Clone(m)}
						metadata[key(entry)] = entry
						continue
					}
					stat := m["statistic"]
					requests := q.Name == "requests" || q.Name == "initRequests"
					if m["namespace"] == "" || m["pod"] == "" || m["container"] == "" || m["container"] == "POD" ||
						!slices.Contains([]string{"min", "max", "count", "first", "last"}, stat) || (stat == "min" && !requests) ||
						(requests && m["resource"] != "cpu" && m["resource"] != "memory") ||
						(stat == "count" && (v < 1 || v != math.Trunc(v) || v > last.Sub(first).Minutes()+1)) ||
						((stat == "first" || stat == "last") && (v < float64(first.Unix()) || v > float64(last.Unix()) || math.Mod(v, 60) != 0)) {
						invalid()
						continue
					}
					labels := maps.Clone(m)
					delete(labels, "statistic")
					delete(labels, "__name__")
					k := key(replicaPeakMetadata{q.Workspace + "/" + q.Name, labels})
					if records[k] == nil {
						records[k] = &replicaPeakContainer{Cluster: cluster, Namespace: m["namespace"], Pod: m["pod"], PodUID: strings.ToLower(m["uid"]), Container: m["container"], Workspace: q.Workspace, QueryName: q.Name, Labels: labels, Summary: map[string]float64{}}
					}
					summary := records[k].Summary
					if old, exists := summary[stat]; conflicts[k+stat] || (exists && old != v) {
						conflicts[k+stat] = true
						delete(summary, stat)
						invalid()
					} else {
						summary[stat] = v
					}
				}
			}
			if q.Error != "" {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s %s %s: %s", cluster, q.Workspace, q.Name, q.Error))
			}
			r.Queries = append(r.Queries, q)
		}
	}
	consume("", utilizationQueryBatch(ctx, query, []utilizationQuery{discovery}, last, last))
	slices.Sort(r.Clusters)
	r.Clusters = slices.Compact(r.Clusters)
	if len(r.Clusters) == 0 {
		r.Warnings = append(r.Warnings, "underlay cluster inventory unavailable or empty; no replica data collected")
	}
	for _, cluster := range r.Clusters {
		before := len(records)
		consume(cluster, utilizationQueryBatch(ctx, query, replicaPeakQueries(cluster, start, end), last, last))
		if before == len(records) {
			r.Warnings = append(r.Warnings, cluster+": no container observations")
		}
	}
	// Runtime IDs are indexed with pod/container scope. Conflicting metadata
	// invalidates the fallback permanently, independent of response order.
	runtimeUIDs := map[[5]string]string{}
	runtimeID := func(id string) string {
		if _, after, found := strings.Cut(id, "://"); found {
			id = after
		}
		id = strings.TrimSuffix(path.Base(id), ".scope")
		for _, prefix := range []string{"cri-containerd-", "crio-", "docker-"} {
			id = strings.TrimPrefix(id, prefix)
		}
		return id
	}
	for _, k := range slices.Sorted(maps.Keys(metadata)) {
		entry := metadata[k]
		r.Metadata = append(r.Metadata, entry)
		m := entry.Labels
		if (m["metric"] != "kube_pod_container_info" && m["metric"] != "kube_pod_init_container_info") || m["container_id"] == "" || m["uid"] == "" {
			continue
		}
		id := [5]string{m["cluster"], m["namespace"], m["pod"], m["container"], runtimeID(m["container_id"])}
		uid := strings.ToLower(m["uid"])
		if old, exists := runtimeUIDs[id]; exists && old != uid {
			uid = ""
		}
		runtimeUIDs[id] = uid
	}
	for _, k := range slices.Sorted(maps.Keys(records)) {
		row := records[k]
		if len(row.Summary) == 0 {
			continue
		}
		expected := 4
		if row.QueryName == "requests" || row.QueryName == "initRequests" {
			expected = 5
		}
		if len(row.Summary) != expected {
			r.Warnings = append(r.Warnings, row.Cluster+"/"+row.Namespace+"/"+row.Pod+"/"+row.Container+": "+row.Workspace+" "+row.QueryName+" summary incomplete")
		}
		if row.QueryName == "cpu" || row.QueryName == "cpuSustained" || row.QueryName == "memory" {
			id := row.Labels["id"]
			row.PodUID = ""
			if match := utilizationCgroupUID.FindStringSubmatch(id); len(match) > 1 {
				row.PodUID = strings.ToLower(strings.ReplaceAll(match[1], "_", "-"))
			}
			uid, found := runtimeUIDs[[5]string{row.Cluster, row.Namespace, row.Pod, row.Container, runtimeID(id)}]
			if row.PodUID == "" {
				row.PodUID = uid
			} else if found && uid != row.PodUID {
				row.PodUID = ""
			}
		}
		if row.PodUID == "" {
			r.Warnings = append(r.Warnings, row.Cluster+"/"+row.Namespace+"/"+row.Pod+"/"+row.Container+": "+row.QueryName+" pod UID unavailable or conflicting; retained unmatched")
		}
		r.Containers = append(r.Containers, *row)
	}
	if ctx.Err() != nil {
		r.Warnings = append(r.Warnings, "replica peak collection incomplete: "+ctx.Err().Error())
	}
	slices.Sort(r.Warnings)
	r.Warnings = slices.Compact(r.Warnings)
	return r
}
