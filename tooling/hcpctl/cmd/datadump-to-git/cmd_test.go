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

package datadumptogit

import (
	"encoding/json"
	"testing"
)

func TestCSClientAndLegacySnapshots(t *testing.T) {
	const clusterID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"
	const csID = "/api/aro_hcp/v1alpha1/clusters/cs-cluster"
	for _, scope := range []string{"legacy", "cluster", "nodepool", "cluster list"} {
		t.Run(scope, func(t *testing.T) {
			entry := map[string]any{
				"clusterServiceID": csID, "nodePoolClusterServiceID": csID + "/node_pools/np",
				"resource_id": clusterID, "csCluster": map[string]any{"id": "cs-cluster"}, "csNodePool": map[string]any{"id": "np"},
			}
			if scope != "legacy" {
				entry["snapshotType"] = "clusterService"
			}
			if scope == "nodepool" {
				entry["resource_id"] = clusterID + "/nodePools/np"
				entry["hcp_cluster_name"] = clusterID
			}
			if scope == "cluster list" {
				delete(entry, "resource_id")
				entry["csCluster"] = map[string]any{"id": "cs-cluster", "azure": map[string]any{
					"subscription_id": "sub", "resource_group_name": "rg", "resource_name": "cluster",
				}}
			}
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			cluster, ok := parseCSClusterStateDump(string(data), "timestamp")
			if !ok || cluster.RelativePath != csClusterStatePath(clusterID, csID) {
				t.Fatalf("cluster snapshot placed incorrectly: %+v, accepted=%v", cluster, ok)
			}
			if scope == "cluster list" {
				return
			}
			nodePool, ok := parseCSNodePoolStateDump(string(data), "timestamp")
			if !ok || nodePool.RelativePath != csNodePoolStatePath(clusterID, csID+"/node_pools/np") {
				t.Fatalf("node pool snapshot placed incorrectly: %+v, accepted=%v", nodePool, ok)
			}
		})
	}
}

func TestParseCosmosResourceSnapshot(t *testing.T) {
	line := `{"content":{"_ts":1788199837,"properties":{"cosmosMetadata":{"instanceVersion":2}}},"cosmosContainer":"resources","resourceID":"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster"}`

	entry, ok := parseCosmosResourceSnapshot(line)
	if !ok {
		t.Fatal("expected Cosmos resource snapshot to parse")
	}
	if entry.Timestamp != "2026-08-31T18:10:37Z" {
		t.Errorf("unexpected timestamp: %q", entry.Timestamp)
	}
	if entry.ResourceID != "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster" {
		t.Errorf("unexpected resource ID: %q", entry.ResourceID)
	}
	if entry.Content != `{"_ts":1788199837,"properties":{"cosmosMetadata":{"instanceVersion":2}}}` {
		t.Errorf("unexpected content: %q", entry.Content)
	}
}

func TestParseCosmosResourceSnapshotBilling(t *testing.T) {
	line := `{"content":{"_ts":1788199837},"cosmosContainer":"billing","resourceID":"/subscriptions/sub"}`

	entry, ok := parseCosmosResourceSnapshot(line)
	if !ok {
		t.Fatal("expected billing Cosmos resource snapshot to parse")
	}
	if entry.ContainerPrefix != "billing" {
		t.Errorf("unexpected container prefix: %q", entry.ContainerPrefix)
	}
	if entry.RelativePath != "subscriptions/sub/billing.json" {
		t.Errorf("unexpected relative path: %q", entry.RelativePath)
	}
}

func TestParseCosmosResourceSnapshotOperationTimestamp(t *testing.T) {
	line := `{"content":{"lastTransitionTime":"2026-08-31T18:10:37.7488150Z","startTime":"2026-08-31T18:10:36Z"},"cosmosContainer":"resources","resourceID":"/subscriptions/sub/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/op"}`

	entry, ok := parseCosmosResourceSnapshot(line)
	if !ok {
		t.Fatal("expected operation snapshot to parse")
	}
	if entry.Timestamp != "2026-08-31T18:10:37.748815Z" {
		t.Errorf("unexpected timestamp: %q", entry.Timestamp)
	}
}

func TestParseCosmosResourceSnapshotRejectsLogEntry(t *testing.T) {
	if _, ok := parseCosmosResourceSnapshot(`{"time":"2026-08-31T18:10:37Z","msg":"dumping resourceID x"}`); ok {
		t.Fatal("expected backend log entry not to parse as a Cosmos resource snapshot")
	}
}
