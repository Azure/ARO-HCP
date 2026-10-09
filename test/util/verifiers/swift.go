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

package verifiers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/test/util/config"
)

// VerifySwiftRouterChecks waits for complete, fresh runtime checks from every current
// router of the exact customer resource. It needs Kusto access, not an MC kubeconfig.
func VerifySwiftRouterChecks(ctx context.Context, credential azcore.TokenCredential, resourceID string, start time.Time, workers int, timeout time.Duration) error {
	if resourceID == "" || start.IsZero() || workers <= 0 || timeout <= 0 {
		return fmt.Errorf("SWIFT checks require a resource ID, test start, positive worker count and timeout")
	}
	cfg, err := config.GetServiceConfig()
	if err != nil {
		return err
	}
	var settings [3]string
	for i, path := range []string{"kusto.kustoName", "kusto.location", "kusto.serviceLogsDatabase"} {
		settings[i], err = config.GetStringByPath(cfg, path)
		if err != nil {
			return err
		}
	}
	endpoint := fmt.Sprintf("https://%s.%s.kusto.windows.net", settings[0], settings[1])
	client, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(endpoint).WithTokenCredential(credential))
	if err != nil {
		return fmt.Errorf("create SWIFT Kusto client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			ginkgo.GinkgoLogr.Error(err, "Close SWIFT Kusto client")
		}
	}()
	return pollUntilReady(ctx, "VerifySwiftRouterChecks "+resourceID, timeout, DefaultPollInterval, nil, 0, nil, func(ctx context.Context) error {
		queryCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		dataset, err := client.IterativeQuery(queryCtx, settings[2], swiftRouterQuery(resourceID, start))
		if err != nil {
			return fmt.Errorf("query SWIFT checks in %s/%s: %w", endpoint, settings[2], err)
		}
		defer dataset.Close()
		var rows []swiftRouterRow
		for table := range dataset.Tables() {
			if err := table.Err(); err != nil {
				return fmt.Errorf("SWIFT query table: %w", err)
			}
			if table.Table() == nil {
				return fmt.Errorf("SWIFT query returned a nil table")
			}
			for result := range table.Table().Rows() {
				if err := result.Err(); err != nil {
					return fmt.Errorf("SWIFT query row: %w", err)
				}
				if !table.Table().IsPrimaryResult() {
					continue
				}
				var row swiftRouterRow
				if err := result.Row().ToStruct(&row); err != nil {
					return fmt.Errorf("decode SWIFT query row: %w", err)
				}
				rows = append(rows, row)
			}
		}
		return checkSwiftRouterRows(rows, start, time.Now(), workers)
	})
}

