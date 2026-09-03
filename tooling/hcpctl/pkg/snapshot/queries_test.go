// Copyright 2025 Microsoft Corporation
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

// fullQueryData returns a queryData with every field populated, so that every
// template renders down a non-empty branch.
func fullQueryData() queryData {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return queryData{
		ClusterURI:                  "https://example.kusto.windows.net",
		ServiceDatabase:             "ServiceLogs",
		HCPDatabase:                 "HostedControlPlaneLogs",
		MonitoringEventsDatabase:    "MonitoringEvents",
		ResourceGroup:               "rg-test",
		ServiceClusterName:          "svc-cluster",
		ManagementClusterName:       "mgmt-cluster",
		ManagementClusterNames:      []string{"mgmt-cluster-1", "mgmt-cluster-2"},
		FullStartTime:               now,
		FullEndTime:                 now.Add(time.Hour),
		PhaseStartTime:              now,
		PhaseEndTime:                now.Add(time.Hour),
		PhaseName:                   "test",
		CorrelationID:               "corr-1",
		ClientRequestID:             "req-1",
		ResponseStatusCode:          200,
		SubscriptionID:              "00000000-0000-0000-0000-000000000000",
		ResourceID:                  "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1",
		ResourceType:                "microsoft.redhatopenshift/hcpopenshiftclusters",
		ResourceName:                "c1",
		ClusterResourceID:           "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg-test/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1",
		ClusterResourceName:         "c1",
		ServiceProviderResourceType: "microsoft.redhatopenshift/hcpopenshiftclusters/serviceproviderclusters",
		AsyncOperationId:            "op-1",
		AsyncOperationPath:          "/subscriptions/x/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationsStatus/op-1",
		InternalID:                  "2abc",
		ClusterID:                   "2abc",
		HostedClusterNamespace:      "ocm-pers-2abc",
		HostedControlPlaneNamespace: "ocm-pers-2abc-c1",
		ChildResourceTypes:          map[string]bool{"microsoft.redhatopenshift/hcpopenshiftclusters/hcpopenshiftcontrollers": true},
	}
}

// TestAllQueryTemplatesRender ensures every registered query template parses and
// executes against a fully-populated queryData without error, and that a README
// exists for each. This guards embed paths and template syntax across the chain.
func TestAllQueryTemplatesRender(t *testing.T) {
	data := fullQueryData()
	all := append(append([]querySpec{}, allQueries...), contextQueries...)
	for _, q := range all {
		t.Run(q.key(), func(t *testing.T) {
			rendered, err := renderQuery(q.templatePath, data)
			if err != nil {
				t.Fatalf("renderQuery(%s) failed: %v", q.templatePath, err)
			}
			if strings.TrimSpace(rendered) == "" {
				t.Fatalf("renderQuery(%s) produced empty output", q.templatePath)
			}
			if readme := readQueryReadme(q); strings.TrimSpace(readme) == "" {
				t.Errorf("query %s has no README.md", q.key())
			}
		})
	}
}

// TestVeleroServerLogsClusterList checks the management-cluster IN-list renders
// from ManagementClusterNames, and collapses to no cluster filter when empty.
func TestVeleroServerLogsClusterList(t *testing.T) {
	const path = "queries/velero/serverLogs/query.kql"

	data := fullQueryData()
	rendered, err := renderQuery(path, data)
	if err != nil {
		t.Fatalf("renderQuery failed: %v", err)
	}
	if !strings.Contains(rendered, "cluster in ('mgmt-cluster-1', 'mgmt-cluster-2')") {
		t.Errorf("expected management-cluster IN-list, got:\n%s", rendered)
	}

	data.ManagementClusterNames = nil
	rendered, err = renderQuery(path, data)
	if err != nil {
		t.Fatalf("renderQuery (empty) failed: %v", err)
	}
	if strings.Contains(rendered, "| where cluster in (") {
		t.Errorf("expected no cluster filter when ManagementClusterNames empty, got:\n%s", rendered)
	}
}

// TestVeleroReadyGating verifies the ready predicates gate on the data each
// velero query depends on.
func TestVeleroReadyGating(t *testing.T) {
	byKey := map[string]querySpec{}
	for _, q := range allQueries {
		if q.component == "velero" {
			byKey[q.queryName] = q
		}
	}
	for _, name := range []string{"mgmtCluster", "backups", "schedules", "dataUploads", "deleteBackupRequests", "logs", "serverLogs"} {
		if _, ok := byKey[name]; !ok {
			t.Fatalf("velero/%s query not registered", name)
		}
	}

	// Empty data: nothing is ready.
	empty := queryData{}
	for name, q := range byKey {
		if q.ready != nil && q.ready(empty) {
			t.Errorf("velero/%s should not be ready with empty data", name)
		}
	}

	// Non-cluster resource type: velero queries stay gated off.
	np := fullQueryData()
	np.ResourceType = "microsoft.redhatopenshift/hcpopenshiftclusters/nodepools"
	for name, q := range byKey {
		if q.ready != nil && q.ready(np) {
			t.Errorf("velero/%s should not be ready for a nodepool", name)
		}
	}

	// Fully-populated cluster data: state + logs queries are ready; mgmtCluster
	// discovery is gated off because ManagementClusterNames is already seeded.
	full := fullQueryData()
	for _, name := range []string{"backups", "schedules", "dataUploads", "deleteBackupRequests", "logs", "serverLogs"} {
		if q := byKey[name]; q.ready != nil && !q.ready(full) {
			t.Errorf("velero/%s should be ready with full cluster data", name)
		}
	}
	if q := byKey["mgmtCluster"]; q.ready(full) {
		t.Error("velero/mgmtCluster should be gated off once ManagementClusterNames is seeded")
	}
	full.ManagementClusterNames = nil
	if q := byKey["mgmtCluster"]; !q.ready(full) {
		t.Error("velero/mgmtCluster should be ready when ManagementClusterNames is empty and HCPN known")
	}
	if q := byKey["serverLogs"]; q.ready(full) {
		t.Error("velero/serverLogs should be gated off when ManagementClusterNames is empty")
	}
}

// TestVeleroMgmtClusterStoreResult verifies discovery result collection.
func TestVeleroMgmtClusterStoreResult(t *testing.T) {
	var spec querySpec
	for _, q := range allQueries {
		if q.component == "velero" && q.queryName == "mgmtCluster" {
			spec = q
		}
	}
	d := &queryData{}
	rows := []resultRow{
		{columns: []string{"cluster"}, values: []string{"mgmt-a"}},
		{columns: []string{"cluster"}, values: []string{"mgmt-b"}},
		{columns: []string{"cluster"}, values: []string{""}},
	}
	if err := spec.storeResult(d, rows); err != nil {
		t.Fatalf("storeResult failed: %v", err)
	}
	if len(d.ManagementClusterNames) != 2 || d.ManagementClusterNames[0] != "mgmt-a" || d.ManagementClusterNames[1] != "mgmt-b" {
		t.Errorf("expected [mgmt-a mgmt-b], got %v", d.ManagementClusterNames)
	}
}
