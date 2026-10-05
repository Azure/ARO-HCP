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
	"strconv"
	"strings"
	"time"
)

// replicaPeakQueries returns compact evidence for query_range with start=end=end.
// The inclusive grid is ceil(start)..floor(end), aligned to UTC minutes. An empty
// grid yields no queries. The extra second includes the first grid point despite
// PromQL's left-open ranges; anchoring at last also supports unaligned callers.
//
// HA series are max-deduplicated at each grid point, never summed. Usage returns
// statistic=max/count/first/last; requests additionally return min. Count is the
// number of grid points with a deduplicated observation, not scrapes or replicas.
// First/last are Unix seconds of those grid points, NOT times of extrema or raw
// samples. Missing requests remain absent; observed zero requests are retained.
//
// CPU uses rate over (t-2m,t], and sustained CPU over (t-10m,t]. Memory uses the raw
// maximum over (t-60s,t], not an average, covering between-minute spikes. Thus the
// first point can inspect data before start, and data after floor(end) is excluded.
// Range functions can still observe pre-staleness samples in those lookbacks.
// Requests and metadata use instant selectors with normal Prometheus
// lookback/staleness semantics.
func replicaPeakQueries(cluster string, start, end time.Time) []utilizationQuery {
	first, last := start.UTC().Truncate(time.Minute), end.UTC().Truncate(time.Minute)
	if first.Before(start) {
		first = first.Add(time.Minute)
	}
	if first.After(last) {
		return nil
	}
	rangeSelector := fmt.Sprintf("[%ds:60s] @ %d", int64(last.Sub(first)/time.Second)+1, last.Unix())
	selector := `cluster=` + strconv.Quote(cluster) + `,hostedcontrolplane=""`
	usageLabels := "cluster, namespace, pod, container, node, instance, id"
	queries := []utilizationQuery{
		{"cpu", workspaceSvc, `max by (` + usageLabels + `) (rate(container_cpu_usage_seconds_total{` + selector + `,container!="",container!="POD",pod!=""}[2m]))`},
		{"cpuSustained", workspaceSvc, `max by (` + usageLabels + `) (rate(container_cpu_usage_seconds_total{` + selector + `,container!="",container!="POD",pod!=""}[10m]))`},
		{"memory", workspaceSvc, `max by (` + usageLabels + `) (max_over_time(container_memory_working_set_bytes{` + selector + `,container!="",container!="POD",pod!=""}[60s]))`},
	}
	metadata := `max by (__name__, cluster, namespace, pod, uid, container, container_id, owner_kind, owner_name, owner_is_controller, replicaset, job_name, replicationcontroller) ((` + utilizationMetricUnion(selector,
		"kube_pod_container_info", "kube_pod_init_container_info", "kube_pod_owner",
		"kube_replicaset_owner", "kube_job_owner", "kube_replicationcontroller_owner") + `) == 1)`
	// Over-time functions drop __name__; capture it before reducing so families
	// with otherwise identical labels remain distinguishable to the collector.
	metadata = `max_over_time((label_replace(` + metadata + `, "metric", "$1", "__name__", "(.+)"))` + rangeSelector + `)`
	for _, ws := range []string{workspaceSvc, workspaceHcp} {
		queries = append(queries,
			utilizationQuery{"requests", ws, `max by (cluster, namespace, pod, uid, container, resource) (kube_pod_container_resource_requests{` + selector + `,resource=~"cpu|memory"})`},
			utilizationQuery{"initRequests", ws, `max by (cluster, namespace, pod, uid, container, resource) (kube_pod_init_container_resource_requests{` + selector + `,resource=~"cpu|memory"})`},
			utilizationQuery{"metadata", ws, metadata},
		)
	}
	for i := range queries {
		query := &queries[i]
		if query.name == "metadata" {
			continue
		}
		var summaries []string
		for _, statistic := range []string{"max", "min", "count", "first", "last"} {
			if statistic == "min" && (query.name == "cpu" || query.name == "cpuSustained" || query.name == "memory") {
				continue
			}
			expression, reduction := query.expression, statistic
			if statistic == "first" || statistic == "last" {
				expression = "timestamp(" + expression + ")"
				reduction = "min"
				if statistic == "last" {
					reduction = "max"
				}
			}
			summaries = append(summaries, fmt.Sprintf(`label_replace(%s_over_time((%s)%s), "statistic", %q, "", "")`, reduction, expression, rangeSelector, statistic))
		}
		query.expression = strings.Join(summaries, " or ")
	}
	return queries
}