func swiftRouterQuery(resourceID string, start time.Time) *kql.Builder {
	// Mapping documents need not change after test start. Runtime evidence must.
	// Select current documents before inspecting manifests so missing content and
	// soft-delete tombstones cannot resurrect an older HostedCluster mapping.
	// Fleet IDs can repeat across installations in the same environment/region,
	// so retain the source service cluster until resolving the AKS cluster name.
	return kql.New("let target = ").AddString(resourceID).AddLiteral("; let start = ").AddDateTime(start).AddLiteral(`;
let desires = cosmosResourceSnapshots
| where resourceID startswith strcat(target, "/")
| where resourceType =~ "microsoft.redhatopenshift/hcpopenshiftclusters/readdesires"
| extend resourceID = tolower(resourceID), ts = tolong(content._ts), sourceCluster = cluster
| summarize arg_max(timestamp, *) by environment, region, sourceCluster, resourceID, ts
| summarize arg_max(ts, *) by environment, region, sourceCluster, resourceID
| where isempty(content.deletionTimestamp)
| extend manifest = content.properties.status.kubeContent
| where manifest.kind == "HostedCluster" and isempty(manifest.metadata.deletionTimestamp)
| extend hostedClusterNamespace = tostring(manifest.metadata.namespace), hostedClusterName = tostring(manifest.metadata.name),
    managementCluster = tolower(tostring(content.properties.spec.managementCluster))
| where isnotempty(hostedClusterNamespace) and isnotempty(hostedClusterName) and isnotempty(managementCluster)
| project environment, region, sourceCluster, managementCluster, namespace = strcat(hostedClusterNamespace, "-", hostedClusterName);
let fleet = cosmosResourceSnapshots
| where resourceType =~ "microsoft.redhatopenshift/stamps/managementclusters"
| extend managementCluster = tolower(resourceID), ts = tolong(content._ts), sourceCluster = cluster
| join kind=leftsemi desires on environment, region, sourceCluster, managementCluster
| summarize arg_max(timestamp, *) by environment, region, sourceCluster, managementCluster, ts
| summarize arg_max(ts, *) by environment, region, sourceCluster, managementCluster
| where isempty(content.deletionTimestamp)
| extend clusterName = tostring(split(tostring(content.properties.status.aksResourceID), "/")[-1])
| where isnotempty(clusterName)
| project environment, region, sourceCluster, managementCluster, cluster = clusterName;
let hcp = desires
| join kind=inner fleet on environment, region, sourceCluster, managementCluster
| distinct environment, region, cluster, namespace;
let inventory = materialize(kubernetesResourceSnapshots
| where timestamp >= start and objectKind in ("Pod", "Deployment")
| join kind=leftsemi hcp on environment, region, cluster, namespace
| summarize arg_max(timestamp, *) by environment, region, cluster, namespace, objectKind, name
| where event != "Delete" and isempty(object.metadata.deletionTimestamp));
let pods = inventory
| where objectKind == "Pod" and object.metadata.labels.app == "private-router" and object.status.phase == "Running"
| project environment, region, cluster, namespace, UID = uid, Node = tostring(object.spec.nodeName);
let counts = pods | summarize Current = count() by environment, region, cluster, namespace;
let deployments = inventory
| where objectKind == "Deployment" and name == "router"
| project environment, region, cluster, namespace, Desired = tolong(object.spec.replicas);
let summaries = containerLogs
| where timestamp >= start and container_name == "swift-recorder"
| where log.controller_name == "swift-router-check" and log.field == "summary"
| extend namespace = tostring(log.pod_namespace), UID = tostring(log.pod_uid)
| join kind=leftsemi hcp on environment, region, cluster, namespace
| summarize arg_max(timestamp, *) by environment, region, cluster, namespace, UID
| project environment, region, cluster, namespace, UID, SummaryUID = tostring(log.pod_uid),
    SummaryNode = tostring(log.node_name), Pass = tostring(log.pass_id), At = timestamp, Summary = tostring(log.record);
hcp
| join kind=leftouter deployments on environment, region, cluster, namespace
| join kind=leftouter counts on environment, region, cluster, namespace
| join kind=leftouter pods on environment, region, cluster, namespace
| join kind=leftouter summaries on environment, region, cluster, namespace, UID
| project Scope = strcat(environment, "/", region, "/", cluster, "/", namespace),
    Desired = coalesce(Desired, 0), Current = coalesce(Current, 0), UID, Node, SummaryUID, SummaryNode, Pass, At, Summary
| order by Scope asc, UID asc
| take 101`)
}

type swiftRouterRow struct {
	Scope, UID, Node, SummaryUID, SummaryNode, Pass, Summary string
	Desired, Current                                         int64
	At                                                       time.Time
}

type swiftSummary struct {
	Outcome      string         `json:"outcome"`
	Partial      bool           `json:"partial"`
	Canceled     bool           `json:"canceled"`
	Discovered   int            `json:"discovered_targets"`
	Submitted    int            `json:"submitted_targets"`
	Reported     int            `json:"reported_targets"`
	Omitted      int            `json:"omitted_targets"`
	Roles        map[string]int `json:"roles"`
	HTTPSuccess  int            `json:"http_success"`
	HTTPFailed   int            `json:"http_failed"`
	TCPConnected int            `json:"tcp_connected"`
	TCPRefused   int            `json:"tcp_refused"`
	TCPTimeout   int            `json:"tcp_timeout"`
	TCPFailed    int            `json:"tcp_failed"`
	DNSSuccess   int            `json:"dns_success"`
	DNSFailed    int            `json:"dns_failed"`
	DNSReported  int            `json:"dns_reported"`
	DNSExpected  int            `json:"dns_expected"`
}

