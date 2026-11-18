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
	"io/fs"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagementClusterResolverData(t *testing.T) {
	seedData := queryData{
		ServiceClusterName: "ci01-j5162112-svc",
		ResourceGroup:      "test-resource-group",
	}

	tests := []struct {
		name        string
		requests    []frontendRequest
		wantErr     string
		wantTargets []managementClusterTarget
	}{
		{
			name:    "no requests",
			wantErr: "no frontend request provided a named HCP cluster resource ID and subscription ID",
		},
		{
			name: "request missing placement inputs is skipped",
			requests: []frontendRequest{
				{ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"},
				{SubscriptionID: "sub"},
			},
			wantErr: "no frontend request provided a named HCP cluster resource ID and subscription ID",
		},
		{
			name: "not-found top-level cluster GET is not a placement target",
			requests: []frontendRequest{
				{
					Method:            http.MethodGet,
					Status:            http.StatusNotFound,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
					ResourceType:      "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
					SubscriptionID:    "sub",
				},
			},
		},
		{
			name: "not-found cluster GET is skipped while hosted placements remain required",
			requests: []frontendRequest{
				{
					Method:            http.MethodGet,
					Status:            http.StatusNotFound,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b",
					ResourceType:      "Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b",
					SubscriptionID:    "sub",
				},
				{
					Status:            http.StatusOK,
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					SubscriptionID:    "sub",
				},
			},
			wantTargets: []managementClusterTarget{{
				SubscriptionID:    "sub",
				ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-a",
			}},
		},
		{
			name: "admission-rejected create is optional beside a hosted cluster",
			requests: []frontendRequest{
				{
					Method:            http.MethodPut,
					Status:            http.StatusAccepted,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					ResourceType:      hcpOpenShiftClusterResourceType,
					ResourceName:      "cluster-a",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					SubscriptionID:    "sub",
				},
				{
					Method:            http.MethodPut,
					Status:            http.StatusBadRequest,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b",
					ResourceType:      hcpOpenShiftClusterResourceType,
					ResourceName:      "cluster-b",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b",
					SubscriptionID:    "sub",
				},
			},
			wantTargets: []managementClusterTarget{
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-a",
				},
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-b",
					Optional:          true,
				},
			},
		},
		{
			name: "failed create remains required when another request targets the same cluster",
			requests: []frontendRequest{
				{
					Method:            http.MethodPut,
					Status:            http.StatusBadRequest,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					ResourceType:      hcpOpenShiftClusterResourceType,
					ResourceName:      "cluster-a",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					SubscriptionID:    "sub",
				},
				{
					Method:            http.MethodGet,
					Status:            http.StatusOK,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					ResourceType:      hcpOpenShiftClusterResourceType,
					ResourceName:      "cluster-a",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
					SubscriptionID:    "sub",
				},
			},
			wantTargets: []managementClusterTarget{{
				SubscriptionID:    "sub",
				ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-a",
			}},
		},
		{
			name: "cluster collection GET with resource-group fallback is not a placement target",
			requests: []frontendRequest{{
				Method:            http.MethodGet,
				Status:            http.StatusOK,
				ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters",
				ResourceType:      hcpOpenShiftClusterResourceType,
				ClusterResourceID: "/subscriptions/sub/resourceGroups/rg",
				SubscriptionID:    "sub",
			}},
		},
		{
			name: "not-found child GET still requires parent cluster placement",
			requests: []frontendRequest{
				{
					Method:            http.MethodGet,
					Status:            http.StatusNotFound,
					ResourceID:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/nodePools/pool",
					ResourceType:      "Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
					SubscriptionID:    "sub",
				},
			},
			wantTargets: []managementClusterTarget{{
				SubscriptionID:    "sub",
				ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster",
			}},
		},
		{
			name: "all distinct cluster requests supply placement inputs",
			requests: []frontendRequest{
				{ClusterResourceID: "incomplete"},
				{
					SubscriptionID:    "SUB",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b/",
				},
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a",
				},
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/SUBSCRIPTIONS/SUB/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.REDHATOPENSHIFT/HCPOPENSHIFTCLUSTERS/CLUSTER-A/",
				},
			},
			wantTargets: []managementClusterTarget{
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-a",
				},
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster-b",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := managementClusterResolverData(seedData, tt.requests)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("managementClusterResolverData() error = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("managementClusterResolverData() error = %v", err)
			}
			if !reflect.DeepEqual(got.ManagementClusterTargets, tt.wantTargets) {
				t.Fatalf("managementClusterResolverData() targets = %+v, want %+v", got.ManagementClusterTargets, tt.wantTargets)
			}
			if got.ServiceClusterName != seedData.ServiceClusterName || got.ResourceGroup != seedData.ResourceGroup {
				t.Fatalf("managementClusterResolverData() lost seed data: got %+v", got)
			}
		})
	}
}

