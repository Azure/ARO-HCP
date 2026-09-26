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
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	kustoerrors "github.com/Azure/azure-kusto-go/azkustodata/errors"
	azkquery "github.com/Azure/azure-kusto-go/azkustodata/query"
	"github.com/Azure/azure-kusto-go/azkustodata/types"
	"github.com/Azure/azure-kusto-go/azkustodata/value"

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

// TestRenderedPodNodeNamesQuery verifies the pod-node-names template scopes by
// cluster + namespace + time and does not apply the current-state delete
// filtering that the resource-snapshot query uses (it must see every pod
// snapshot ever recorded in the window, not just pods still live at TimestampMax).
func TestRenderedPodNodeNamesQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectPodNodeNames")
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

	for _, want := range []string{
		"kubernetesResourceSnapshots",
		"cluster == 'aro-hcp-mgmt-1'",
		"namespace == 'ocm-stg-abc'",
		"objectKind == 'Pod'",
		"nodeName = tostring(object.spec.nodeName)",
		"distinct nodeName",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered pod-node-names query missing %q:\n%s", want, rendered)
		}
	}
	for _, notWant := range []string{"event != 'Delete'", "deletionTimestamp"} {
		if strings.Contains(rendered, notWant) {
			t.Errorf("rendered pod-node-names query should not do current-state filtering, but contains %q:\n%s", notWant, rendered)
		}
	}
}

// TestRenderedNodesQuery verifies the nodes template filters by a name list
// instead of a namespace, and applies the same current-state delete filtering
// as the resource-snapshot query.
func TestRenderedNodesQuery(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	def, err := factory.GetBuiltinQueryDefinition("ocAdmInspectNodes")
	if err != nil {
		t.Fatalf("failed to get query definition: %v", err)
	}
	data := kusto.NewTemplateDataFromOptions(kusto.NewQueryOptions(),
		kusto.WithClusterName("aro-hcp-mgmt-1"),
		kusto.WithNames([]string{"aks-nodepool1-vmss000000", "aks-nodepool1-vmss000001"}),
	)
	queries, err := factory.Build(*def, data)
	if err != nil {
		t.Fatalf("failed to build query: %v", err)
	}
	rendered := queries[0].GetQuery().String()

	for _, want := range []string{
		"kubernetesResourceSnapshots",
		"cluster == 'aro-hcp-mgmt-1'",
		"objectKind == 'Node'",
		"name in ('aks-nodepool1-vmss000000', 'aks-nodepool1-vmss000001')",
		"event != 'Delete'",
		"deletionTimestamp",
		"deletionGracePeriodSeconds",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered nodes query missing %q:\n%s", want, rendered)
		}
	}
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

// makeInspectRow builds a kusto.TaggedRow wrapping a real azkquery.Row, following
// the same construction mustgather's makeTestRow uses, so fakeInspectExecutor can
// hand the Inspector real rows without a live Kusto backend.
func makeInspectRow(t *testing.T, columnNames []string, vals value.Values) kusto.TaggedRow {
	t.Helper()
	columns := make([]azkquery.Column, len(columnNames))
	for i, name := range columnNames {
		typ := types.String
		if name == "object" {
			typ = types.Dynamic
		}
		columns[i] = azkquery.NewColumn(i, name, typ)
	}
	ds := azkquery.NewBaseDataset(context.Background(), kustoerrors.OpUnknown, "PrimaryResult")
	bt := azkquery.NewBaseTable(ds, 0, "test-id", "PrimaryResult", "PrimaryResult", columns)
	return kusto.TaggedRow{Row: azkquery.NewRow(bt, 0, vals), QueryName: "test"}
}

// namespaceNodeNames is one namespace's discoverPodNodeNames response.
type namespaceNodeNames struct {
	names []string
	err   error
}

// fakeInspectExecutor is a QueryExecutor that answers oc-adm-inspect's queries by
// name (kusto.Query.GetName()) instead of hitting a real Kusto backend: resources/
// events/container-log queries return no rows, pod-node-names queries are answered
// per namespace (found by matching the rendered `namespace == '<ns>'` clause), and
// every nodes query is recorded (rendered text) and answered with nodeRows.
type fakeInspectExecutor struct {
	t                *testing.T
	podNodeNames     map[string]namespaceNodeNames
	nodeRows         []kusto.TaggedRow
	nodesQueryCount  int
	renderedNodesQry []string
}

