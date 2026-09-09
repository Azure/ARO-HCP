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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}

// objectEventTracker records informer events in a thread-safe way.
type objectEventTracker struct {
	mu      sync.Mutex
	added   []runtime.Object
	updated []updateEvent
	deleted []runtime.Object
}

type updateEvent struct {
	oldObj runtime.Object
	newObj runtime.Object
}

func (e *objectEventTracker) onAdd(obj interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.added = append(e.added, obj.(runtime.Object))
}

func (e *objectEventTracker) onUpdate(oldObj, newObj interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.updated = append(e.updated, updateEvent{
		oldObj: oldObj.(runtime.Object),
		newObj: newObj.(runtime.Object),
	})
}

func (e *objectEventTracker) onDelete(obj interface{}) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = d.Obj
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleted = append(e.deleted, obj.(runtime.Object))
}

func (e *objectEventTracker) getAdded() []runtime.Object {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]runtime.Object, len(e.added))
	copy(ret, e.added)
	return ret
}

func (e *objectEventTracker) getUpdated() []updateEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]updateEvent, len(e.updated))
	copy(ret, e.updated)
	return ret
}

func (e *objectEventTracker) getDeleted() []runtime.Object {
	e.mu.Lock()
	defer e.mu.Unlock()
	ret := make([]runtime.Object, len(e.deleted))
	copy(ret, e.deleted)
	return ret
}

type informerIntegrationTestCase struct {
	name string

	// seedDB populates the database with initial items.
	seedDB func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient)

	// createInformer creates the SharedIndexInformer under test.
	createInformer func(resourcesDBClient corecosmosstorage.ResourcesDBClient) cache.SharedIndexInformer

	// expectedInitialAdds is the number of Add events expected from the initial list.
	expectedInitialAdds int

	// mutateDB modifies the database after initial sync.
	mutateDB func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient)

	// verifyMutationEvents checks events after mutation and relist.
	verifyMutationEvents func(t *testing.T, tracker *objectEventTracker)
}

func TestInformerEvents(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, testInformerEvents)
}

func testInformerEvents(t *testing.T, withMock bool) {
	testCases := []informerIntegrationTestCase{
		subscriptionInformerIntegrationTestCase(),
		clusterInformerIntegrationTestCase(),
		nodePoolInformerIntegrationTestCase(),
		activeOperationInformerIntegrationTestCase(),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			var storageInfo integrationutils.StorageIntegrationTestInfo
			var err error
			if withMock {
				storageInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
			} else {
				storageInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
			}
			require.NoError(t, err)
			defer storageInfo.Cleanup(context.Background())

			dbClient := storageInfo.ResourcesDBClient()
			tc.seedDB(t, ctx, dbClient)

			informer := tc.createInformer(dbClient)

			tracker := &objectEventTracker{}
			_, err = informer.AddEventHandlerWithResyncPeriod(
				cache.ResourceEventHandlerFuncs{
					AddFunc:    tracker.onAdd,
					UpdateFunc: tracker.onUpdate,
					DeleteFunc: tracker.onDelete,
				},
				4*time.Second)
			require.NoError(t, err)

			go informer.Run(ctx.Done())
			require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced), "timed out waiting for cache sync")

			// Verify initial adds.
			require.Eventually(t, func() bool {
				return len(tracker.getAdded()) == tc.expectedInitialAdds
			}, 10*time.Second, 200*time.Millisecond,
				"expected %d add events from initial list, got %d", tc.expectedInitialAdds, len(tracker.getAdded()))
			require.Empty(t, tracker.getUpdated(), "expected no update events after initial list")
			require.Empty(t, tracker.getDeleted(), "expected no delete events after initial list")

			// Mutate the database.
			tc.mutateDB(t, ctx, dbClient)

			// Wait for watcher to expire and reflector to relist.
			tc.verifyMutationEvents(t, tracker)
		})
	}
}

func TestInformerResync(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, testInformerResync)
}

