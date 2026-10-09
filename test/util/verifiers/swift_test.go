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
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCheckSwiftRouterRows(t *testing.T) {
	now := time.Date(2026, 10, 7, 1, 20, 0, 0, time.UTC)
	start := now.Add(-20 * time.Minute)
	for _, tt := range []struct {
		name string
		edit func([]swiftRouterRow, *swiftSummary) []swiftRouterRow
		want string
	}{
		{name: "healthy current routers"},
		{name: "TCP refusal is valid reachability", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow {
			s.TCPRefused, s.TCPConnected = 2, 0
			return r
		}},
		{name: "missing mapping", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { return nil }, want: "no customer resource ID"},
		{name: "missing routers", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { return r[:1] }, want: "desired=3 current=3 rows=1"},
		{name: "one observed router cannot satisfy deployment", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].Current = 1; return r[:1] }, want: "desired=3 current=1"},
		{name: "missing deployment", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].Desired = 0; return r }, want: "desired replicas > 0"},
		{name: "wrong scope", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[1].Scope = "other-mc/hcp"; return r }, want: "ambiguous"},
		{name: "duplicate UID", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[1] = r[0]; return r }, want: "ambiguous"},
		{name: "wrong UID", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].SummaryUID = "retired"; return r }, want: "matching summary"},
		{name: "wrong node", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].SummaryNode = "other"; return r }, want: "matching summary"},
		{name: "missing summary", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].Pass = ""; return r }, want: "matching summary"},
		{name: "stale summary", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow {
			r[0].At = now.Add(-9 * time.Minute)
			return r
		}, want: "within 8m"},
		{name: "before test start", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow {
			r[0].At = start.Add(-time.Second)
			return r
		}, want: "after test start"},
		{name: "future summary", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow {
			r[0].At = now.Add(time.Second)
			return r
		}, want: "matching summary"},
		{name: "malformed summary", edit: func(r []swiftRouterRow, _ *swiftSummary) []swiftRouterRow { r[0].Summary = "{"; return r }, want: "decode summary"},
		{name: "failed outcome", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Outcome = "failed"; return r }, want: "healthy complete coverage"},
		{name: "partial", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Partial = true; return r }, want: "healthy complete coverage"},
		{name: "canceled", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Canceled = true; return r }, want: "healthy complete coverage"},
		{name: "unreported target", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Reported--; return r }, want: "healthy complete coverage"},
		{name: "no coverage", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Discovered = 0; return r }, want: "healthy complete coverage"},
		{name: "omitted target", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Omitted = 1; return r }, want: "healthy complete coverage"},
		{name: "missing KAS", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { delete(s.Roles, "kas-service"); return r }, want: "kas-service expected >=1"},
		{name: "missing peer", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Roles["peer-swift"] = 1; return r }, want: "peer-swift expected >=2"},
		{name: "missing worker", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.Roles["worker-outbound"] = 1; return r }, want: "worker-outbound expected >=2"},
		{name: "HTTP failure", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.HTTPFailed = 1; return r }, want: "all HTTP/TLS"},
		{name: "missing HTTP success", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.HTTPSuccess--; return r }, want: "all HTTP/TLS"},
		{name: "TCP timeout", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.TCPTimeout = 1; return r }, want: "all HTTP/TLS"},
		{name: "no DNS", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow {
			s.DNSExpected, s.DNSReported, s.DNSSuccess = 0, 0, 0
			return r
		}, want: "DNS checks"},
		{name: "missing DNS success", edit: func(r []swiftRouterRow, s *swiftSummary) []swiftRouterRow { s.DNSSuccess--; return r }, want: "DNS checks"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := swiftSummary{Outcome: "healthy", Discovered: 24, Submitted: 24, Reported: 24, HTTPSuccess: 22, TCPConnected: 2, DNSExpected: 12, DNSReported: 12, DNSSuccess: 12,
				Roles: map[string]int{"haproxy-ready": 1, "router-loopback": 1, "router-management": 1, "router-swift": 1,
					"peer-management": 2, "peer-swift": 2, "ignition-server-service": 1, "ignition-server-endpoint": 1,
					"ignition-server-proxy-service": 1, "ignition-server-proxy-endpoint": 1, "kas-router-loopback": 1,
					"kas-router-management": 1, "kas-router-swift": 1, "kas-peer-management": 2, "kas-peer-swift": 2,
					"kas-service": 1, "kas-endpoint": 2, "worker-outbound": 2}}
			var rows []swiftRouterRow
			for _, uid := range []string{"router-a", "router-b", "router-c"} {
				rows = append(rows, swiftRouterRow{Scope: "pers/westus3/mgmt-1/hcp", Desired: 3, Current: 3, UID: uid, SummaryUID: uid, Node: uid + "-node", SummaryNode: uid + "-node", Pass: uid + "-pass", At: now.Add(-time.Minute)})
			}
			if tt.edit != nil {
				rows = tt.edit(rows, &s)
			}
			data, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			for i := range rows {
				if rows[i].Summary == "" {
					rows[i].Summary = string(data)
				}
			}
			err = checkSwiftRouterRows(rows, start, now, 2)
			if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCheckSwiftRouterRowsRecordedSummary(t *testing.T) {
	// Actual healthy log.record body from the retained e61ffc115 cluster.
	const summary = `{
		"outcome":"healthy","partial":false,"canceled":false,
		"age_seconds":601,"cadence_seconds":300,"elapsed_ms":201,
		"discovered_targets":27,"submitted_targets":27,"reported_targets":27,"omitted_targets":0,
		"roles":{"haproxy-ready":1,"ignition-server-endpoint":2,"ignition-server-proxy-endpoint":2,
		"ignition-server-proxy-service":1,"ignition-server-service":1,"kas-endpoint":3,
		"kas-peer-management":2,"kas-peer-swift":2,"kas-router-loopback":1,"kas-router-management":1,
		"kas-router-swift":1,"kas-service":1,"peer-management":2,"peer-swift":2,
		"router-loopback":1,"router-management":1,"router-swift":1,"worker-outbound":2},
		"http_success":25,"http_failed":0,"tcp_connected":2,"tcp_refused":0,"tcp_timeout":0,"tcp_failed":0,
		"retired_failed":0,"retired_issues":0,"dns_success":12,"dns_failed":0,"dns_reported":12,
		"dns_expected":12,"truncated":0,"evidence_errors":0
	}`
	now := time.Date(2026, 10, 7, 2, 5, 0, 0, time.UTC)
	var rows []swiftRouterRow
	for _, uid := range []string{"router-a", "router-b", "router-c"} {
		rows = append(rows, swiftRouterRow{
			Scope:   "dev/westus3/pers-usw3stev-mgmt-1/ocm-arohcppers-2t9ld2sicdamq97in1jqjgiksi70grc4-a9v9q1b9t7s9x6w",
			Desired: 3, Current: 3, UID: uid, SummaryUID: uid, Node: uid + "-node", SummaryNode: uid + "-node",
			Pass: uid + "-pass", At: now.Add(-time.Minute), Summary: summary,
		})
	}
	if err := checkSwiftRouterRows(rows, now.Add(-20*time.Minute), now, 2); err != nil {
		t.Fatalf("recorded healthy summary rejected: %v", err)
	}
}

func TestSwiftRouterQuery(t *testing.T) {
	query := swiftRouterQuery("/subscriptions/customer's-cluster", time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)).String()
	for _, expected := range []string{
		`customer\'s-cluster`,
		`let desires = cosmosResourceSnapshots`,
		`resourceID startswith strcat(target, "/")`,
		`resourceType =~ "microsoft.redhatopenshift/hcpopenshiftclusters/readdesires"`,
		`resourceID = tolower(resourceID), ts = tolong(content._ts)`,
		`sourceCluster = cluster`,
		`arg_max(timestamp, *) by environment, region, sourceCluster, resourceID, ts`,
		`arg_max(ts, *) by environment, region, sourceCluster, resourceID` + "\n| where isempty(content.deletionTimestamp)\n| extend manifest = content.properties.status.kubeContent",
		`manifest.kind == "HostedCluster" and isempty(manifest.metadata.deletionTimestamp)`,
		`tostring(manifest.metadata.namespace)`, `tostring(manifest.metadata.name)`,
		`tolower(tostring(content.properties.spec.managementCluster))`,
		`strcat(hostedClusterNamespace, "-", hostedClusterName)`,
		`let fleet = cosmosResourceSnapshots`,
		`resourceType =~ "microsoft.redhatopenshift/stamps/managementclusters"`,
		`join kind=leftsemi desires on environment, region, sourceCluster, managementCluster`,
		`arg_max(timestamp, *) by environment, region, sourceCluster, managementCluster, ts`,
		`arg_max(ts, *) by environment, region, sourceCluster, managementCluster` + "\n| where isempty(content.deletionTimestamp)",
		`tostring(split(tostring(content.properties.status.aksResourceID), "/")[-1])`,
		`join kind=inner fleet on environment, region, sourceCluster, managementCluster`,
		`distinct environment, region, cluster, namespace`,
		`where timestamp >= start and objectKind in ("Pod", "Deployment")`,
		`where timestamp >= start and container_name == "swift-recorder"`,
		`join kind=leftsemi hcp on environment, region, cluster, namespace`,
		"SummaryUID", "| take 101",
	} {
		if !strings.Contains(query, expected) {
			t.Errorf("query missing %q", expected)
		}
	}
	for _, forbidden := range []string{"clustersServiceLogs", "object.spec.infraID", "contains", "has "} {
		if strings.Contains(query, forbidden) {
			t.Errorf("query must not use %q for resource identity", forbidden)
		}
	}
	if strings.Contains(strings.Split(query, "let inventory")[0], "timestamp >= start") {
		t.Fatal("unchanged mapping documents may predate test start")
	}
}
