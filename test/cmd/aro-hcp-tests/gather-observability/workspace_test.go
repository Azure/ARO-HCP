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

package gatherobservability

import (
	"fmt"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement"
)

func mustParseResourceID(sub, rg, name string) *azcorearm.ResourceID {
	id, err := azcorearm.ParseResourceID(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Monitor/accounts/%s", sub, rg, name))
	if err != nil {
		panic(err)
	}
	return id
}

func TestAlertBelongsToWorkspace(t *testing.T) {
	t.Parallel()
	ws := mustParseResourceID("sub-123", "my-rg", "services-westus3")

	tests := []struct {
		name                string
		monitoringWorkspace string
		want                bool
	}{
		{
			name:                "matching_workspace",
			monitoringWorkspace: ws.String(),

			want: true,
		},
		{
			name:                "different_workspace",
			monitoringWorkspace: mustParseResourceID("sub-123", "my-rg", "hcps-westus3").String(),
			want:                false,
		},
		{
			name:                "empty_no_match",
			monitoringWorkspace: "",
			want:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := alert{Metadata: alertMetadata{MonitoringWorkspace: tt.monitoringWorkspace}}
			got := alertBelongsToWorkspace(a, *ws)
			if got != tt.want {
				t.Errorf("alertBelongsToWorkspace() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsWorkspaceTargeted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		monitoringWorkspace string
		want                bool
	}{
		{
			name:                "azure_monitor_workspace",
			monitoringWorkspace: "/subscriptions/sub-123/resourceGroups/my-rg/providers/Microsoft.Monitor/accounts/svc-westus3",
			want:                true,
		},
		{
			name:                "azure_monitor_workspace_case_insensitive",
			monitoringWorkspace: "/subscriptions/sub-123/resourceGroups/my-rg/providers/microsoft.monitor/accounts/svc-westus3",
			want:                true,
		},
		{
			name:                "cosmos_db_account",
			monitoringWorkspace: "/subscriptions/sub-123/resourceGroups/my-rg/providers/Microsoft.DocumentDB/databaseAccounts/my-cosmos",
			want:                false,
		},
		{
			name:                "kusto_cluster",
			monitoringWorkspace: "/subscriptions/sub-123/resourceGroups/my-rg/providers/Microsoft.Kusto/clusters/my-kusto",
			want:                false,
		},
		{
			name:                "empty_string",
			monitoringWorkspace: "",
			want:                false,
		},
		{
			name:                "unparseable_string",
			monitoringWorkspace: "not-a-resource-id",
			want:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := alert{Metadata: alertMetadata{MonitoringWorkspace: tt.monitoringWorkspace}}
			got := isWorkspaceTargeted(a)
			if got != tt.want {
				t.Errorf("isWorkspaceTargeted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildInfraAlertData(t *testing.T) {
	t.Parallel()

	sev3 := armalertsmanagement.SeveritySev3
	allAlerts := []alert{
		{
			Alert:    alertData{Name: "PrometheusAlert1", Severity: sev3},
			Metadata: alertMetadata{MonitoringWorkspace: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Monitor/accounts/svc"},
		},
		{
			Alert:    alertData{Name: "CosmosAlert", Severity: sev3},
			Metadata: alertMetadata{MonitoringWorkspace: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/cosmos"},
		},
		{
			Alert:    alertData{Name: "KustoAlert", Severity: sev3},
			Metadata: alertMetadata{MonitoringWorkspace: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Kusto/clusters/kusto"},
		},
		{
			Alert:    alertData{Name: "PrometheusAlert2", Severity: sev3},
			Metadata: alertMetadata{MonitoringWorkspace: "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Monitor/accounts/hcp"},
		},
	}
	metricRules := []string{"CosmosAlert", "KustoAlert"}

	result := buildInfraAlertData(allAlerts, metricRules, -1, nil)

	if result.Type != workspaceInfra {
		t.Errorf("Type = %q, want %q", result.Type, workspaceInfra)
	}
	if len(result.FiredAlerts) != 2 {
		t.Fatalf("FiredAlerts count = %d, want 2", len(result.FiredAlerts))
	}
	names := []string{result.FiredAlerts[0].Alert.Name, result.FiredAlerts[1].Alert.Name}
	slices.Sort(names)
	if names[0] != "CosmosAlert" || names[1] != "KustoAlert" {
		t.Errorf("FiredAlerts names = %v, want [CosmosAlert KustoAlert]", names)
	}
	for _, a := range result.FiredAlerts {
		if a.Metadata.MonitoringWorkspaceType != workspaceInfra {
			t.Errorf("alert %q MonitoringWorkspaceType = %q, want %q", a.Alert.Name, a.Metadata.MonitoringWorkspaceType, workspaceInfra)
		}
	}
	if !slices.Equal(result.AlertRules, metricRules) {
		t.Errorf("AlertRules = %v, want %v", result.AlertRules, metricRules)
	}
}

func TestUniqueResourceGroups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workspaces map[string]azcorearm.ResourceID
		want       sets.Set[string]
	}{
		{
			name: "same_resource_group_deduped",
			workspaces: map[string]azcorearm.ResourceID{
				"svc": *mustParseResourceID("sub-1", "rg-1", "svc-ws"),
				"hcp": *mustParseResourceID("sub-1", "rg-1", "hcp-ws"),
			},
			want: sets.New[string]("/subscriptions/sub-1/resourceGroups/rg-1"),
		},
		{
			name: "different_resource_groups",
			workspaces: map[string]azcorearm.ResourceID{
				"svc": *mustParseResourceID("sub-1", "rg-1", "svc-ws"),
				"hcp": *mustParseResourceID("sub-1", "rg-2", "hcp-ws"),
			},
			want: sets.New[string](
				"/subscriptions/sub-1/resourceGroups/rg-1",
				"/subscriptions/sub-1/resourceGroups/rg-2",
			),
		},
		{
			name: "different_subscriptions",
			workspaces: map[string]azcorearm.ResourceID{
				"svc": *mustParseResourceID("sub-1", "rg-1", "svc-ws"),
				"hcp": *mustParseResourceID("sub-2", "rg-1", "hcp-ws"),
			},
			want: sets.New[string](
				"/subscriptions/sub-1/resourceGroups/rg-1",
				"/subscriptions/sub-2/resourceGroups/rg-1",
			),
		},
		{
			name:       "empty_workspaces",
			workspaces: map[string]azcorearm.ResourceID{},
			want:       sets.New[string](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := uniqueResourceGroups(tt.workspaces)
			if !got.Equal(tt.want) {
				t.Errorf("uniqueResourceGroups() = %v, want %v", sets.List(got), sets.List(tt.want))
			}
		})
	}
}

func TestScopeContainsWorkspace(t *testing.T) {
	t.Parallel()
	wsPtr := mustParseResourceID("sub-123", "my-rg", "hcps-westus3")
	wsStr := wsPtr.String()

	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name   string
		scopes []*string
		want   bool
	}{
		{
			name:   "matching_scope",
			scopes: []*string{&wsStr},
			want:   true,
		},
		{
			name:   "no_match",
			scopes: []*string{strPtr(mustParseResourceID("sub-123", "my-rg", "services-westus3").String())},
			want:   false,
		},
		{
			name:   "nil_scope_skipped",
			scopes: []*string{nil, &wsStr},
			want:   true,
		},
		{
			name:   "empty_scopes",
			scopes: []*string{},
			want:   false,
		},
		{
			name:   "nil_scopes",
			scopes: nil,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scopeContainsWorkspace(tt.scopes, *wsPtr)
			if got != tt.want {
				t.Errorf("scopeContainsWorkspace() = %v, want %v", got, tt.want)
			}
		})
	}
}