func (f *fakeInspectExecutor) ExecutePreconfiguredQuery(_ context.Context, query kusto.Query, outputChannel chan<- kusto.TaggedRow) (*kusto.QueryResult, error) {
	switch query.GetName() {
	case nodesQueryName:
		f.nodesQueryCount++
		f.renderedNodesQry = append(f.renderedNodesQry, query.GetQuery().String())
		for _, row := range f.nodeRows {
			outputChannel <- row
		}
		return &kusto.QueryResult{}, nil
	case podNodeNamesQueryName:
		rendered := query.GetQuery().String()
		for namespace, resp := range f.podNodeNames {
			if !strings.Contains(rendered, fmt.Sprintf("namespace == '%s'", namespace)) {
				continue
			}
			if resp.err != nil {
				return &kusto.QueryResult{}, resp.err
			}
			for _, name := range resp.names {
				outputChannel <- makeInspectRow(f.t, []string{"nodeName"}, value.Values{value.NewString(name)})
			}
			return &kusto.QueryResult{}, nil
		}
		f.t.Fatalf("no fake pod-node-names response configured for rendered query: %s", rendered)
		return nil, nil
	case resourcesQueryName, eventsQueryName, "ocAdmInspectContainerLogs", "ocAdmInspectHostedControlPlaneLogs":
		return &kusto.QueryResult{}, nil
	default:
		f.t.Fatalf("unexpected query name %q", query.GetName())
		return nil, nil
	}
}

// fakeInspectWriter records what InspectNamespaces writes, without touching disk.
type fakeInspectWriter struct {
	resourceNamespaces []string
	clusterScopedCalls [][]string
}

func (w *fakeInspectWriter) WriteResources(_ context.Context, namespace string, _ []Resource) error {
	w.resourceNamespaces = append(w.resourceNamespaces, namespace)
	return nil
}
func (w *fakeInspectWriter) WriteEvents(context.Context, string, []map[string]any) error { return nil }
func (w *fakeInspectWriter) WriteContainerLog(context.Context, string, string, string, []LogLine) error {
	return nil
}
func (w *fakeInspectWriter) WriteClusterScopedResources(_ context.Context, resources []Resource) error {
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Name
	}
	w.clusterScopedCalls = append(w.clusterScopedCalls, names)
	return nil
}
func (w *fakeInspectWriter) NamespaceOutputPath(string) string { return "" }

var _ Writer = (*fakeInspectWriter)(nil)

func nodeResourceRows(t *testing.T, names []string) []kusto.TaggedRow {
	t.Helper()
	rows := make([]kusto.TaggedRow, 0, len(names))
	for _, name := range names {
		object := fmt.Sprintf(`{"apiVersion":"v1","kind":"Node","metadata":{"name":%q}}`, name)
		rows = append(rows, makeInspectRow(t,
			[]string{"apiVersion", "objectKind", "namespace", "name", "object"},
			value.Values{
				value.NewString("v1"),
				value.NewString("Node"),
				value.NewString(""),
				value.NewString(name),
				value.NewDynamic([]byte(object)),
			}))
	}
	return rows
}

// TestInspectNamespaces_DedupesNodesAcrossNamespaces verifies that node discovery
// (a) is driven by the namespaces already passed to InspectNamespaces, (b) unions
// and dedupes node names across all of them, and (c) issues exactly one batched
// nodes query/write, even when one namespace has no pods on any node.
func TestInspectNamespaces_DedupesNodesAcrossNamespaces(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	exec := &fakeInspectExecutor{
		t: t,
		podNodeNames: map[string]namespaceNodeNames{
			"ns-a":     {names: []string{"node-a", "node-b"}},
			"ns-b":     {names: []string{"node-b", "node-c"}},
			"ns-empty": {names: nil},
		},
		nodeRows: nodeResourceRows(t, []string{"node-a", "node-b", "node-c"}),
	}
	writer := &fakeInspectWriter{}
	inspector := NewInspector(exec, factory, kusto.NewQueryOptions(), "aro-hcp-mgmt-1", writer)

	if err := inspector.InspectNamespaces(context.Background(), []string{"ns-a", "ns-b", "ns-empty"}); err != nil {
		t.Fatalf("InspectNamespaces: %v", err)
	}

	if exec.nodesQueryCount != 1 {
		t.Errorf("nodes query issued %d times, want exactly 1 (one batched call for all namespaces)", exec.nodesQueryCount)
	}
	if want := "name in ('node-a', 'node-b', 'node-c')"; !strings.Contains(exec.renderedNodesQry[0], want) {
		t.Errorf("rendered nodes query missing deduped/sorted name list %q:\n%s", want, exec.renderedNodesQry[0])
	}
	if len(writer.clusterScopedCalls) != 1 {
		t.Fatalf("WriteClusterScopedResources called %d times, want exactly 1", len(writer.clusterScopedCalls))
	}
	gotNames := writer.clusterScopedCalls[0]
	sort.Strings(gotNames)
	wantNames := []string{"node-a", "node-b", "node-c"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("WriteClusterScopedResources got names %v, want %v", gotNames, wantNames)
	}
	wantResourceNamespaces := []string{"ns-a", "ns-b", "ns-empty"}
	if !reflect.DeepEqual(writer.resourceNamespaces, wantResourceNamespaces) {
		t.Errorf("WriteResources namespaces = %v, want %v", writer.resourceNamespaces, wantResourceNamespaces)
	}
}

