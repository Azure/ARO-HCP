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

package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/tooling/hcpctl/internal/tabular"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
)

type recordingKustoClient struct {
	queries []string
	table   *tabular.Table
}

func (c *recordingKustoClient) Query(_ context.Context, kql string) (*tabular.Table, error) {
	c.queries = append(c.queries, kql)
	return c.table, nil
}

func TestQueryLibraryTools(t *testing.T) {
	library, err := snapshot.LoadQueryLibrary()
	if err != nil {
		t.Fatalf("LoadQueryLibrary: %v", err)
	}
	client := &recordingKustoClient{table: &tabular.Table{Columns: []string{"cid"}, Rows: [][]string{{"cid-123"}}}}
	resultsDir := t.TempDir()
	session := snapshot.NewLibrarySession(library, snapshot.LibrarySessionConfig{
		Config: map[string]string{
			"ClusterURI":               "https://adx.example.kusto.windows.net",
			"ServiceDatabase":          "ServiceLogs",
			"HCPDatabase":              "HostedControlPlaneLogs",
			"MonitoringEventsDatabase": "MonitoringEvents",
		},
		Execute:    LibraryQueryExecutor(client),
		ResultsDir: resultsDir,
	})

	tools := map[string]ToolDefinition{}
	for _, tool := range NewQueryLibraryToolDefinitions(session) {
		var schema map[string]any
		if err := json.Unmarshal(tool.ParamSchema, &schema); err != nil {
			t.Fatalf("tool %s has an invalid schema: %v", tool.Name, err)
		}
		tools[tool.Name] = tool
	}
	call := func(name, params string) (string, error) {
		t.Helper()
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		return tool.Handler(context.Background(), json.RawMessage(params))
	}

	out, err := call("list_queries", `{"filter": "clustersService"}`)
	if err != nil || !strings.Contains(out, "clustersService/cid") || strings.Contains(out, "maestro/serverLogs") {
		t.Errorf("list_queries filter: %v\n%s", err, out)
	}
	if out, err := call("list_queries", ``); err != nil || !strings.Contains(out, "maestro/serverLogs") {
		t.Errorf("list_queries without params: %v\n%s", err, out)
	}

	if _, err := call("run_query", `{"name": "clustersService/maestroBundleAssociations"}`); err == nil || !strings.Contains(err.Error(), "clustersService/cid") {
		t.Errorf("run_query should name the query providing ClusterID: %v", err)
	}

	clusterID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c1"
	out, err = call("run_query", `{"name": "clustersService/cid", "params": {"ResourceID": "`+clusterID+`", "FullStartTime": "2025-01-02T00:00:00Z", "FullEndTime": "2025-01-03T00:00:00Z"}}`)
	if err != nil {
		t.Fatalf("run_query: %v", err)
	}
	for _, want := range []string{"cid-123", "ClusterID", "https://adx.example.kusto.windows.net", resultsDir} {
		if !strings.Contains(out, want) {
			t.Errorf("run_query output missing %q:\n%s", want, out)
		}
	}
	if len(client.queries) != 1 || strings.Contains(client.queries[0], "{{") || !strings.Contains(client.queries[0], clusterID) {
		t.Errorf("unexpected executed queries: %q", client.queries)
	}
	entries, err := os.ReadDir(resultsDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one result file: %v %v", entries, err)
	}

	out, err = call("describe_query", `{"name": "clustersService/maestroBundleAssociations"}`)
	if err != nil || !strings.Contains(out, "cid-123") || strings.Contains(out, "Not renderable") {
		t.Errorf("describe_query should render with the provided ClusterID: %v\n%s", err, out)
	}
	out, err = call("describe_param", `{"name": "ClusterID"}`)
	if err != nil || !strings.Contains(out, "cid-123") || !strings.Contains(out, "clustersService/cid") {
		t.Errorf("describe_param: %v\n%s", err, out)
	}
	if _, err := call("describe_query", `{"name": "nope/nope"}`); err == nil {
		t.Errorf("describe_query should reject unknown queries")
	}
}
