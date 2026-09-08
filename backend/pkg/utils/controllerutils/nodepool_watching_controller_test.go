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

package controllerutils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
)

type mockNodePoolSyncer struct {
	syncOnceFunc func(ctx context.Context, key HCPNodePoolKey) error
}

func (m *mockNodePoolSyncer) SyncOnce(ctx context.Context, key HCPNodePoolKey) error {
	if m.syncOnceFunc != nil {
		return m.syncOnceFunc(ctx, key)
	}
	return nil
}

func newFakeNodePoolLister(subscriptionID, resourceGroup, clusterName, nodePoolName string) corelisters.NodePoolLister {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName +
			"/nodePools/" + nodePoolName,
	))
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	err := indexer.Add(&coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID: resourceID,
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.NewResource(resourceID),
		},
	})
	if err != nil {
		panic(err)
	}
	return corelisters.NewNodePoolLister(indexer)
}

// TestNodePoolWatchingControllerApplyDesireEnqueue verifies the
// node-pool-scoped (maxDepth 1) ApplyDesire wiring added for the node-pool
// Degraded aggregator: a node-pool-scoped ApplyDesire event enqueues its parent
// node pool, while a cluster-scoped ApplyDesire (which lives above the node
// pool) does not.
func TestNodePoolWatchingControllerApplyDesireEnqueue(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"
	nodePoolName := "np"

	syncedKeys := make(chan HCPNodePoolKey, 4)
	mockSyncer := &mockNodePoolSyncer{
		syncOnceFunc: func(ctx context.Context, key HCPNodePoolKey) error {
			select {
			case syncedKeys <- key:
			default:
			}
			return nil
		},
	}

	inner := &nodePoolWatchingController{
		name:              "test-controller",
		resourcesDBClient: corecosmosstoragetesting.NewMockResourcesDBClient(),
		syncer:            mockSyncer,
		nodePoolLister:    newFakeNodePoolLister(subscriptionID, resourceGroup, clusterName, nodePoolName),
	}
	gwc := newGenericWatchingController("test-controller", coreapi.NodePoolResourceType, inner)

	notifier := &capturingNotifier{}
	// Mirror the production ApplyDesire wiring: node-pool-scoped only (maxDepth 1).
	require.NoError(t, gwc.QueueForInformersWithMaxDepth(time.Minute, 1, notifier))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		gwc.Run(ctx, 1)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// A node-pool-scoped ApplyDesire sits one hop below the node pool, so
	// maxDepth 1 reaches the node pool and enqueues it.
	nodePoolScopedID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroup, clusterName, nodePoolName, "cfg")))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: nodePoolScopedID})

	select {
	case key := <-syncedKeys:
		require.Equal(t, subscriptionID, key.SubscriptionID)
		require.Equal(t, resourceGroup, key.ResourceGroupName)
		require.Equal(t, clusterName, key.HCPClusterName)
		require.Equal(t, nodePoolName, key.HCPNodePoolName)
	case <-time.After(5 * time.Second):
		t.Fatal("a node-pool-scoped ApplyDesire event should have enqueued the node pool")
	}

	// A cluster-scoped ApplyDesire lives above the node pool, so a maxDepth-1
	// walk from the desire never reaches a node pool -> must NOT enqueue.
	clusterScopedID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroup, clusterName, "cfg")))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: clusterScopedID})

	select {
	case key := <-syncedKeys:
		t.Fatalf("cluster-scoped ApplyDesire must not enqueue a node pool, but synced %+v", key)
	case <-time.After(500 * time.Millisecond):
		// expected: no enqueue for cluster-scoped desires
	}
}