// TestInspectNamespaces_NodeCollectionSkippedWhenNoPodsFound verifies the
// zero-node guard: when no namespace has any pod-derived node name, the nodes
// query/write is never issued (an empty `name in ()` clause must never render).
func TestInspectNamespaces_NodeCollectionSkippedWhenNoPodsFound(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	exec := &fakeInspectExecutor{
		t:            t,
		podNodeNames: map[string]namespaceNodeNames{"ns-empty": {names: nil}},
	}
	writer := &fakeInspectWriter{}
	inspector := NewInspector(exec, factory, kusto.NewQueryOptions(), "aro-hcp-mgmt-1", writer)

	if err := inspector.InspectNamespaces(context.Background(), []string{"ns-empty"}); err != nil {
		t.Fatalf("InspectNamespaces: %v", err)
	}
	if exec.nodesQueryCount != 0 {
		t.Errorf("nodes query issued %d times, want 0 when no pod-derived node names were found", exec.nodesQueryCount)
	}
	if len(writer.clusterScopedCalls) != 0 {
		t.Errorf("WriteClusterScopedResources called %d times, want 0", len(writer.clusterScopedCalls))
	}
}

// TestInspectNamespaces_NamespaceErrorDoesNotAbortOthers verifies that a
// discoverPodNodeNames failure in one namespace does not stop other namespaces'
// resources/events/logs from being written, nor prevent node collection for the
// node names that were successfully discovered elsewhere.
func TestInspectNamespaces_NamespaceErrorDoesNotAbortOthers(t *testing.T) {
	factory, err := kusto.NewQueryFactory()
	if err != nil {
		t.Fatalf("failed to build query factory: %v", err)
	}
	exec := &fakeInspectExecutor{
		t: t,
		podNodeNames: map[string]namespaceNodeNames{
			"ns-a": {names: []string{"node-a"}},
			"ns-b": {err: fmt.Errorf("simulated Kusto failure")},
		},
		nodeRows: nodeResourceRows(t, []string{"node-a"}),
	}
	writer := &fakeInspectWriter{}
	inspector := NewInspector(exec, factory, kusto.NewQueryOptions(), "aro-hcp-mgmt-1", writer)

	err = inspector.InspectNamespaces(context.Background(), []string{"ns-a", "ns-b"})
	if err == nil || !strings.Contains(err.Error(), "ns-b") {
		t.Fatalf("InspectNamespaces error = %v, want an error mentioning the failing namespace ns-b", err)
	}

	wantResourceNamespaces := []string{"ns-a", "ns-b"}
	if !reflect.DeepEqual(writer.resourceNamespaces, wantResourceNamespaces) {
		t.Errorf("WriteResources namespaces = %v, want %v (ns-b's node-discovery failure must not skip its own/other namespaces' resource writes)", writer.resourceNamespaces, wantResourceNamespaces)
	}
	if len(writer.clusterScopedCalls) != 1 || !reflect.DeepEqual(writer.clusterScopedCalls[0], []string{"node-a"}) {
		t.Errorf("WriteClusterScopedResources calls = %v, want a single call with [node-a] (only the successfully discovered namespace's nodes)", writer.clusterScopedCalls)
	}
}
