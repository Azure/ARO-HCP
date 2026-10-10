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

func TestManagementClusterDiscovery(t *testing.T) {
	var discovery querySpec
	for _, q := range queriesByCategory(categoryResourceDiscovery) {
		if q.key() == "hypershift/managementCluster" {
			discovery = q
			break
		}
	}
	if discovery.storeResult == nil {
		t.Fatal("management cluster discovery is not registered")
	}

	seed := queryData{
		ClusterURI:                  "https://example.kusto.windows.net",
		ServiceDatabase:             "ServiceLogs",
		HCPDatabase:                 "HostedControlPlaneLogs",
		ServiceClusterName:          "job-svc",
		ManagementClusterName:       "job-mgmt-1",
		HostedClusterNamespace:      "ocm-cluster-id",
		HostedControlPlaneNamespace: "ocm-cluster-id-name",
		ResourceType:                "microsoft.redhatopenshift/hcpopenshiftclusters",
		FullStartTime:               time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
		FullEndTime:                 time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC),
	}
	if discovery.ready(queryData{}) || !discovery.ready(seed) {
		t.Fatal("placement discovery must wait for hosted cluster namespaces")
	}
	rendered, err := renderQuery(discovery.templatePath, seed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, seed.ManagementClusterName) || strings.Contains(rendered, seed.ServiceClusterName) {
		t.Fatalf("placement discovery is restricted to the configured clusters:\n%s", rendered)
	}
	for _, namespace := range []string{seed.HostedClusterNamespace, seed.HostedControlPlaneNamespace} {
		if !strings.Contains(rendered, namespace) {
			t.Errorf("placement discovery is missing namespace %q", namespace)
		}
	}

	for _, cluster := range []string{"job-mgmt-1", "job-mgmt-2"} {
		t.Run(cluster, func(t *testing.T) {
			resource := seed
			if err := discovery.storeResult(&resource, []resultRow{{values: []string{cluster}}}); err != nil {
				t.Fatal(err)
			}
			request := seed
			mergeResourceData(&request, resource)
			if request.ManagementClusterName != cluster {
				t.Fatalf("trace query retained %q instead of discovered placement %q", request.ManagementClusterName, cluster)
			}

			input := GatherInput{
				TimeWindow:       TimeWindow{Start: seed.FullStartTime, End: seed.FullEndTime},
				CleanupStartTime: seed.FullStartTime.Add(45 * time.Minute),
			}
			for _, phase := range computePhases(input) {
				data := resource
				data.PhaseStartTime, data.PhaseEndTime = phase.start, phase.end
				for _, name := range []string{"controlPlaneEvents", "hypershiftOperatorLogs", "clusterAPIProviderLogs", "events"} {
					t.Run(phase.name+"/"+name, func(t *testing.T) {
						query, err := renderQuery("queries/hypershift/"+name+"/query.kql", data)
						if err != nil {
							t.Fatal(err)
						}
						want := "| where cluster in ('job-svc', '" + cluster + "')"
						if !strings.Contains(query, want) {
							t.Fatalf("query is not scoped to discovered placement; want %q:\n%s", want, query)
						}
					})
				}
			}
		})
	}

	t.Run("ambiguous placement preserves fallback", func(t *testing.T) {
		data := seed
		err := discovery.storeResult(&data, []resultRow{
			{values: []string{"job-mgmt-1"}},
			{values: []string{"job-mgmt-2"}},
		})
		if err == nil {
			t.Fatal("expected ambiguous placement to report an error")
		}
		if data.ManagementClusterName != seed.ManagementClusterName {
			t.Fatal("ambiguous placement changed the configured fallback")
		}
	})
}
