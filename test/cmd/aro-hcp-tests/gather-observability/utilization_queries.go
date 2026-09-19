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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

type utilizationQuery struct {
	name, workspace, expression string
}

type utilizationQueryResult struct {
	query  utilizationQuery
	series []PrometheusResult
	err    error
}

type utilizationQueryFunc func(context.Context, string, string, time.Time, time.Time) ([]PrometheusResult, error)

// Azure Managed Prometheus requires an exact metric name per selector. Match
// unions on __name__ so different metric families with identical labels survive.
func utilizationMetricUnion(selector string, metrics ...string) string {
	parts := make([]string, len(metrics))
	for i, metric := range metrics {
		parts[i] = metric + "{" + selector + "}"
	}
	return strings.Join(parts, " or on (__name__) ")
}

// AMA exports instance as the node name. Keep instance until the Go join so
// node_uname_info can also map exporters using an address as their instance.
func utilizationHistoryQueries() []utilizationQuery {
	return []utilizationQuery{
		{"inventory", workspaceSvc, `max by (cluster) (underlay_clusters{source="bicep"})`},
		{"nodes", workspaceSvc, `max by (__name__, cluster, node, resource, label_node_kubernetes_io_instance_type, label_kubernetes_azure_com_agentpool) (` + utilizationMetricUnion(`hostedcontrolplane=""`, "kube_node_info", "kube_node_labels", "kube_node_status_capacity", "kube_node_status_allocatable") + `)`},
		{"mapping", workspaceSvc, `max by (cluster, instance, nodename) (node_uname_info)`},
		{"cpu", workspaceSvc, `sum by (cluster, instance) (max by (cluster, instance, cpu, mode) (rate(node_cpu_seconds_total{mode!~"idle|guest|guest_nice"}[2m])))`},
		{"total", workspaceSvc, `max by (cluster, instance) (avg_over_time(node_memory_MemTotal_bytes[1m]))`},
		{"available", workspaceSvc, `max by (cluster, instance) (avg_over_time(node_memory_MemAvailable_bytes[1m]))`},
	}
}

func utilizationSnapshotQueries(clusters []string) []utilizationQuery {
	parts := make([]string, len(clusters))
	for i, cluster := range clusters {
		parts[i] = regexp.QuoteMeta(cluster)
	}
	selector := `hostedcontrolplane="",cluster=~` + strconv.Quote(strings.Join(parts, "|"))
	queries := []utilizationQuery{
		{"cpu", workspaceSvc, `max by (cluster, namespace, pod, container, node, instance, id) (rate(container_cpu_usage_seconds_total{` + selector + `,container!="",container!="POD",pod!=""}[2m]))`},
		{"memory", workspaceSvc, `max by (cluster, namespace, pod, container, node, instance, id) (avg_over_time(container_memory_working_set_bytes{` + selector + `,container!="",container!="POD",pod!=""}[1m]))`},
	}
	for _, ws := range []string{workspaceSvc, workspaceHcp} {
		queries = append(queries,
			utilizationQuery{"metadata", ws, `max by (__name__, cluster, namespace, pod, uid, container, container_id, node, phase, condition, owner_kind, owner_name, owner_is_controller, replicaset, job_name, replicationcontroller) (` + utilizationMetricUnion(selector, "kube_pod_info", "kube_pod_container_info", "kube_pod_status_phase", "kube_pod_status_scheduled", "kube_pod_owner", "kube_replicaset_owner", "kube_job_owner", "kube_replicationcontroller_owner") + `)`},
			utilizationQuery{"requests", ws, `max by (cluster, namespace, pod, uid, container, resource) (kube_pod_container_resource_requests{` + selector + `,resource=~"cpu|memory"})`},
			utilizationQuery{"limits", ws, `max by (cluster, namespace, pod, uid, container, resource) (kube_pod_container_resource_limits{` + selector + `,resource=~"cpu|memory"})`},
		)
	}
	return queries
}

// A batch has at most two in-flight requests, including credential acquisition.
// Results retain query order, making warnings and joins deterministic.
func utilizationQueryBatch(ctx context.Context, query utilizationQueryFunc, queries []utilizationQuery, start, end time.Time) []utilizationQueryResult {
	results := make([]utilizationQueryResult, len(queries))
	var workers sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for i := worker; i < len(queries); i += 2 {
				results[i].query = queries[i]
				if err := ctx.Err(); err != nil {
					results[i].err = err
					continue
				}
				results[i].series, results[i].err = query(ctx, queries[i].workspace, queries[i].expression, start, end)
				logger := logr.FromContextOrDiscard(ctx).WithValues("workspace", queries[i].workspace, "query", queries[i].name, "start", start, "end", end)
				if results[i].err != nil {
					logger.Error(results[i].err, "utilization query failed")
				} else {
					logger.Info("collected utilization query", "series", len(results[i].series))
				}
			}
		}(worker)
	}
	workers.Wait()
	return results
}

func utilizationQueryWarnings(results []utilizationQueryResult) []string {
	var warnings []string
	for _, result := range results {
		if result.err != nil {
			warnings = append(warnings, fmt.Sprintf("%s %s query unavailable: %v", result.query.workspace, result.query.name, result.err))
		}
	}
	return warnings
}
