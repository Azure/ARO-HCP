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

package metadataapi

import (
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
)

const (
	testClusterResourceID  = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster"
	testNodePoolResourceID = testClusterResourceID + "/nodePools/np"
	testResourceGroupID    = "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg"
)

func TestClusterNameFromResourceID(t *testing.T) {
	wantCluster := Must(azcorearm.ParseResourceID(testClusterResourceID)).String()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"cluster returns its own resource ID", testClusterResourceID, wantCluster},
		{"node pool walks up to the cluster", testNodePoolResourceID, wantCluster},
		{"resource group has no HCP cluster ancestor", testResourceGroupID, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := Must(azcorearm.ParseResourceID(tt.input))
			if got := ClusterNameFromResourceID(id); got != tt.want {
				t.Errorf("ClusterNameFromResourceID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	if got := ClusterNameFromResourceID(nil); got != "" {
		t.Errorf("ClusterNameFromResourceID(nil) = %q, want empty", got)
	}
}

func TestObjectMetadataForResourceID_FillsClusterResourceID(t *testing.T) {
	clusterID := Must(azcorearm.ParseResourceID(testClusterResourceID))
	nodePoolID := Must(azcorearm.ParseResourceID(testNodePoolResourceID))
	resourceGroupID := Must(azcorearm.ParseResourceID(testResourceGroupID))

	if md := ObjectMetadataForResourceID("resources", clusterID); md.ClusterResourceID != clusterID.String() {
		t.Errorf("cluster: ClusterResourceID = %q, want %q", md.ClusterResourceID, clusterID.String())
	}
	if md := ObjectMetadataForResourceID("resources", nodePoolID); md.ClusterResourceID != clusterID.String() {
		t.Errorf("node pool: ClusterResourceID = %q, want %q (the enclosing cluster)", md.ClusterResourceID, clusterID.String())
	}
	if md := ObjectMetadataForResourceID("resources", resourceGroupID); md.ClusterResourceID != "" {
		t.Errorf("resource group: ClusterResourceID = %q, want empty", md.ClusterResourceID)
	}
	if md := ObjectMetadataForResourceID("resources", nil); md.ClusterResourceID != "" {
		t.Errorf("nil: ClusterResourceID = %q, want empty", md.ClusterResourceID)
	}
}

func TestResourceTypeEquality(t *testing.T) {
	clusterType := Must(azcorearm.ParseResourceID(testClusterResourceID)).ResourceType

	if !ResourceTypeEqual(clusterType, clusterType) {
		t.Error("ResourceTypeEqual should be true for identical types")
	}
	if !ResourceTypeStringEqual("microsoft.redhatopenshift/hcpopenshiftclusters", clusterType) {
		t.Error("ResourceTypeStringEqual should match case-insensitively")
	}
	if ResourceTypeStringEqual("Microsoft.RedHatOpenShift/nodePools", clusterType) {
		t.Error("ResourceTypeStringEqual should not match a different type")
	}
}
