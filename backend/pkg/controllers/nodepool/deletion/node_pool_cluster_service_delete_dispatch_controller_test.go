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

package deletion

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	kubeapplierlistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testNodePoolName        = "test-nodepool"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testNodePoolCSIDStr     = testClusterServiceIDStr + "/node_pools/" + testNodePoolName
)

func TestNodePoolClusterServiceDeleteDispatchSyncer_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	verifyClusterServiceDeletionTimestampIsNil := func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err)
		assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	}

	verifyClusterServiceDeletionTimestampStamped := func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp, "expected ClusterServiceDeletionTimestamp to be stamped")
		assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime),
			"expected ClusterServiceDeletionTimestamp to equal fixedClockTime, got %v", stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time)
	}

	newFakeOCMParentClusterUninstallingError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("Cannot delete node pool: its parent cluster must be in a deletable state. Parent cluster state: 'uninstalling'").
			Build()
		return e
	}

	testCases := []struct {
		name                string
		existingNodePool    *coreapi.HCPOpenShiftClusterNodePool
		firstSeenDeletionAt time.Time
		setupMockCSClient   func(mock *ocm.MockClusterServiceClientSpec)
		wantErr             bool
		wantErrContain      string
		verifyDB            func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:             "when no DeletionTimestamp no-op is performed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, nil),
			verifyDB:         verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceDeletionTimestamp is set no-op is performed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
				assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime.Add(-30*time.Minute)),
					"expected ClusterServiceDeletionTimestamp unchanged")
			},
		},
		{
			name: "when ClusterServiceID is not set and deletion is first observed then first seen is recorded and no-op is performed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen within missing cluster service id is within timeout no-op is performed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			verifyDB:            verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen older than missing cluster service id timeout then we give up and set ClusterServiceDeletionTimestamp",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			verifyDB:            verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when ClusterServiceID is set we trigger CS nodepool deletion and set ClusterServiceDeletionTimestamp",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
					Return(nil)
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS nodepool deletion returns 404 and first seen is within the missing cluster service id timeout no-op is performed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when CS nodepool deletion returns 404 and first seen is older than the missing cluster service id timeout then we give up and set ClusterServiceDeletionTimestamp",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS nodepool deletion returns one of the not handled errors we propagate it without setting ClusterServiceDeletionTimestamp",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
					Return(errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to delete cluster-service NodePool",
		},
		{
			name: "when CS nodepool deletion returns parent cluster is uninstalling we set ClusterServiceDeletionTimestamp immediately",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Second)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
					Return(newFakeOCMParentClusterUninstallingError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "UsesNewNodePoolDeletionApproach false -- no-op even when DeletionTimestamp is set",
			existingNodePool: newTestNodePoolWithOldDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "node pool not found no-op is performed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			nodePoolsForLister := []*coreapi.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			firstSeenDeletionTimestampCache := lru.New(10)
			if !tc.firstSeenDeletionAt.IsZero() {
				firstSeenDeletionTimestampCache.Add(
					strings.ToLower(tc.existingNodePool.ID.String()),
					tc.firstSeenDeletionAt,
				)
			}

			syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
				clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
				nodePoolLister:                  &corelistertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				applyDesireLister:               &kubeapplierlistertesting.SliceApplyDesireLister{},
				firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
			}

			err = syncer.SyncOnce(ctx, testKey)
			if tc.wantErr {
				require.Error(t, err)
				if len(tc.wantErrContain) > 0 {
					require.ErrorContains(t, err, tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}

func TestNodePoolClusterServiceDeleteDispatchSyncer_SyncOnce_cacheShortCircuit(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	nodePoolInDB := newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
	})
	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{nodePoolInDB})
	require.NoError(t, err)

	// Here the cached node pool does not have a DeletionTimestamp set, so the syncer will short-circuit.
	cachedNodePool := newTestNodePoolWithNewDeletionApproach(t, nil)

	syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		nodePoolLister:                  &corelistertesting.SliceNodePoolLister{NodePools: []*coreapi.HCPOpenShiftClusterNodePool{cachedNodePool}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		applyDesireLister:               &kubeapplierlistertesting.SliceApplyDesireLister{},
		firstSeenDeletionTimestampCache: lru.New(10),
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Get(ctx, testNodePoolName)
	require.NoError(t, err)
	assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
}

func TestNodePoolClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCachePopulation(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	nodePool := newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	})
	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{nodePool})
	require.NoError(t, err)

	firstSeenDeletionTimestampCache := lru.New(10)
	syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		nodePoolLister:                  &corelistertesting.SliceNodePoolLister{NodePools: []*coreapi.HCPOpenShiftClusterNodePool{nodePool}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		applyDesireLister:               &kubeapplierlistertesting.SliceApplyDesireLister{},
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	cacheKey := strings.ToLower(nodePool.ID.String())
	firstSeenEntry, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	require.True(t, ok, "expected first seen deletion timestamp to be cached")
	assert.True(t, firstSeenEntry.(time.Time).Equal(fixedClockTime))
}

func TestNodePoolClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCacheCleared(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	nodePool := newTestNodePoolWithNewDeletionApproach(t, func(np *coreapi.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
	})
	mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{nodePool})
	require.NoError(t, err)

	cacheKey := strings.ToLower(nodePool.ID.String())
	firstSeenDeletionTimestampCache := lru.New(10)
	firstSeenDeletionTimestampCache.Add(cacheKey, fixedClockTime.Add(-missingClusterServiceIDTimeout/2))

	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCSClient.EXPECT().
		DeleteNodePool(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr))).
		Return(nil)

	syncer := &nodePoolClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		nodePoolLister:                  &corelistertesting.SliceNodePoolLister{NodePools: []*coreapi.HCPOpenShiftClusterNodePool{nodePool}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            mockCSClient,
		applyDesireLister:               &kubeapplierlistertesting.SliceApplyDesireLister{},
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	key := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, key))

	_, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	assert.False(t, ok, "expected first seen deletion cache entry to be removed after terminal sync")

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Get(ctx, testNodePoolName)
	require.NoError(t, err)
	require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime))
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}

// TODO rename this to newTestNodePoolWithNewDeletionApproach and remove the newTestNodePoolWithOldDeletionApproach function once
// the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
func newTestNodePoolWithNewDeletionApproach(t *testing.T, opts func(*coreapi.HCPOpenShiftClusterNodePool)) *coreapi.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	nodePoolInternalID := metadataapi.Ptr(metadataapi.Must(metadataapi.NewInternalID(testNodePoolCSIDStr)))
	np := &coreapi.HCPOpenShiftClusterNodePool{
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
		Properties: coreapi.HCPOpenShiftClusterNodePoolProperties{
			Platform: coreapi.NodePoolPlatformProfile{
				OSDisk: coreapi.OSDiskProfile{
					DiskStorageAccountType: metadataapi.DiskStorageAccountTypePremium_LRS,
					DiskType:               metadataapi.OsDiskTypeManaged,
				},
			},
		},
		ServiceProviderProperties: coreapi.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID:                nodePoolInternalID,
			UsesNewNodePoolDeletionApproach: true,
		},
	}
	if opts != nil {
		opts(np)
	}
	return np
}

// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
func newTestNodePoolWithOldDeletionApproach(t *testing.T, opts func(*coreapi.HCPOpenShiftClusterNodePool)) *coreapi.HCPOpenShiftClusterNodePool {
	np := newTestNodePoolWithNewDeletionApproach(t, opts)
	np.ServiceProviderProperties.UsesNewNodePoolDeletionApproach = false
	return np
}

func newTestSPNP(t *testing.T, bundles coreapi.MaestroBundleReferenceList) *coreapi.ServiceProviderNodePool {
	t.Helper()
	spnpResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/serviceProviderNodePools/default"))
	return &coreapi.ServiceProviderNodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: spnpResourceID, PartitionKey: strings.ToLower(spnpResourceID.SubscriptionID)},
		Status: coreapi.ServiceProviderNodePoolStatus{
			MaestroReadonlyBundles: bundles,
		},
	}
}
