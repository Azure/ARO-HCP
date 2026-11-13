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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Run against the same local Kusto emulator used by make verify-kql:
// KUSTO_ENDPOINT=http://localhost:8080 go test ./pkg/snapshot -run TestCSQueries
func TestCSQueries(t *testing.T) {
	endpoint := os.Getenv("KUSTO_ENDPOINT")
	if endpoint == "" {
		t.Skip("set KUSTO_ENDPOINT to run query compatibility tests against a local emulator")
	}
	database := fmt.Sprintf("csquerytests%d", time.Now().UnixNano())
	request := func(route, query string) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]string{"db": database, "csl": query})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+route, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		result, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Kusto returned %s: %s", resp.Status, result)
		}
		return result
	}
	request("/v1/rest/mgmt", fmt.Sprintf(`.create database %s persist (@"/kustodata/dbs/%s/md", @"/kustodata/dbs/%s/data")`, database, database, database))
	t.Cleanup(func() { request("/v1/rest/mgmt", ".drop database "+database) })
	const clusterID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
	for _, kind := range []string{"clusterState", "nodePoolState"} {
		for _, scope := range []string{"legacy", "cluster", "nodepool", "unscoped list"} {
			if kind == "nodePoolState" && scope == "unscoped list" {
				continue
			}
			t.Run(kind+"/"+scope, func(t *testing.T) {
				entry := map[string]any{"controller_name": "caller", "resource_group": "rg", "resource_name": "cluster"}
				if scope == "legacy" {
					entry["controller_name"] = "csstatedump"
					entry["hcp_nodepool_name"] = clusterID + "/nodePools/np"
				} else {
					entry["snapshotType"] = "clusterService"
					entry["hcp_cluster_name"] = strings.ToLower(clusterID)
				}
				if scope == "nodepool" {
					entry["resource_name"] = "np"
				}
				if scope == "unscoped list" {
					delete(entry, "resource_group")
					delete(entry, "resource_name")
					delete(entry, "hcp_cluster_name")
				}
				if kind == "clusterState" {
					entry["msg"] = "cluster-service state dump"
					entry["csCluster"] = map[string]any{"id": "expected", "azure": map[string]string{"resource_group_name": "rg", "resource_name": "cluster"}}
				} else {
					entry["msg"] = "cluster-service node pool state dump"
					entry["csNodePool"] = map[string]any{"id": "expected", "azure_node_pool": map[string]string{"resource_name": "np"}}
				}
				data := queryData{ResourceGroup: "rg", ClusterResourceName: "cluster", ResourceID: clusterID + "/nodePools/np",
					PhaseStartTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), PhaseEndTime: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
				query, err := renderQuery("queries/clustersService/"+kind+"/query.kql", data)
				if err != nil {
					t.Fatal(err)
				}
				_, filters, _ := strings.Cut(query, "\n")
				for _, matches := range []bool{true, false} {
					if !matches {
						entry["resource_group"] = "different-rg"
						if obj, ok := entry["csCluster"].(map[string]any); ok {
							obj["azure"].(map[string]string)["resource_group_name"] = "different-rg"
						}
					}
					payload, err := json.Marshal(entry)
					if err != nil {
						t.Fatal(err)
					}
					source := fmt.Sprintf("datatable(timestamp:datetime,container_name:string,log:dynamic)[datetime(2026-01-01T01:00:00Z),'aro-hcp-backend',dynamic(%s)]\n", payload)
					result := request("/v1/rest/query", source+filters)
					if bytes.Contains(result, []byte("expected")) != matches {
						t.Fatalf("matches=%v: unexpected query result: %s", matches, result)
					}
				}
			})
		}
	}
}