func testInformerResync(t *testing.T, withMock bool) {
	testCases := []informerIntegrationTestCase{
		subscriptionInformerIntegrationTestCase(),
		clusterInformerIntegrationTestCase(),
		nodePoolInformerIntegrationTestCase(),
		activeOperationInformerIntegrationTestCase(),
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			var storageInfo integrationutils.StorageIntegrationTestInfo
			var err error
			if withMock {
				storageInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
			} else {
				storageInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
			}
			require.NoError(t, err)
			defer storageInfo.Cleanup(context.Background())

			dbClient := storageInfo.ResourcesDBClient()
			tc.seedDB(t, ctx, dbClient)

			informer := tc.createInformer(dbClient)

			tracker := &objectEventTracker{}
			_, err = informer.AddEventHandlerWithResyncPeriod(
				cache.ResourceEventHandlerFuncs{
					AddFunc:    tracker.onAdd,
					UpdateFunc: tracker.onUpdate,
					DeleteFunc: tracker.onDelete,
				},
				4*time.Second)
			require.NoError(t, err)

			go informer.Run(ctx.Done())
			require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced), "timed out waiting for cache sync")

			// Wait for initial adds.
			require.Eventually(t, func() bool {
				return len(tracker.getAdded()) == tc.expectedInitialAdds
			}, 5*time.Second, 200*time.Millisecond,
				"expected %d add events from initial list", tc.expectedInitialAdds)

			// Do NOT mutate the database. Wait for a relist cycle.
			// Verify onUpdate is called with both old and new objects.
			require.Eventually(t, func() bool {
				return len(tracker.getUpdated()) >= tc.expectedInitialAdds
			}, 10*time.Second, 200*time.Millisecond,
				"expected at least %d update events from resync", tc.expectedInitialAdds)

			for i, evt := range tracker.getUpdated() {
				require.NotNil(t, evt.oldObj, "update event %d has nil oldObj", i)
				require.NotNil(t, evt.newObj, "update event %d has nil newObj", i)
			}
		})
	}
}

// ---- Subscription informer integration test case ----