func checkSwiftRouterRows(rows []swiftRouterRow, start, now time.Time, workers int) error {
	if len(rows) == 0 {
		return fmt.Errorf("no customer resource ID -> live ReadDesire HostedCluster -> fleet management cluster mapping in Kusto")
	}
	first := rows[0]
	if len(rows) > 100 || first.Desired <= 0 || first.Current < first.Desired || int64(len(rows)) != first.Current {
		return fmt.Errorf("%s: expected all current routers >= desired replicas > 0; desired=%d current=%d rows=%d", first.Scope, first.Desired, first.Current, len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Scope != first.Scope || row.Desired != first.Desired || row.Current != first.Current || row.UID == "" || seen[row.UID] {
			return fmt.Errorf("ambiguous SWIFT router inventory: scope=%s uid=%s", row.Scope, row.UID)
		}
		seen[row.UID] = true
		if row.SummaryUID != row.UID || row.Node == "" || row.SummaryNode != row.Node || row.Pass == "" || row.At.Before(start) || row.At.Before(now.Add(-8*time.Minute)) || row.At.After(now) {
			return fmt.Errorf("%s router=%s node=%s: expected matching summary after test start and within 8m; summaryUID=%s node=%s pass=%s at=%s", row.Scope, row.UID, row.Node, row.SummaryUID, row.SummaryNode, row.Pass, row.At.Format(time.RFC3339))
		}
		var s swiftSummary
		if err := json.Unmarshal([]byte(row.Summary), &s); err != nil {
			return fmt.Errorf("router=%s pass=%s: decode summary: %w", row.UID, row.Pass, err)
		}
		if s.Outcome != "healthy" || s.Partial || s.Canceled || s.Discovered <= 0 || s.Submitted != s.Discovered || s.Reported != s.Discovered || s.Omitted != 0 {
			return fmt.Errorf("router=%s pass=%s: expected healthy complete coverage: %s", row.UID, row.Pass, row.Summary)
		}
		for _, role := range []string{"haproxy-ready", "router-loopback", "router-management", "router-swift", "ignition-server-service", "ignition-server-endpoint", "ignition-server-proxy-service", "ignition-server-proxy-endpoint", "kas-router-loopback", "kas-router-management", "kas-router-swift", "kas-service", "kas-endpoint", "peer-management", "peer-swift", "kas-peer-management", "kas-peer-swift", "worker-outbound"} {
			minimum := 1
			switch role {
			case "peer-management", "peer-swift", "kas-peer-management", "kas-peer-swift":
				minimum = int(row.Current) - 1
			case "worker-outbound":
				minimum = workers
			}
			if s.Roles[role] < minimum {
				return fmt.Errorf("router=%s pass=%s: role %s expected >=%d targets, got %d", row.UID, row.Pass, role, minimum, s.Roles[role])
			}
		}
		roleTargets := 0
		for _, count := range s.Roles {
			roleTargets += count
		}
		// Recorder HTTP success requires TLSVerify && TLSVerified for every HTTPS target.
		if roleTargets != s.Discovered || s.HTTPSuccess != s.Discovered-s.Roles["worker-outbound"] || s.TCPConnected+s.TCPRefused != s.Roles["worker-outbound"] || s.HTTPFailed != 0 || s.TCPTimeout != 0 || s.TCPFailed != 0 || s.DNSFailed != 0 || s.DNSExpected < 12 || s.DNSSuccess != s.DNSExpected || s.DNSReported != s.DNSExpected {
			return fmt.Errorf("router=%s pass=%s: expected all HTTP/TLS, worker TCP and DNS checks successful: %s", row.UID, row.Pass, row.Summary)
		}
	}
	return nil
}
