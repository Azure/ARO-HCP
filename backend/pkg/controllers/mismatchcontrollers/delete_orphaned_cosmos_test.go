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

// TestSynchronizeSubscription_OrphanedDesires verifies the kube-applier sweep across
// the new per-management-cluster container model: each MC has its own MockKubeApplierDBClient
// registered in a MockKubeApplierDBClients (plural). The orphan controller iterates the
// registry, opens an UntypedCRUD against each MC's container, and deletes any *Desire whose
// parent cluster/nodepool is gone — using DeleteByCosmosID with the partitionKey from the
// listed row, never relying on the resourceID encoding a partition key.
//
// Coverage:
//   - Cluster-scoped desire under a live cluster (mc-a) — kept.
//   - Nodepool-scoped desire under a live nodepool (mc-a) — kept.
//   - Cluster-scoped desire under a missing cluster (mc-b, a different MC's container) — deleted.
//   - Nodepool-scoped desire under a missing nodepool (mc-a) — deleted.
func TestSynchronizeSubscription_OrphanedDesires(t *testing.T) {
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

	const (
		subscriptionID      = "a433a095-1277-44f1-8453-8d61a4d848c2"
		resourceGroupName   = "rg"
		liveClusterName     = "live-cluster"
		missingClusterName  = "missing-cluster"
		liveNodePoolName    = "live-np"
		missingNodePoolName = "missing-np"
		mgmtClusterAName    = "mc-a"
		mgmtClusterBName    = "mc-b"
	)

	mgmtClusterAResourceID := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/test/managementclusters/" + mgmtClusterAName))
	mgmtClusterBResourceID := api.Must(azcorearm.ParseResourceID("/providers/microsoft.redhatopenshift/stamps/test/managementclusters/" + mgmtClusterBName))

	// --- resources container: subscription + live cluster + live nodepool
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
		CosmosMetadata: arm.CosmosMetadata{ResourceID: liveNodePoolResourceID},
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

	// --- kube-applier containers: one per management cluster.
	// mc-a: desire-under-live-cluster (kept) + desire-under-live-nodepool (kept) + desire-under-missing-nodepool (deleted)
	// mc-b: desire-under-missing-cluster (deleted)
	desireUnderLiveCluster := newApplyDesire(t, mgmtClusterAResourceID,
		kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, "live-cluster-desire"))
	desireUnderLiveNodePool := newApplyDesire(t, mgmtClusterAResourceID,
		kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, liveNodePoolName, "live-np-desire"))
	desireUnderMissingNodePool := newApplyDesire(t, mgmtClusterAResourceID,
		kubeapplier.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, liveClusterName, missingNodePoolName, "missing-np-desire"))
	desireUnderMissingCluster := newApplyDesire(t, mgmtClusterBResourceID,
		kubeapplier.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroupName, missingClusterName, "missing-cluster-desire"))

	mcAClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{
		desireUnderLiveCluster, desireUnderLiveNodePool, desireUnderMissingNodePool,
	})
	require.NoError(t, err)
	mcBClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, []any{desireUnderMissingCluster})
	require.NoError(t, err)

	kubeApplierClients := databasetesting.NewMockKubeApplierDBClients()
	kubeApplierClients.Register(mgmtClusterAResourceID, mcAClient)
	kubeApplierClients.Register(mgmtClusterBResourceID, mcBClient)

	c := &deleteOrphanedCosmosResources{
		name:                 "DeleteOrphanedCosmosResources",
		resourcesDBClient:    resourcesClient,
		kubeApplierDBClients: kubeApplierClients,
	}

	require.NoError(t, c.synchronizeSubscription(ctx, subscriptionID))

	// Desires whose parent still exists survive — in their respective containers.
	assertDesirePresent(t, mcAClient, desireUnderLiveCluster.GetResourceID(),
		"desire under live cluster must survive")
	assertDesirePresent(t, mcAClient, desireUnderLiveNodePool.GetResourceID(),
		"desire under live nodepool must survive")

	// Desires whose parent is gone are deleted by cosmosID from their respective containers.
	assertDesireAbsent(t, mcAClient, desireUnderMissingNodePool.GetResourceID(),
		"desire under missing nodepool (mc-a) must be deleted")
	assertDesireAbsent(t, mcBClient, desireUnderMissingCluster.GetResourceID(),
		"desire under missing cluster (mc-b) must be deleted")
}

func newApplyDesire(t *testing.T, managementCluster *azcorearm.ResourceID, resourceIDString string) *kubeapplier.ApplyDesire {
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
