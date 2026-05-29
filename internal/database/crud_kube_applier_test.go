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

package database

import "testing"

// NewKubeApplierPartitionKey is exercised end-to-end by the mock CRUD round-trip
// tests in databasetesting/. azcosmos.PartitionKey exposes no public string
// accessor, so a direct unit test isn't worth the contortion.
var _ = NewKubeApplierPartitionKey

func TestClusterParentResourceID(t *testing.T) {
	tests := []struct {
		name              string
		subscriptionID    string
		resourceGroupName string
		clusterName       string
		want              string
		wantErr           bool
	}{
		{
			name:              "cluster-scoped",
			subscriptionID:    "00000000-0000-0000-0000-000000000001",
			resourceGroupName: "myRG",
			clusterName:       "myCluster",
			// azcorearm.ParseResourceID re-canonicalises some segment names
			// (e.g. "resourceGroups") while leaving user-supplied names alone.
			want: "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/myrg/" +
				"providers/microsoft.redhatopenshift/hcpopenshiftclusters/mycluster",
		},
		{
			name:              "missing subscription is rejected",
			resourceGroupName: "rg",
			clusterName:       "c",
			wantErr:           true,
		},
		{
			name:           "missing resource group is rejected",
			subscriptionID: "sub",
			clusterName:    "c",
			wantErr:        true,
		},
		{
			name:              "missing cluster is rejected",
			subscriptionID:    "sub",
			resourceGroupName: "rg",
			wantErr:           true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := clusterParentResourceID(tc.subscriptionID, tc.resourceGroupName, tc.clusterName)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id.String() != tc.want {
				t.Errorf("resourceID = %q\n         want %q", id.String(), tc.want)
			}
		})
	}
}

func TestNodePoolParentResourceID(t *testing.T) {
	id, err := nodePoolParentResourceID(
		"00000000-0000-0000-0000-000000000001", "myRG", "myCluster", "myNodePool",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/myrg/" +
		"providers/microsoft.redhatopenshift/hcpopenshiftclusters/mycluster/nodepools/mynodepool"
	if id.String() != want {
		t.Errorf("resourceID = %q\n         want %q", id.String(), want)
	}

	if _, err := nodePoolParentResourceID("sub", "rg", "c", ""); err == nil {
		t.Errorf("expected error for empty nodePoolName, got nil")
	}
}
