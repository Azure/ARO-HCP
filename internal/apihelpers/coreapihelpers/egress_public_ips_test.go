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

package coreapihelpers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
)

func TestEgressPublicIPReferences(t *testing.T) {
	expected := []*azcorearm.ResourceID{
		metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/group/providers/Microsoft.Network/publicIPAddresses/cluster-0")),
		metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/group/providers/Microsoft.Network/publicIPAddresses/cluster-1")),
	}
	for _, testCase := range []struct {
		name          string
		confirmed     []*azcorearm.ResourceID
		wantConfirmed int
	}{
		{name: "unobserved"},
		{name: "partial", confirmed: expected[:1], wantConfirmed: 1},
		{name: "all", confirmed: expected, wantConfirmed: 2},
		{name: "duplicate is not membership", confirmed: []*azcorearm.ResourceID{expected[0], expected[0]}, wantConfirmed: 1},
		{name: "nil is not evidence", confirmed: []*azcorearm.ResourceID{nil}},
		{name: "unrelated", confirmed: []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(expected[0].String() + "-other"))}},
		{name: "normalized", confirmed: []*azcorearm.ResourceID{metadataapi.Must(azcorearm.ParseResourceID(strings.ToUpper(expected[0].String())))}, wantConfirmed: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			references := EgressPublicIPReferences(expected, testCase.confirmed)
			require.Len(t, references.AzureResources, testCase.wantConfirmed)
			require.Len(t, references.PendingAzureResources, len(expected)-testCase.wantConfirmed)
			if testCase.wantConfirmed > 0 {
				require.Equal(t, expected[0].String(), references.AzureResources[0].String())
			}
			resources := coreapi.AzureResources{EgressPublicIPs: references}
			encoded, err := json.Marshal(resources)
			require.NoError(t, err)
			var decoded coreapi.AzureResources
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			require.Equal(t, references, decoded.EgressPublicIPs)
			copy := resources.DeepCopy()
			if len(copy.EgressPublicIPs.PendingAzureResources) > 0 {
				copy.EgressPublicIPs.PendingAzureResources[0] = nil
				require.NotNil(t, resources.EgressPublicIPs.PendingAzureResources[0])
			}
			if len(copy.EgressPublicIPs.AzureResources) > 0 {
				copy.EgressPublicIPs.AzureResources[0] = nil
				require.NotNil(t, resources.EgressPublicIPs.AzureResources[0])
			}
		})
	}
}

func TestEgressPublicIPNamingContract(t *testing.T) {
	t.Parallel()
	names := []string{
		"abc123-0", "abc123-1", "abc123-2", "abc123-3", "abc123-4",
		"abc123-5", "abc123-6", "abc123-7", "abc123-8",
	}
	require.Equal(t, 9, egressPublicIPCount)
	for index, name := range names {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, name, egressPublicIPName("abc123", index))
		})
	}
}

func TestEgressPublicIPNameUsesInputVerbatim(t *testing.T) {
	t.Parallel()
	require.Equal(t, "DifferentCluster-0", egressPublicIPName("DifferentCluster", 0))
	require.Equal(t, "DifferentCluster-8", egressPublicIPName("DifferentCluster", 8))
}

func TestEgressPublicIPResourceIDs(t *testing.T) {
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123"))
	cluster := &coreapi.Cluster{}
	cluster.ID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/example"))
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "managed-rg"
	expected := []string{
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-0",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-1",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-2",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-3",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-4",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-5",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-6",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-7",
		"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/managed-rg/providers/Microsoft.Network/publicIPAddresses/abc123-8",
	}
	for _, name := range []string{"default", "LoadBalancer", "non-LB", "missing ARM ID", "missing cluster ID", "empty cluster ID", "missing group"} {
		t.Run(name, func(t *testing.T) {
			input := cluster.DeepCopy()
			switch name {
			case "LoadBalancer":
				input.CustomerProperties.Platform.OutboundType = metadataapi.OutboundTypeLoadBalancer
			case "non-LB":
				input.CustomerProperties.Platform.OutboundType = metadataapi.OutboundType("UserDefinedRouting")
			case "missing ARM ID":
				input.ID = nil
			case "missing cluster ID":
				input.ServiceProviderProperties.ClusterServiceID = nil
			case "empty cluster ID":
				input.ServiceProviderProperties.ClusterServiceID = new(metadataapi.InternalID)
			case "missing group":
				input.CustomerProperties.Platform.ManagedResourceGroup = ""
			}
			actual, err := EgressPublicIPResourceIDs(input)
			require.NoError(t, err)
			if name != "default" && name != "LoadBalancer" {
				require.Empty(t, actual)
				return
			}
			ids := make([]string, 0, len(actual))
			for _, resourceID := range actual {
				ids = append(ids, resourceID.String())
				require.Equal(t, "managed-rg", resourceID.ResourceGroupName)
			}
			require.Equal(t, expected, ids)
			repeated, err := EgressPublicIPResourceIDs(input)
			require.NoError(t, err)
			require.Equal(t, actual, repeated)
		})
	}
}

func TestEgressPublicIPResourceIDsUseNormalizedIdentity(t *testing.T) {
	cluster := &coreapi.Cluster{}
	cluster.ID = metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/OTHER/resourceGroups/customer-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/example"))
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/DifferentCluster"))
	require.Equal(t, "differentcluster", clusterID.ID())
	cluster.ServiceProviderProperties.ClusterServiceID = &clusterID
	cluster.CustomerProperties.Platform.ManagedResourceGroup = "OtherRG"
	actual, err := EgressPublicIPResourceIDs(cluster)
	require.NoError(t, err)
	require.Len(t, actual, 9)
	require.Equal(t, "/subscriptions/OTHER/resourceGroups/OtherRG/providers/Microsoft.Network/publicIPAddresses/differentcluster-0", actual[0].String())
	require.Equal(t, "/subscriptions/OTHER/resourceGroups/OtherRG/providers/Microsoft.Network/publicIPAddresses/differentcluster-8", actual[8].String())
}
