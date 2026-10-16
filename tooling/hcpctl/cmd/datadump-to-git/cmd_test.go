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

import "testing"

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