func TestManagementClustersFromRows(t *testing.T) {
	clusterA := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-a"
	clusterB := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster-b"
	targets := []managementClusterTarget{
		{ClusterResourceID: clusterA, SubscriptionID: "sub"},
		{ClusterResourceID: clusterB, SubscriptionID: "sub"},
	}
	tests := []struct {
		name    string
		rows    []resultRow
		targets []managementClusterTarget
		want    []string
		wantErr string
	}{
		{
			name: "single cluster placement",
			rows: []resultRow{{
				columns: []string{"clusterResourceID", "managementCluster"},
				values:  []string{clusterA, "ci01-j5162112-mgmt-2"},
			}},
			targets: targets[:1],
			want:    []string{"ci01-j5162112-mgmt-2"},
		},
		{
			name: "different clusters may have different placements",
			rows: []resultRow{
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterA, "ci01-j5162112-mgmt-1"}},
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterB, "ci01-j5162112-mgmt-2"}},
			},
			targets: targets,
			want:    []string{"ci01-j5162112-mgmt-1", "ci01-j5162112-mgmt-2"},
		},
		{
			name: "same placement across clusters is deduplicated",
			rows: []resultRow{
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterA, "ci01-j5162112-mgmt-1"}},
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterB, "ci01-j5162112-mgmt-1"}},
			},
			targets: targets,
			want:    []string{"ci01-j5162112-mgmt-1"},
		},
		{
			name: "conflicting placement for one cluster is ambiguous",
			rows: []resultRow{
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterA, "mgmt-1"}},
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterA, "mgmt-2"}},
			},
			targets: targets[:1],
			wantErr: "2 distinct placements",
		},
		{
			name:    "no rows",
			wantErr: "returned no results",
		},
		{
			name: "missing optional rejected create placement is ignored",
			rows: []resultRow{{
				columns: []string{"clusterResourceID", "managementCluster"},
				values:  []string{clusterA, "mgmt-1"},
			}},
			targets: []managementClusterTarget{
				{ClusterResourceID: clusterA, SubscriptionID: "sub"},
				{ClusterResourceID: clusterB, SubscriptionID: "sub", Optional: true},
			},
			want: []string{"mgmt-1"},
		},
		{
			name: "no rows for optional rejected create placement",
			targets: []managementClusterTarget{{
				ClusterResourceID: clusterB,
				SubscriptionID:    "sub",
				Optional:          true,
			}},
			want: nil,
		},
		{
			name: "missing placement for a requested cluster",
			rows: []resultRow{
				{columns: []string{"clusterResourceID", "managementCluster"}, values: []string{clusterA, "mgmt-1"}},
			},
			targets: targets,
			wantErr: "no placement for cluster resource ID",
		},
		{
			name: "missing column",
			rows: []resultRow{{
				columns: []string{"managementCluster"},
				values:  []string{"ci01-j5162112-mgmt-2"},
			}},
			targets: targets[:1],
			wantErr: "missing the clusterResourceID column",
		},
		{
			name: "empty value",
			rows: []resultRow{{
				columns: []string{"clusterResourceID", "managementCluster"},
				values:  []string{clusterA, ""},
			}},
			targets: targets[:1],
			wantErr: "empty clusterResourceID or managementCluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := managementClustersFromRows(tt.rows, tt.targets)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("managementClustersFromRows() error = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("managementClustersFromRows() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("managementClustersFromRows() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagementClusterQueryIsTargeted(t *testing.T) {
	rendered, err := renderQuery(managementClusterQuery.templatePath, queryData{
		ClusterURI:         "https://kusto.example.com",
		ServiceDatabase:    "service-db",
		ServiceClusterName: "ci01-j5162112-svc",
		ResourceGroup:      "rg",
		ManagementClusterTargets: []managementClusterTarget{{
			SubscriptionID:    "sub",
			ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster",
		}},
		FullStartTime: time.Unix(0, 0).UTC(),
		FullEndTime:   time.Unix(60, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("renderQuery() error = %v", err)
	}

	assertions := []string{
		"cluster('https://kusto.example.com').database('service-db').table('cosmosResourceSnapshots')",
		"let targets = datatable(clusterResourceID:string, targetSubscriptionID:string)",
		"'/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster', 'sub'",
		"| where timestamp <= datetime(1970-01-01T00:01:00Z)",
		"| where cluster == 'ci01-j5162112-svc'",
		"| where tolower(subscriptionID) == targetSubscriptionID",
		"| where resourceID startswith strcat(clusterResourceID, '/')",
		"| where resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/readdesires'",
		"| summarize arg_max(timestamp, *) by cluster, resourceID, ts, clusterResourceID",
		"| summarize arg_max(ts, *) by cluster, resourceID, clusterResourceID",
		"content.properties.spec.targetItem.group",
		"content.properties.spec.targetItem.resource",
		"content.properties.spec.managementCluster",
		"| where resourceType =~ 'microsoft.redhatopenshift/stamps/managementclusters'",
		"| join kind=leftsemi desires on managementCluster",
		"| summarize arg_max(timestamp, *) by cluster, managementCluster, ts",
		"| summarize arg_max(ts, *) by cluster, managementCluster",
		"| extend managementClusterName = tolower(tostring(split(tostring(content.properties.status.aksResourceID), '/')[-1]))",
		"| join kind=inner fleet on managementCluster",
		"| project clusterResourceID, managementCluster = managementClusterName",
	}
	for _, assertion := range assertions {
		if !strings.Contains(rendered, assertion) {
			t.Errorf("management cluster query does not contain %q:\n%s", assertion, rendered)
		}
	}
	if got := strings.Count(rendered, "cluster('https://kusto.example.com').database('service-db').table('cosmosResourceSnapshots')"); got != 2 {
		t.Errorf("management cluster query must qualify both snapshot sources, got %d:\n%s", got, rendered)
	}
	if strings.Contains(rendered, "| where timestamp between (") {
		t.Errorf("management cluster query must allow placement documents that predate the test window:\n%s", rendered)
	}
	assertBalancedKQL(t, rendered)
}

func TestManagementClusterQueryPreservesPlacementDuringCleanup(t *testing.T) {
	rendered, err := renderQuery(managementClusterQuery.templatePath, queryData{
		ServiceClusterName: "ci01-j5162112-svc",
		ResourceGroup:      "rg",
		FullEndTime:        time.Unix(60, 0).UTC(),
		ManagementClusterTargets: []managementClusterTarget{{
			ClusterResourceID: "/subscriptions/sub/resourcegroups/rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/cluster",
			SubscriptionID:    "sub",
		}},
	})
	if err != nil {
		t.Fatalf("renderQuery() error = %v", err)
	}
	desiresQuery := strings.SplitN(rendered, "let fleet", 2)[0]

	cleanupStates := []struct {
		name           string
		obsoleteFilter string
	}{
		{name: "soft-deleted ReadDesire", obsoleteFilter: "where isempty(content.deletionTimestamp)"},
		{name: "terminating HostedCluster", obsoleteFilter: "manifest.metadata.deletionTimestamp"},
		{name: "cleared kubeContent", obsoleteFilter: "content.properties.status.kubeContent"},
	}
	for _, cleanupState := range cleanupStates {
		t.Run(cleanupState.name, func(t *testing.T) {
			if strings.Contains(desiresQuery, cleanupState.obsoleteFilter) {
				t.Errorf("ReadDesire placement is still discarded by %q:\n%s", cleanupState.obsoleteFilter, desiresQuery)
			}
			if !strings.Contains(desiresQuery, "content.properties.spec.targetItem.resource") {
				t.Errorf("ReadDesire query does not identify HostedClusters through spec.targetItem:\n%s", desiresQuery)
			}
			if !strings.Contains(desiresQuery, "content.properties.spec.managementCluster") {
				t.Errorf("ReadDesire query does not retain placement from spec.managementCluster:\n%s", desiresQuery)
			}
		})
	}
}

func assertBalancedKQL(t *testing.T, query string) {
	t.Helper()

	depth := 0
	var quote rune
	escaped := false
	for _, r := range query {
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			switch r {
			case '\\':
				escaped = true
			case quote:
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				t.Fatalf("KQL has an unmatched closing parenthesis:\n%s", query)
			}
		}
	}
	if quote != 0 {
		t.Fatalf("KQL has an unterminated string literal:\n%s", query)
	}
	if depth != 0 {
		t.Fatalf("KQL has %d unmatched opening parentheses:\n%s", depth, query)
	}
}

func TestQueryTemplatesFilterResolvedManagementCluster(t *testing.T) {
	var templatePaths []string
	err := fs.WalkDir(queriesFS, "queries", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".kql") ||
			strings.HasPrefix(path, "queries/partials/") ||
			path == managementClusterQuery.templatePath ||
			path == frontendRequestQuery.templatePath {
			return nil
		}
		templatePaths = append(templatePaths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk embedded queries: %v", err)
	}

	const clusterFilter = "| where cluster in ("
	const wantFilter = "| where cluster in ('ci01-j5162112-svc', 'ci01-j5162112-mgmt-1', 'ci01-j5162112-mgmt-2')"
	filtered := 0
	for _, path := range templatePaths {
		t.Run(path, func(t *testing.T) {
			withCluster, err := renderQuery(path, queryData{
				ServiceClusterName:                 "ci01-j5162112-svc",
				ManagementClusterNames:             []string{"ci01-j5162112-mgmt-1", "ci01-j5162112-mgmt-2"},
				ManagementClusterPlacementResolved: true,
				FullStartTime:                      time.Unix(0, 0).UTC(),
				FullEndTime:                        time.Unix(60, 0).UTC(),
			})
			if err != nil {
				t.Fatalf("renderQuery() error = %v", err)
			}
			if strings.Contains(withCluster, clusterFilter) {
				filtered++
				if !strings.Contains(withCluster, wantFilter) {
					t.Errorf("cluster filter does not use the resolved management cluster, want %q in:\n%s", wantFilter, withCluster)
				}

				serviceOnly, err := renderQuery(path, queryData{
					ServiceClusterName:                 "ci01-j5162112-svc",
					ManagementClusterPlacementResolved: true,
					FullStartTime:                      time.Unix(0, 0).UTC(),
					FullEndTime:                        time.Unix(60, 0).UTC(),
				})
				if err != nil {
					t.Fatalf("renderQuery() with no resolved management clusters error = %v", err)
				}
				if !strings.Contains(serviceOnly, "| where cluster in ('ci01-j5162112-svc')") {
					t.Errorf("cluster filter must stay scoped to the service cluster when no placement is resolved:\n%s", serviceOnly)
				}
			}

			withoutManagementCluster, err := renderQuery(path, queryData{
				ServiceClusterName: "ci01-j5162112-svc",
				FullStartTime:      time.Unix(0, 0).UTC(),
				FullEndTime:        time.Unix(60, 0).UTC(),
			})
			if err != nil {
				t.Fatalf("renderQuery() without management cluster error = %v", err)
			}
			if strings.Contains(withoutManagementCluster, clusterFilter) {
				t.Errorf("cluster filter rendered without a management cluster:\n%s", withoutManagementCluster)
			}
		})
	}
	if filtered == 0 {
		t.Fatal("no query template rendered a management cluster filter")
	}
}

func TestFrontendRequestQueryFiltersOnlyServiceCluster(t *testing.T) {
	rendered, err := renderQuery(frontendRequestQuery.templatePath, queryData{
		ClusterURI:         "https://kusto.example.com",
		ServiceDatabase:    "service-db",
		ServiceClusterName: "ci01-j5162112-svc",
		ResourceGroup:      "rg",
		FullStartTime:      time.Unix(0, 0).UTC(),
		FullEndTime:        time.Unix(60, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("renderQuery() error = %v", err)
	}
	if !strings.Contains(rendered, "| where cluster == 'ci01-j5162112-svc'") {
		t.Fatalf("frontend request query does not filter on the service cluster:\n%s", rendered)
	}
	if strings.Contains(rendered, "ManagementClusterName") || strings.Contains(rendered, "managementCluster") {
		t.Fatalf("frontend request query depends on management-cluster placement before it is resolved:\n%s", rendered)
	}
}
