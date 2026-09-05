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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"

	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type capturingNotifier struct {
	addFunc    func(any)
	updateFunc func(any, any)
}

func (n *capturingNotifier) AddEventHandlerWithOptions(handler cache.ResourceEventHandler, opts cache.HandlerOptions) (cache.ResourceEventHandlerRegistration, error) {
	f := handler.(cache.ResourceEventHandlerFuncs)
	n.addFunc = f.AddFunc
	n.updateFunc = f.UpdateFunc
	return nil, nil
}

type mockClusterSyncer struct {
	syncOnceFunc func(ctx context.Context, key HCPClusterKey) error
	cooldown     controllerutil.CooldownChecker
}

func newFakeClusterLister(subscriptionID, resourceGroup, clusterName string) corelisters.ClusterLister {
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + subscriptionID +
			"/resourceGroups/" + resourceGroup +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + clusterName,
	))
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	err := indexer.Add(&coreapi.HCPOpenShiftCluster{
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
	return corelisters.NewClusterLister(indexer)
}

func (m *mockClusterSyncer) SyncOnce(ctx context.Context, key HCPClusterKey) error {
	if m.syncOnceFunc != nil {
		return m.syncOnceFunc(ctx, key)
	}
	return nil
}

func (m *mockClusterSyncer) CooldownChecker() controllerutil.CooldownChecker {
	if m.cooldown != nil {
		return m.cooldown
	}
	return controllerutil.NewTimeBasedCooldownChecker(time.Minute)
}

func TestClusterWatchingControllerSyncHasLoggerContextValues(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	capturedCtxCh := make(chan context.Context, 1)
	mockSyncer := &mockClusterSyncer{
		syncOnceFunc: func(ctx context.Context, key HCPClusterKey) error {
			select {
			case capturedCtxCh <- ctx:
			default:
			}
			return nil
		},
	}

	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()

	inner := &clusterWatchingController{
		name:              "test-controller",
		resourcesDBClient: mockResourcesDBClient,
		syncer:            mockSyncer,
		clusterLister:     newFakeClusterLister(subscriptionID, resourceGroup, clusterName),
	}
	gwc := newGenericWatchingController("test-controller", coreapi.ClusterResourceType, inner)

	notifier := &capturingNotifier{}
	require.NoError(t, gwc.QueueForInformers(time.Minute, notifier))

	clusterResourceID := metadataapi.Must(coreapi.ToClusterResourceID(subscriptionID, resourceGroup, clusterName))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: clusterResourceID})

	var logOutput strings.Builder
	logger := funcr.New(func(prefix, args string) {
		logOutput.WriteString(prefix)
		logOutput.WriteString(args)
	}, funcr.Options{})

	ctx, cancel := context.WithCancel(utils.ContextWithLogger(context.Background(), logger))

	done := make(chan struct{})
	go func() {
		defer close(done)
		gwc.Run(ctx, 1)
	}()

	var capturedCtx context.Context
	select {
	case capturedCtx = <-capturedCtxCh:
	case <-time.After(5 * time.Second):
		t.Fatal("syncer should have been called")
	}

	cancel()
	<-done

	capturedLogger := utils.LoggerFromContext(capturedCtx)
	capturedLogger.Info("test")

	output := logOutput.String()
	require.Contains(t, output, ` "subscription_id"="00000000-0000-0000-0000-000000000000" `, "logger should contain subscription_id")
	require.Contains(t, output, ` "resource_group"="test-rg" `, "logger should contain resource_group")
	require.Contains(t, output, ` "resource_name"="test-cluster" `, "logger should contain cluster name")
	require.Contains(t, output, `"hcp_cluster_name"="/subscriptions/00000000-0000-0000-0000-000000000000/resourcegroups/test-rg/providers/microsoft.redhatopenshift/hcpopenshiftclusters/test-cluster"`)

}

// TestClusterWatchingControllerApplyDesireEnqueue verifies the cluster-scoped
// (maxDepth 1) ApplyDesire wiring added for the Degraded aggregator: a
// cluster-scoped ApplyDesire event enqueues its parent cluster, while a
// node-pool-nested ApplyDesire (two hops from the cluster) does not.
func TestClusterWatchingControllerApplyDesireEnqueue(t *testing.T) {
	subscriptionID := "00000000-0000-0000-0000-000000000000"
	resourceGroup := "test-rg"
	clusterName := "test-cluster"

	syncedKeys := make(chan HCPClusterKey, 4)
	mockSyncer := &mockClusterSyncer{
		syncOnceFunc: func(ctx context.Context, key HCPClusterKey) error {
			select {
			case syncedKeys <- key:
			default:
			}
			return nil
		},
	}

	inner := &clusterWatchingController{
		name:              "test-controller",
		resourcesDBClient: corecosmosstoragetesting.NewMockResourcesDBClient(),
		syncer:            mockSyncer,
		clusterLister:     newFakeClusterLister(subscriptionID, resourceGroup, clusterName),
	}
	gwc := newGenericWatchingController("test-controller", coreapi.ClusterResourceType, inner)

	notifier := &capturingNotifier{}
	// Mirror the production ApplyDesire wiring: cluster-scoped only (maxDepth 1).
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

	// A cluster-scoped ApplyDesire sits one hop below the cluster, so maxDepth 1
	// reaches the cluster and enqueues it.
	clusterScopedID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToClusterScopedApplyDesireResourceIDString(subscriptionID, resourceGroup, clusterName, "cfg")))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: clusterScopedID})

	select {
	case key := <-syncedKeys:
		require.Equal(t, subscriptionID, key.SubscriptionID)
		require.Equal(t, resourceGroup, key.ResourceGroupName)
		require.Equal(t, clusterName, key.HCPClusterName)
	case <-time.After(5 * time.Second):
		t.Fatal("a cluster-scoped ApplyDesire event should have enqueued the cluster")
	}

	// A node-pool-nested ApplyDesire is two hops from the cluster, beyond
	// maxDepth 1, so it must NOT enqueue the cluster.
	nodePoolNestedID := metadataapi.Must(azcorearm.ParseResourceID(
		kubeapplierapi.ToNodePoolScopedApplyDesireResourceIDString(subscriptionID, resourceGroup, clusterName, "np", "cfg")))
	notifier.addFunc(&coreapi.CosmosMetadata{ResourceID: nodePoolNestedID})

	select {
	case key := <-syncedKeys:
		t.Fatalf("node-pool-nested ApplyDesire must not enqueue the cluster, but synced %+v", key)
	case <-time.After(500 * time.Millisecond):
		// expected: no enqueue for node-pool-nested desires
	}
}
