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

package arm

import (
	"encoding/json"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

func mustParseTestID(t *testing.T, s string) *azcorearm.ResourceID {
	t.Helper()
	id, err := azcorearm.ParseResourceID(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return id
}

func TestCosmosMetadataPartitionKey(t *testing.T) {
	const idStr = "/subscriptions/MyUpperCaseSub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"

	tests := []struct {
		name string
		// Each test mutates the metadata and asserts on Get/Set behaviour.
		runWith func(t *testing.T, m *CosmosMetadata)
	}{
		{
			name: "GetPartitionKey falls back to lowercased subscription ID when field is empty",
			runWith: func(t *testing.T, m *CosmosMetadata) {
				if got, want := m.GetPartitionKey(), "myuppercasesub"; got != want {
					t.Errorf("GetPartitionKey() = %q, want %q (lowercased SubscriptionID)", got, want)
				}
			},
		},
		{
			name: "SetPartitionKey lowercases on store",
			runWith: func(t *testing.T, m *CosmosMetadata) {
				m.SetPartitionKey("MGMT-Cluster-1")
				if got, want := m.PartitionKey, "mgmt-cluster-1"; got != want {
					t.Errorf("PartitionKey field = %q, want %q (lowercased on Set)", got, want)
				}
			},
		},
		{
			name: "GetPartitionKey returns the stored field when set, lowercased",
			runWith: func(t *testing.T, m *CosmosMetadata) {
				m.SetPartitionKey("MGMT-Cluster-1")
				if got, want := m.GetPartitionKey(), "mgmt-cluster-1"; got != want {
					t.Errorf("GetPartitionKey() = %q, want %q", got, want)
				}
			},
		},
		{
			name: "GetPartitionKey lowercases even if the field was set directly with mixed case",
			runWith: func(t *testing.T, m *CosmosMetadata) {
				m.PartitionKey = "MGMT-Cluster-1" // bypass setter
				if got, want := m.GetPartitionKey(), "mgmt-cluster-1"; got != want {
					t.Errorf("GetPartitionKey() = %q, want %q (lowercased on Get)", got, want)
				}
			},
		},
		{
			name: "SetPartitionKey then GetPartitionKey is idempotent",
			runWith: func(t *testing.T, m *CosmosMetadata) {
				m.SetPartitionKey("foo")
				m.SetPartitionKey(m.GetPartitionKey())
				if got := m.PartitionKey; got != "foo" {
					t.Errorf("idempotent set yielded %q", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &CosmosMetadata{
				ResourceID: mustParseTestID(t, idStr),
			}
			tc.runWith(t, m)
		})
	}
}

func TestCosmosMetadataPartitionKey_NilResourceID(t *testing.T) {
	m := &CosmosMetadata{}
	if got := m.GetPartitionKey(); got != "" {
		t.Errorf("GetPartitionKey() with nil ResourceID and unset PartitionKey = %q, want empty", got)
	}
	m.SetPartitionKey("X")
	if got := m.GetPartitionKey(); got != "x" {
		t.Errorf("GetPartitionKey() after Set with nil ResourceID = %q, want %q", got, "x")
	}
}

func TestCosmosMetadataJSONRoundTrip_PartitionKey(t *testing.T) {
	m := &CosmosMetadata{
		ResourceID: mustParseTestID(t,
			"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"),
	}
	m.SetPartitionKey("MGMT-1")

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Should serialize the lowercased value under "partitionKey".
	if want := `"partitionKey":"mgmt-1"`; !contains(string(data), want) {
		t.Errorf("marshalled JSON did not contain %q\n  got: %s", want, data)
	}

	var got CosmosMetadata
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GetPartitionKey() != "mgmt-1" {
		t.Errorf("after round-trip, GetPartitionKey() = %q, want %q", got.GetPartitionKey(), "mgmt-1")
	}
}

func TestCosmosMetadataJSONRoundTrip_OmitsEmptyPartitionKey(t *testing.T) {
	// Older documents on disk don't have partitionKey in cosmosMetadata. Round-tripping
	// without setting the field must not introduce one (omitempty), and GetPartitionKey
	// must still fall back to the subscription ID.
	m := &CosmosMetadata{
		ResourceID: mustParseTestID(t,
			"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/c"),
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if contains(string(data), "partitionKey") {
		t.Errorf("expected omitempty to drop partitionKey from JSON, got: %s", data)
	}
	if got, want := m.GetPartitionKey(), "sub"; got != want {
		t.Errorf("GetPartitionKey() with unset field = %q, want %q (subscription ID fallback)", got, want)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
