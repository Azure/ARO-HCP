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

package mismatchcontrollers

import (
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// TestSynchronizeSubscription_OrphanedDesires verifies the kube-applier sweep:
// *Desires whose parent cluster or nodepool is gone are deleted by cosmosID; those
// whose parent is still present in the resources container are left alone.
//
// The kube-applier container is partitioned by management-cluster name, so deletions
// happen via DeleteByCosmosID using the partitionKey from each listed row. We exercise
// both cluster-scoped and nodepool-scoped *Desires across two management clusters.
func TestSynchronizeSubscription_OrphanedDesires(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const (
		subscriptionID    = "a433a095-1277-44f1-8453-8d61a4d848c2"
		resourceGroupName = "rg"
		liveClusterName   = "live-cluster"
		missingClusterName = "missing-cluster"
		liveNodePoolName  = "live-np"
		missingNodePoolName = "missing-np"
		mgmtClusterA      = "mc-a"
		mgmtClusterB      = "mc-b"
	)

	// --- resources container: a cluster + nodepool that DO exist, plus a subscription
	subscriptionResourceID := api.Must(arm.ToSubscriptionResourceID(subscriptionID))
	subscription := &arm.Subscription{
		CosmosMetadata: api.CosmosMetadata{ResourceID: subscriptionResourceID},
		ResourceID:     subscriptionResourceID,
		State:          arm.SubscriptionStateRegistered,
	}

	liveClusterResourceID := api.Must(api.ToClusterResourceID(subscriptionID, resourceGroupName, liveClusterName))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   liveClusterResourceID,
				Name: liveClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
	}

	liveNodePoolResourceID := api.Must(api.ToNodePoolResourceID(subscriptionID, resourceGroupName, liveClusterName, liveNodePoolName))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   liveNodePoolResourceID,
				Name: liveNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}

	resourcesClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{subscription, cluster, nodePool})
	require.NoError(t, err)

	// --- kube-applier container: four ApplyDesires
	desireUnderLiveCluster := newApplyDesire(t, mgmtClusterA,
		kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, "live-cluster-desire"))
	desireUnderLiveNodePool := newApplyDesire(t, mgmtClusterA,
		kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, liveNodePoolName, "live-np-desire"))
	desireUnderMissingCluster := newApplyDesire(t, mgmtClusterB,
		kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, missingClusterName, "missing-cluster-desire"))
	desireUnderMissingNodePool := newApplyDesire(t, mgmtClusterA,
		kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, missingNodePoolName, "missing-np-desire"))

	kubeApplierClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		desireUnderLiveCluster,
		desireUnderLiveNodePool,
		desireUnderMissingCluster,
		desireUnderMissingNodePool,
	})
	require.NoError(t, err)

	c := &deleteOrphanedCosmosResources{
		name:                "DeleteOrphanedCosmosResources",
		resourcesDBClient:   resourcesClient,
		kubeApplierDBClient: kubeApplierClient,
	}

	require.NoError(t, c.synchronizeSubscription(ctx, subscriptionID))

	// Desires whose parent still exists survive.
	assertDesirePresent(t, kubeApplierClient, desireUnderLiveCluster.GetResourceID(),
		"desire under live cluster must survive")
	assertDesirePresent(t, kubeApplierClient, desireUnderLiveNodePool.GetResourceID(),
		"desire under live nodepool must survive")

	// Desires whose parent is gone are deleted by cosmosID.
	assertDesireAbsent(t, kubeApplierClient, desireUnderMissingCluster.GetResourceID(),
		"desire under missing cluster must be deleted")
	assertDesireAbsent(t, kubeApplierClient, desireUnderMissingNodePool.GetResourceID(),
		"desire under missing nodepool must be deleted")
}

func newApplyDesire(t *testing.T, managementCluster, resourceIDString string) *kubeapplier.ApplyDesire {
	t.Helper()
	rid := api.Must(azcorearm.ParseResourceID(resourceIDString))
	return &kubeapplier.ApplyDesire{
		CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
		Spec: kubeapplier.ApplyDesireSpec{
			ManagementCluster: managementCluster,
			KubeContent:       &runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"x","namespace":"default"}}`)},
		},
	}
}

func assertDesirePresent(t *testing.T, client *databasetesting.MockKubeApplierDBClient, resourceID *azcorearm.ResourceID, msg string) {
	t.Helper()
	cosmosID := api.Must(arm.ResourceIDToCosmosID(resourceID))
	_, present := client.GetDocument(cosmosID)
	assert.True(t, present, "%s (resourceID %s, cosmosID %s)", msg, strings.ToLower(resourceID.String()), cosmosID)
}

func assertDesireAbsent(t *testing.T, client *databasetesting.MockKubeApplierDBClient, resourceID *azcorearm.ResourceID, msg string) {
	t.Helper()
	cosmosID := api.Must(arm.ResourceIDToCosmosID(resourceID))
	_, present := client.GetDocument(cosmosID)
	assert.False(t, present, "%s (resourceID %s, cosmosID %s)", msg, strings.ToLower(resourceID.String()), cosmosID)
}
