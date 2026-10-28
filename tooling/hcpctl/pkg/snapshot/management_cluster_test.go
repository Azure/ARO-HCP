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
		name     string
		requests []frontendRequest
		wantErr  string
		wantData queryData
	}{
		{
			name:    "no requests",
			wantErr: "no frontend request provided a cluster resource ID and subscription ID",
		},
		{
			name: "request missing placement inputs is skipped",
			requests: []frontendRequest{
				{ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"},
				{SubscriptionID: "sub"},
			},
			wantErr: "no frontend request provided a cluster resource ID and subscription ID",
		},
		{
			name: "valid request supplies placement inputs",
			requests: []frontendRequest{
				{ClusterResourceID: "incomplete"},
				{
					SubscriptionID:    "sub",
					ClusterResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
				},
			},
			wantData: queryData{
				ServiceClusterName: "ci01-j5162112-svc",
				ResourceGroup:      "test-resource-group",
				SubscriptionID:     "sub",
				ClusterResourceID:  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
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
			if got.SubscriptionID != tt.wantData.SubscriptionID || got.ClusterResourceID != tt.wantData.ClusterResourceID {
				t.Fatalf("managementClusterResolverData() = %+v, want %+v", got, tt.wantData)
			}
			if got.ServiceClusterName != seedData.ServiceClusterName || got.ResourceGroup != seedData.ResourceGroup {
				t.Fatalf("managementClusterResolverData() lost seed data: got %+v", got)
			}
		})
	}
}

func TestManagementClusterFromRows(t *testing.T) {
	tests := []struct {
		name    string
		rows    []resultRow
		want    string
		wantErr string
	}{
		{
			name: "valid row",
			rows: []resultRow{{
				columns: []string{"managementCluster"},
				values:  []string{"ci01-j5162112-mgmt-2"},
			}},
			want: "ci01-j5162112-mgmt-2",
		},
		{
			name:    "no rows",
			wantErr: "returned no results",
		},
		{
			name: "ambiguous rows",
			rows: []resultRow{
				{columns: []string{"managementCluster"}, values: []string{"mgmt-1"}},
				{columns: []string{"managementCluster"}, values: []string{"mgmt-2"}},
			},
			wantErr: "returned 2 distinct results",
		},
		{
			name: "missing column",
			rows: []resultRow{{
				columns: []string{"clusterName"},
				values:  []string{"ci01-j5162112-mgmt-2"},
			}},
			wantErr: "missing the managementCluster column",
		},
		{
			name: "empty value",
			rows: []resultRow{{
				columns: []string{"managementCluster"},
				values:  []string{""},
			}},
			wantErr: "empty managementCluster value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := managementClusterFromRows(tt.rows)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("managementClusterFromRows() error = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("managementClusterFromRows() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("managementClusterFromRows() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagementClusterQueryIsTargeted(t *testing.T) {
	rendered, err := renderQuery(managementClusterQuery.templatePath, queryData{
		ClusterURI:         "https://kusto.example.com",
		ServiceDatabase:    "service-db",
		ServiceClusterName: "ci01-j5162112-svc",
		SubscriptionID:     "sub",
		ResourceGroup:      "rg",
		ClusterResourceID:  "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster",
		FullStartTime:      time.Unix(0, 0).UTC(),
		FullEndTime:        time.Unix(60, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("renderQuery() error = %v", err)
	}

	assertions := []string{
		"| where timestamp <= datetime(1970-01-01T00:01:00Z)",
		"| where cluster == 'ci01-j5162112-svc'",
		"| where resourceID startswith '/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/'",
		"| where resourceType =~ 'microsoft.redhatopenshift/hcpopenshiftclusters/readdesires'",
		"| summarize arg_max(timestamp, *) by cluster, resourceID, ts",
		"| summarize arg_max(ts, *) by cluster, resourceID",
		"| where resourceType =~ 'microsoft.redhatopenshift/stamps/managementclusters'",
		"| join kind=leftsemi desires on managementCluster",
		"| summarize arg_max(timestamp, *) by cluster, managementCluster, ts",
		"| summarize arg_max(ts, *) by cluster, managementCluster",
		"| extend managementClusterName = tostring(split(tostring(content.properties.status.aksResourceID), '/')[-1])",
		"| join kind=inner fleet on managementCluster",
		"| project managementCluster = managementClusterName",
	}
	for _, assertion := range assertions {
		if !strings.Contains(rendered, assertion) {
			t.Errorf("management cluster query does not contain %q:\n%s", assertion, rendered)
		}
	}
	if strings.Contains(rendered, "| where timestamp between (") {
		t.Errorf("management cluster query must allow placement documents that predate the test window:\n%s", rendered)
	}
	assertBalancedKQL(t, rendered)
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
	const wantFilter = "| where cluster in ('ci01-j5162112-svc', 'ci01-j5162112-mgmt-2')"
	filtered := 0
	for _, path := range templatePaths {
		t.Run(path, func(t *testing.T) {
			withCluster, err := renderQuery(path, queryData{
				ServiceClusterName:    "ci01-j5162112-svc",
				ManagementClusterName: "ci01-j5162112-mgmt-2",
				FullStartTime:         time.Unix(0, 0).UTC(),
				FullEndTime:           time.Unix(60, 0).UTC(),
			})
			if err != nil {
				t.Fatalf("renderQuery() error = %v", err)
			}
			if strings.Contains(withCluster, clusterFilter) {
				filtered++
				if !strings.Contains(withCluster, wantFilter) {
					t.Errorf("cluster filter does not use the resolved management cluster, want %q in:\n%s", wantFilter, withCluster)
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
