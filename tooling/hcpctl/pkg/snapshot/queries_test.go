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

package snapshot

import (
	"strings"
	"testing"
	"time"
)

func TestIgnitionQueries(t *testing.T) {
	tests := []struct {
		name     string
		category queryCategory
		database string
		markers  []string
	}{
		{
			name:     "ignitionServerLogs",
			category: categoryLogs,
			database: "hcp",
			markers: []string{
				".database('HostedControlPlaneLogs').table('containerLogs')",
				"namespace_name in ('hosted-cluster', 'hosted-control-plane')",
				"container_name == 'ignition-server'",
				"text = tostring(log)",
				"countof(text, 'Requested: /ignition')",
				"countof(text, 'Token not found')",
				"countof(text, 'Payload found in cache')",
				"countof(text, 'Invalid Authorization header value prefix')",
				"countof(text, 'Invalid token value')",
				"countof(text, 'Invalid ignition payload')",
				"tokenLookupFailures = sum(tokenMissLines)",
				"by minute = bin(timestamp, 1m), cluster, namespace_name, pod_name",
			},
		},
		{
			name:     "ignitionServerEvents",
			category: categoryResourceEvents,
			database: "service",
			markers: []string{
				".database('ServiceLogs').table('kubernetesEvents')",
				"eventNamespace in ('hosted-cluster', 'hosted-control-plane')",
				"sourceComponent == 'ignition-server'",
				"reason == 'GetPayloadFailed'",
				"firstObserved = min(timestamp)",
				"lastObserved = max(timestamp)",
				"maxRecordedEventCount = max(tolong(count))",
				"by cluster, eventNamespace, objectKind, objectName, reason, message",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec *querySpec
			for i := range allQueries {
				q := &allQueries[i]
				if q.component == "hypershift" && q.queryName == tt.name {
					if spec != nil {
						t.Fatalf("duplicate query registration for %s", tt.name)
					}
					spec = q
				}
			}
			if spec == nil {
				t.Fatalf("query %s is not registered", tt.name)
			}
			if spec.category != tt.category || spec.database != tt.database {
				t.Fatalf("query routing = (%s, %s), want (%s, %s)", spec.category, spec.database, tt.category, tt.database)
			}
			if spec.ready == nil {
				t.Fatal("query must require namespace discovery")
			}
			if spec.requiredWhen != nil {
				t.Fatal("empty ignition diagnostics must remain informational")
			}
			if readQueryReadme(*spec) == "" {
				t.Fatal("query README is not embedded")
			}

			data := queryData{
				ClusterURI:                  "https://example.com",
				ServiceDatabase:             "ServiceLogs",
				HCPDatabase:                 "HostedControlPlaneLogs",
				FullStartTime:               time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				FullEndTime:                 time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				PhaseStartTime:              time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
				PhaseEndTime:                time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC),
				ResourceName:                "target-pool",
				HostedClusterNamespace:      "hosted-cluster",
				HostedControlPlaneNamespace: "hosted-control-plane",
			}
			for _, resourceType := range []string{
				"microsoft.redhatopenshift/hcpopenshiftclusters",
				"Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
			} {
				data.ResourceType = resourceType
				if !spec.ready(data) {
					t.Errorf("query is not ready for resource type %s", resourceType)
				}
			}
			data.ResourceType = "microsoft.redhatopenshift/hcpopenshiftclusters/externalauths"
			if spec.ready(data) {
				t.Error("query must not run for unrelated resource types")
			}
			data.ResourceType = "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools"
			data.HostedClusterNamespace = ""
			if spec.ready(data) {
				t.Error("query must not run before HostedCluster namespace discovery")
			}
			data.HostedClusterNamespace = "hosted-cluster"

			rendered, err := renderQuery(spec.templatePath, data)
			if err != nil {
				t.Fatalf("render query: %v", err)
			}
			markers := append([]string{
				"cluster('https://example.com')",
				"timestamp between (datetime(2026-01-01T01:00:00Z) .. datetime(2026-01-01T02:00:00Z))",
			}, tt.markers...)
			for _, marker := range markers {
				if !strings.Contains(rendered, marker) {
					t.Errorf("rendered query is missing %q", marker)
				}
			}
			for _, unexpected := range []string{"{{", "target-pool", "2026-01-02", "| project text", "sum(count)", "| where cluster in"} {
				if strings.Contains(rendered, unexpected) {
					t.Errorf("rendered query unexpectedly contains %q", unexpected)
				}
			}

			data.ServiceClusterName = "service-cluster"
			data.ManagementClusterName = "management-cluster"
			rendered, err = renderQuery(spec.templatePath, data)
			if err != nil {
				t.Fatalf("render PR-scoped query: %v", err)
			}
			if !strings.Contains(rendered, "| where cluster in ('service-cluster', 'management-cluster')") {
				t.Error("PR-scoped query is missing its cluster filter")
			}

			data.HostedControlPlaneNamespace = ""
			rendered, err = renderQuery(spec.templatePath, data)
			if err != nil {
				t.Fatalf("render query without hosted control plane namespace: %v", err)
			}
			if !strings.Contains(rendered, "in ('hosted-cluster')") {
				t.Error("query must retain HostedCluster scope without an additional namespace")
			}
		})
	}
}