func subscriptionInformerIntegrationTestCase() informerIntegrationTestCase {
	return informerIntegrationTestCase{
		name: "subscription",
		seedDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			sub1 := &coreapi.Subscription{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   mustParseResourceID(t, "/subscriptions/sub-1"),
					PartitionKey: "sub-1",
				},
				State: coreapi.SubscriptionStateRegistered,
			}
			sub2 := &coreapi.Subscription{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   mustParseResourceID(t, "/subscriptions/sub-2"),
					PartitionKey: "sub-2",
				},
				State: coreapi.SubscriptionStateRegistered,
			}
			_, err := resourcesDBClient.Subscriptions().Create(ctx, sub1, nil)
			require.NoError(t, err)
			_, err = resourcesDBClient.Subscriptions().Create(ctx, sub2, nil)
			require.NoError(t, err)
		},
		createInformer: func(resourcesDBClient corecosmosstorage.ResourcesDBClient) cache.SharedIndexInformer {
			return coreinformers.NewSubscriptionInformerWithRelistDuration(resourcesDBClient.ResourcesGlobalListers().Subscriptions(), resourcesDBClient, 5*time.Second)
		},
		expectedInitialAdds: 2,
		mutateDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			// Deep-copy the live document so the Replace carries the existing
			// etag and instance version forward; PrepareForReplace rejects
			// fresh-built docs.
			existing, err := resourcesDBClient.Subscriptions().Get(ctx, "sub-1")
			require.NoError(t, err)
			sub1Updated := existing.DeepCopy()
			sub1Updated.State = coreapi.SubscriptionStateWarned
			_, err = resourcesDBClient.Subscriptions().Replace(ctx, sub1Updated, nil)
			require.NoError(t, err)

			sub3 := &coreapi.Subscription{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   mustParseResourceID(t, "/subscriptions/sub-3"),
					PartitionKey: "sub-3",
				},
				State: coreapi.SubscriptionStateRegistered,
			}
			_, err = resourcesDBClient.Subscriptions().Create(ctx, sub3, nil)
			require.NoError(t, err)

			err = resourcesDBClient.Subscriptions().Delete(ctx, "sub-2")
			require.NoError(t, err)
		},
		verifyMutationEvents: func(t *testing.T, tracker *objectEventTracker) {
			t.Helper()
			require.Eventually(t, func() bool {
				for _, evt := range tracker.getUpdated() {
					if sub, ok := evt.newObj.(*coreapi.Subscription); ok {
						if sub.ResourceID.SubscriptionID == "sub-1" && sub.State == coreapi.SubscriptionStateWarned {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected update event for sub-1 with state Warned")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getAdded() {
					if sub, ok := obj.(*coreapi.Subscription); ok {
						if sub.ResourceID.SubscriptionID == "sub-3" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected add event for sub-3")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getDeleted() {
					if sub, ok := obj.(*coreapi.Subscription); ok {
						if sub.ResourceID.SubscriptionID == "sub-2" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected delete event for sub-2")
		},
	}
}

// ---- Cluster informer integration test case ----

func clusterInformerIntegrationTestCase() informerIntegrationTestCase {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000001"
		resourceGroupName = "test-rg"
	)

	newCluster := func(t *testing.T, name string, state coreapi.ProvisioningState) *coreapi.HCPOpenShiftCluster {
		t.Helper()
		clusterResourceID := mustParseResourceID(t,
			"/subscriptions/"+subscriptionID+
				"/resourceGroups/"+resourceGroupName+
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+name)
		internalID, err := metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + name)
		require.NoError(t, err)
		return &coreapi.HCPOpenShiftCluster{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   clusterResourceID,
				PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
			},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{
					ID:   clusterResourceID,
					Name: name,
					Type: coreapi.ClusterResourceType.String(),
				},
				Location: "eastus",
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
				ProvisioningState: state,
				ClusterServiceID:  &internalID,
			},
		}
	}

	return informerIntegrationTestCase{
		name: "cluster",
		seedDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			clusterCRUD := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)
			_, err := clusterCRUD.Create(ctx, newCluster(t, "cluster-1", coreapi.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
			_, err = clusterCRUD.Create(ctx, newCluster(t, "cluster-2", coreapi.ProvisioningStateSucceeded), nil)
			require.NoError(t, err)
		},
		createInformer: func(resourcesDBClient corecosmosstorage.ResourcesDBClient) cache.SharedIndexInformer {
			return coreinformers.NewClusterInformerWithRelistDuration(resourcesDBClient.ResourcesGlobalListers().Clusters(), resourcesDBClient, 5*time.Second)
		},
		expectedInitialAdds: 2,
		mutateDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			clusterCRUD := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName)
			// Deep-copy the live document so the Replace carries the existing
			// etag and instance version forward; PrepareForReplace rejects
			// fresh-built docs.
			existing, err := clusterCRUD.Get(ctx, "cluster-1")
			require.NoError(t, err)
			updated := existing.DeepCopy()
			updated.ServiceProviderProperties.ProvisioningState = coreapi.ProvisioningStateDeleting
			_, err = clusterCRUD.Replace(ctx, updated, nil)
			require.NoError(t, err)

			_, err = clusterCRUD.Create(ctx, newCluster(t, "cluster-3", coreapi.ProvisioningStateAccepted), nil)
			require.NoError(t, err)

			err = clusterCRUD.Delete(ctx, "cluster-2")
			require.NoError(t, err)
		},
		verifyMutationEvents: func(t *testing.T, tracker *objectEventTracker) {
			t.Helper()
			require.Eventually(t, func() bool {
				for _, evt := range tracker.getUpdated() {
					if c, ok := evt.newObj.(*coreapi.HCPOpenShiftCluster); ok {
						if c.Name == "cluster-1" && c.ServiceProviderProperties.ProvisioningState == coreapi.ProvisioningStateDeleting {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected update event for cluster-1")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getAdded() {
					if c, ok := obj.(*coreapi.HCPOpenShiftCluster); ok {
						if c.Name == "cluster-3" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected add event for cluster-3")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getDeleted() {
					if c, ok := obj.(*coreapi.HCPOpenShiftCluster); ok {
						if c.Name == "cluster-2" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected delete event for cluster-2")
		},
	}
}

// ---- NodePool informer integration test case ----

func nodePoolInformerIntegrationTestCase() informerIntegrationTestCase {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000002"
		resourceGroupName = "test-rg"
		clusterName       = "parent-cluster"
	)

	newNodePool := func(t *testing.T, name string, replicas int32) *coreapi.HCPOpenShiftClusterNodePool {
		t.Helper()
		npResourceID := mustParseResourceID(t,
			"/subscriptions/"+subscriptionID+
				"/resourceGroups/"+resourceGroupName+
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+
				"/nodePools/"+name)
		internalID := metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + clusterName + "/node_pools/" + name)))
		return &coreapi.HCPOpenShiftClusterNodePool{
			CosmosMetadata: coreapi.CosmosMetadata{ResourceID: npResourceID, PartitionKey: strings.ToLower(npResourceID.SubscriptionID)},
			TrackedResource: coreapi.TrackedResource{
				Resource: coreapi.Resource{
					ID:   npResourceID,
					Name: name,
					Type: coreapi.NodePoolResourceType.String(),
				},
				Location: "eastus",
			},
			Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
				ProvisioningState: coreapi.ProvisioningStateSucceeded,
				Replicas:          replicas,
			},
			ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
				ClusterServiceID: internalID,
			},
		}
	}

	return informerIntegrationTestCase{
		name: "nodePool",
		seedDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			// Create parent cluster first.
			clusterResourceID := mustParseResourceID(t,
				"/subscriptions/"+subscriptionID+
					"/resourceGroups/"+resourceGroupName+
					"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName)
			internalID, err := metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName)
			require.NoError(t, err)
			cluster := &coreapi.HCPOpenShiftCluster{
				CosmosMetadata: coreapi.CosmosMetadata{
					ResourceID:   clusterResourceID,
					PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
				},
				TrackedResource: coreapi.TrackedResource{
					Resource: coreapi.Resource{
						ID:   clusterResourceID,
						Name: clusterName,
						Type: coreapi.ClusterResourceType.String(),
					},
					Location: "eastus",
				},
				ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
					ProvisioningState: coreapi.ProvisioningStateSucceeded,
					ClusterServiceID:  &internalID,
				},
			}
			_, err = resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(ctx, cluster, nil)
			require.NoError(t, err)

			npCRUD := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName)
			_, err = npCRUD.Create(ctx, newNodePool(t, "np-1", 3), nil)
			require.NoError(t, err)
			_, err = npCRUD.Create(ctx, newNodePool(t, "np-2", 5), nil)
			require.NoError(t, err)
		},
		createInformer: func(resourcesDBClient corecosmosstorage.ResourcesDBClient) cache.SharedIndexInformer {
			return coreinformers.NewNodePoolInformerWithRelistDuration(resourcesDBClient.ResourcesGlobalListers().NodePools(), resourcesDBClient, 5*time.Second)
		},
		expectedInitialAdds: 2,
		mutateDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			npCRUD := resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName)

			// Deep-copy the live document so the Replace carries the existing
			// etag and instance version forward; PrepareForReplace rejects
			// fresh-built docs.
			existing, err := npCRUD.Get(ctx, "np-1")
			require.NoError(t, err)
			updated := existing.DeepCopy()
			updated.Properties.Replicas = 10
			_, err = npCRUD.Replace(ctx, updated, nil)
			require.NoError(t, err)

			_, err = npCRUD.Create(ctx, newNodePool(t, "np-3", 2), nil)
			require.NoError(t, err)

			err = npCRUD.Delete(ctx, "np-2")
			require.NoError(t, err)
		},
		verifyMutationEvents: func(t *testing.T, tracker *objectEventTracker) {
			t.Helper()
			require.Eventually(t, func() bool {
				for _, evt := range tracker.getUpdated() {
					if np, ok := evt.newObj.(*coreapi.HCPOpenShiftClusterNodePool); ok {
						if np.Name == "np-1" && np.Properties.Replicas == 10 {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected update event for np-1 with replicas=10")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getAdded() {
					if np, ok := obj.(*coreapi.HCPOpenShiftClusterNodePool); ok {
						if np.Name == "np-3" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected add event for np-3")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getDeleted() {
					if np, ok := obj.(*coreapi.HCPOpenShiftClusterNodePool); ok {
						if np.Name == "np-2" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected delete event for np-2")
		},
	}
}

// ---- Active operation informer integration test case ----

func activeOperationInformerIntegrationTestCase() informerIntegrationTestCase {
	const subscriptionID = "00000000-0000-0000-0000-000000000003"

	newOperation := func(t *testing.T, opName string, status coreapi.ProvisioningState) *coreapi.Operation {
		t.Helper()
		operationID := mustParseResourceID(t,
			"/subscriptions/"+subscriptionID+
				"/providers/Microsoft.RedHatOpenShift/locations/eastus/hcpOperationStatuses/"+opName)
		externalID := mustParseResourceID(t,
			"/subscriptions/"+subscriptionID+
				"/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster")
		resourceID := mustParseResourceID(t,
			"/subscriptions/"+subscriptionID+
				"/providers/Microsoft.RedHatOpenShift/hcpOperationStatuses/"+opName)
		now := time.Now().UTC()
		return &coreapi.Operation{
			CosmosMetadata: coreapi.CosmosMetadata{
				ResourceID:   resourceID,
				PartitionKey: strings.ToLower(resourceID.SubscriptionID),
			},
			OperationID:        operationID,
			ExternalID:         externalID,
			Request:            coreapi.OperationRequestCreate,
			Status:             status,
			StartTime:          now,
			LastTransitionTime: now,
		}
	}

	return informerIntegrationTestCase{
		name: "activeOperation",
		seedDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			opCRUD := resourcesDBClient.Operations(subscriptionID)
			_, err := opCRUD.Create(ctx, newOperation(t, "op-1", coreapi.ProvisioningStateAccepted), nil)
			require.NoError(t, err)
			_, err = opCRUD.Create(ctx, newOperation(t, "op-2", coreapi.ProvisioningStateProvisioning), nil)
			require.NoError(t, err)
		},
		createInformer: func(resourcesDBClient corecosmosstorage.ResourcesDBClient) cache.SharedIndexInformer {
			return coreinformers.NewActiveOperationInformerWithRelistDuration(resourcesDBClient.ResourcesGlobalListers().ActiveOperations(), resourcesDBClient, 5*time.Second)
		},
		expectedInitialAdds: 2,
		mutateDB: func(t *testing.T, ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient) {
			t.Helper()
			opCRUD := resourcesDBClient.Operations(subscriptionID)

			// Transition op-1 to terminal state — should appear as deletion.
			// Deep-copy the live document so the Replace carries the existing
			// etag and instance version forward; PrepareForReplace rejects
			// fresh-built docs.
			existing, err := opCRUD.Get(ctx, "op-1")
			require.NoError(t, err)
			updated := existing.DeepCopy()
			updated.Status = coreapi.ProvisioningStateSucceeded
			_, err = opCRUD.Replace(ctx, updated, nil)
			require.NoError(t, err)

			// Add new active operation.
			_, err = opCRUD.Create(ctx, newOperation(t, "op-3", coreapi.ProvisioningStateAccepted), nil)
			require.NoError(t, err)
		},
		verifyMutationEvents: func(t *testing.T, tracker *objectEventTracker) {
			t.Helper()
			require.Eventually(t, func() bool {
				return len(tracker.getDeleted()) >= 1
			}, 45*time.Second, 200*time.Millisecond, "expected delete event for op-1 (now terminal)")

			require.Eventually(t, func() bool {
				for _, obj := range tracker.getAdded() {
					if op, ok := obj.(*coreapi.Operation); ok {
						if op.OperationID != nil && op.OperationID.Name == "op-3" {
							return true
						}
					}
				}
				return false
			}, 45*time.Second, 200*time.Millisecond, "expected add event for op-3")
		},
	}
}

// TestServiceProviderNodePoolLister verifies that after seeding a
// ServiceProviderNodePool in cosmos and starting the SPNP informer with a
// very short relist duration, the serviceProviderNodePoolLister built from
// the informer's indexer eventually surfaces the SPNP via both List and Get.
func TestServiceProviderNodePoolLister(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, testServiceProviderNodePoolLister)
}

func testServiceProviderNodePoolLister(t *testing.T, withMock bool) {
	const (
		subscriptionID    = "00000000-0000-0000-0000-000000000004"
		resourceGroupName = "test-rg"
		clusterName       = "spnp-cluster"
		nodePoolName      = "spnp-np"
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var storageInfo integrationutils.StorageIntegrationTestInfo
	var err error
	if withMock {
		storageInfo, err = integrationutils.NewMockCosmosFromTestingEnv(ctx, t)
	} else {
		storageInfo, err = integrationutils.NewCosmosFromTestingEnv(ctx, t)
	}
	require.NoError(t, err)
	defer storageInfo.Cleanup(context.Background())

	resourcesDBClient := storageInfo.ResourcesDBClient()

	// Seed the parent cluster, parent node pool, and the SPNP.
	clusterResourceID := mustParseResourceID(t,
		"/subscriptions/"+subscriptionID+
			"/resourceGroups/"+resourceGroupName+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName)
	clusterInternalID, err := metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/" + clusterName)
	require.NoError(t, err)
	cluster := &coreapi.HCPOpenShiftCluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   clusterResourceID,
				Name: clusterName,
				Type: coreapi.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			ClusterServiceID:  &clusterInternalID,
		},
	}
	_, err = resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err, "failed to seed parent cluster")

	npResourceID := mustParseResourceID(t,
		"/subscriptions/"+subscriptionID+
			"/resourceGroups/"+resourceGroupName+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+
			"/nodePools/"+nodePoolName)
	npInternalID := metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/" + clusterName + "/node_pools/" + nodePoolName)))
	nodePool := &coreapi.HCPOpenShiftClusterNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   npResourceID,
			PartitionKey: strings.ToLower(npResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   npResourceID,
				Name: nodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			ProvisioningState: coreapi.ProvisioningStateSucceeded,
			Replicas:          1,
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: npInternalID,
		},
	}
	_, err = resourcesDBClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err, "failed to seed parent node pool")

	spnpResourceID := mustParseResourceID(t,
		"/subscriptions/"+subscriptionID+
			"/resourceGroups/"+resourceGroupName+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+clusterName+
			"/nodePools/"+nodePoolName+
			"/serviceProviderNodePools/"+coreapi.ServiceProviderNodePoolResourceName)
	spnp := &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   spnpResourceID,
			PartitionKey: strings.ToLower(spnpResourceID.SubscriptionID),
		},
	}
	_, err = resourcesDBClient.ServiceProviderNodePools(subscriptionID, resourceGroupName, clusterName, nodePoolName).Create(ctx, spnp, nil)
	require.NoError(t, err, "failed to seed service provider node pool")

	// Start the SPNP informer with a very short relist duration so the cache
	// observes the seeded object quickly.
	informer := coreinformers.NewServiceProviderNodePoolInformerWithRelistDuration(
		resourcesDBClient.ResourcesGlobalListers().ServiceProviderNodePools(),
		resourcesDBClient,
		1*time.Second)
	go informer.Run(ctx.Done())
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced), "timed out waiting for service provider node pool informer cache sync")

	lister := corelisters.NewServiceProviderNodePoolLister(informer.GetIndexer())

	// Wait up to 30s for the SPNP to be visible via List.
	require.Eventually(t, func() bool {
		all, err := lister.List(ctx)
		if err != nil {
			return false
		}
		for _, item := range all {
			if item.ResourceID != nil && strings.EqualFold(item.ResourceID.String(), spnpResourceID.String()) {
				return true
			}
		}
		return false
	}, 30*time.Second, 200*time.Millisecond, "service provider node pool never appeared in lister.List output")

	// Wait up to 30s for the SPNP to be visible via Get.
	require.Eventually(t, func() bool {
		got, err := lister.Get(ctx, subscriptionID, resourceGroupName, clusterName, nodePoolName)
		if err != nil {
			return false
		}
		return got != nil && got.ResourceID != nil && strings.EqualFold(got.ResourceID.String(), spnpResourceID.String())
	}, 30*time.Second, 200*time.Millisecond, "service provider node pool never appeared in lister.Get output")
}
