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

package ocadminspect

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/kusto"
)

func TestClusterNameFromNameOrResourceID(t *testing.T) {
	tests := map[string]string{
		"cluster-candidate-4-20-xz2crd": "cluster-candidate-4-20-xz2crd",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster": "my-cluster",
		"": "",
	}
	for input, want := range tests {
		if got := clusterNameFromNameOrResourceID(input); got != want {
			t.Errorf("clusterNameFromNameOrResourceID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsManagementCluster(t *testing.T) {
	tests := map[string]bool{
		"aro-hcp-mgmt-1": true,
		"hcpmgmtuks1":    true,
		"aro-hcp-svc-1":  false,
		"svc-cluster":    false,
	}
	for name, want := range tests {
		if got := IsManagementCluster(name); got != want {
			t.Errorf("IsManagementCluster(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestPairNamespaces(t *testing.T) {
	// The namespaces present on the cluster: a hosted-cluster namespace, its
	// control-plane namespace (hosted + "-<name>"), and an unrelated cluster's pair.
	clusterNamespaces := []string{
		"kube-system",
		"kube-system-audit", // shares a prefix with kube-system but is not an ocm- namespace
		"ocm-arohcpci01-2sicoll",
		"ocm-arohcpci01-2sicoll-u0y8w2y6",
		"ocm-arohcpci01-otherdef",
		"ocm-arohcpci01-otherdef-x9",
	}
	tests := []struct {
		name      string
		requested []string
		want      []string
	}{
		{
			name:      "hosted pulls in control plane",
			requested: []string{"ocm-arohcpci01-2sicoll"},
			want:      []string{"ocm-arohcpci01-2sicoll", "ocm-arohcpci01-2sicoll-u0y8w2y6"},
		},
		{
			name:      "control plane pulls in hosted",
			requested: []string{"ocm-arohcpci01-2sicoll-u0y8w2y6"},
			want:      []string{"ocm-arohcpci01-2sicoll", "ocm-arohcpci01-2sicoll-u0y8w2y6"},
		},
		{
			// kube-system must not pair with kube-system-audit: only ocm- namespaces pair.
			name:      "non-ocm namespace with shared prefix is not paired",
			requested: []string{"kube-system"},
			want:      []string{"kube-system"},
		},
		{
			name:      "does not cross-pair different clusters",
			requested: []string{"ocm-arohcpci01-2sicoll"},
			want:      []string{"ocm-arohcpci01-2sicoll", "ocm-arohcpci01-2sicoll-u0y8w2y6"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pairNamespaces(tc.requested, clusterNamespaces)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("pairNamespaces(%v) = %v, want %v", tc.requested, got, tc.want)
			}
		})
	}
}

// TestRenderedResourceQuery verifies the resource-snapshot template renders KQL
// that scopes by cluster + namespace + time and excludes actually-deleted objects.
func TestRenderedResourceQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectResources")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}
	baseOptions := kusto.NewQueryOptions()
	data := kusto.NewTemplateDataFromOptions(baseOptions,
		kusto.WithClusterName("aro-hcp-mgmt-1"),
		kusto.WithNamespace("kube-system"),
	)
	queries, err := factory.Build(*def, data)
	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}
	rendered := queries[0].GetQuery().String()

	for _, want := range []string{
		"kubernetesResourceSnapshots",
		"cluster == 'aro-hcp-mgmt-1'",
		"namespace == 'kube-system'",
		"event != 'Delete'",
		"deletionTimestamp",
		"deletionGracePeriodSeconds",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered resource query missing %q:\n%s", want, rendered)
		}
	}
}

// TestRenderedResourceQuery_WholeCluster verifies that leaving Namespace unset
// (no WithNamespace call — the whole-cluster export path) drops the namespace
// filter entirely, rather than rendering an empty/broken clause, while every
// other clause (collapse, delete/grace-period filtering) is untouched.
func TestRenderedResourceQuery_WholeCluster(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectResources")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}
	data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(), kusto.WithClusterName("aro-hcp-mgmt-1"))
	queries, err := factory.Build(*def, data)
	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}
	rendered := queries[0].GetQuery().String()

	if strings.Contains(rendered, "namespace ==") {
		t.Errorf("whole-cluster rendered resource query should have no namespace filter:\n%s", rendered)
	}
	for _, want := range []string{"kubernetesResourceSnapshots", "cluster == 'aro-hcp-mgmt-1'", "event != 'Delete'", "deletionGracePeriodSeconds"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("whole-cluster rendered resource query missing %q:\n%s", want, rendered)
		}
	}
}

// TestRenderedResourceHistoryQuery verifies the uncollapsed changelog query
// (feeding watch-index) carries the event column and ordering, and does not
// collapse to "state as of" or drop deleted objects the way
// ocAdmInspectResources does.
func TestRenderedResourceHistoryQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectResourceHistory")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}
	data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(), kusto.WithClusterName("aro-hcp-mgmt-1"))
	queries, err := factory.Build(*def, data)
	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}
	rendered := queries[0].GetQuery().String()

	for _, notWant := range []string{"arg_max", "event != 'Delete'", "deletionTimestamp", "namespace =="} {
		if strings.Contains(rendered, notWant) {
			t.Errorf("rendered resource history query should not contain %q:\n%s", notWant, rendered)
		}
	}
	for _, want := range []string{"kubernetesResourceSnapshots", "cluster == 'aro-hcp-mgmt-1'", "project timestamp, event,", "order by timestamp asc"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered resource history query missing %q:\n%s", want, rendered)
		}
	}
}

// TestRenderedEventsQuery verifies the events template projects eventNamespace
// (needed to route each event to the right archive path in whole-cluster
// mode) both with and without a namespace filter.
func TestRenderedEventsQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectEvents")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}

	t.Run("namespaced", func(t *testing.T) {
		data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(),
			kusto.WithClusterName("aro-hcp-mgmt-1"),
			kusto.WithNamespace("kube-system"),
		)
		queries, err := factory.Build(*def, data)
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		rendered := queries[0].GetQuery().String()
		for _, want := range []string{"eventNamespace == 'kube-system'", "eventNamespace,", "cluster == 'aro-hcp-mgmt-1'"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("rendered events query missing %q:\n%s", want, rendered)
			}
		}
	})

	t.Run("whole cluster", func(t *testing.T) {
		data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(), kusto.WithClusterName("aro-hcp-mgmt-1"))
		queries, err := factory.Build(*def, data)
		if err != nil {
			t.Fatalf("failed to build query: %v", err)
		}
		rendered := queries[0].GetQuery().String()
		if strings.Contains(rendered, "eventNamespace ==") {
			t.Errorf("whole-cluster rendered events query should have no namespace filter:\n%s", rendered)
		}
		if !strings.Contains(rendered, "eventNamespace,") {
			t.Errorf("whole-cluster rendered events query should still project eventNamespace:\n%s", rendered)
		}
	})
}

func TestRenderedContainerLogsQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectContainerLogs")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}
	data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(),
		kusto.WithClusterName("aro-hcp-mgmt-1"),
		kusto.WithNamespace("ocm-stg-abc"),
	)
	queries, err := factory.Build(*def, data)
	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}
	rendered := queries[0].GetQuery().String()
	for _, want := range []string{"containerLogs", "namespace_name == 'ocm-stg-abc'", "cluster == 'aro-hcp-mgmt-1'"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered container logs query missing %q:\n%s", want, rendered)
		}
	}
}
