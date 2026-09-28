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

package frontend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/operation"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
)

func TestNewClusterAdmissionContextCreateReadsSubscriptionListers(t *testing.T) {
	const (
		subscriptionA = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		subscriptionB = "11111111-1111-1111-1111-111111111111"
	)

	clusterA1 := admissionTestCluster(t, subscriptionA, "rg-a", "cluster-a1")
	clusterA2 := admissionTestCluster(t, subscriptionA, "rg-a", "cluster-a2")
	clusterB := admissionTestCluster(t, subscriptionB, "rg-b", "cluster-b")
	nodePoolA := admissionTestNodePool(t, subscriptionA, "rg-a", "cluster-a1", "pool-a")
	nodePoolB := admissionTestNodePool(t, subscriptionB, "rg-b", "cluster-b", "pool-b")

	f := &Frontend{
		// A nil client panics if create admission falls back to a live list.
		resourcesDBClient: nil,
		clusterLister: &corelistertesting.SliceClusterLister{
			Clusters: []*coreapi.Cluster{clusterA1, clusterA2, clusterB},
		},
		nodePoolLister: &corelistertesting.SliceNodePoolLister{
			NodePools: []*coreapi.NodePool{nodePoolA, nodePoolB},
		},
	}

	original := admissionTestCluster(t, subscriptionA, "rg-a", "cluster-new")
	got, err := f.newClusterAdmissionContext(
		context.Background(),
		operation.Operation{Type: operation.Create},
		&coreapi.Subscription{},
		original,
		nil,
	)
	require.NoError(t, err)
	require.ElementsMatch(t, []*coreapi.Cluster{clusterA1, clusterA2}, got.SubscriptionClusters)
	require.ElementsMatch(t, []*coreapi.NodePool{nodePoolA}, got.SubscriptionNodePools)
}

func TestNewClusterAdmissionContextCreateRequiresListers(t *testing.T) {
	f := &Frontend{}
	_, err := f.newClusterAdmissionContext(
		context.Background(),
		operation.Operation{Type: operation.Create},
		&coreapi.Subscription{},
		admissionTestCluster(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "rg", "cluster"),
		nil,
	)
	require.Error(t, err)
}

func admissionTestCluster(t *testing.T, subscriptionID, resourceGroupName, clusterName string) *coreapi.Cluster {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	return &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: resourceID, Name: clusterName},
		},
	}
}

func admissionTestNodePool(t *testing.T, subscriptionID, resourceGroupName, clusterName, nodePoolName string) *coreapi.NodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName,
	))
	return &coreapi.NodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: resourceID, Name: nodePoolName},
		},
	}
}
