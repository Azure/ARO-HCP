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

package clusterdeletion

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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}

func newTestClusterWithNewDeletionApproach(t *testing.T, opts func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID := api.Ptr(api.Must(api.NewInternalID(testClusterServiceIDStr)))
	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		CosmosMetadata: arm.CosmosMetadata{ResourceID: resourceID},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID:               clusterInternalID,
			UsesNewClusterDeletionApproach: true,
		},
	}
	if opts != nil {
		opts(cluster)
	}
	return cluster
}

func newTestClusterWithOldDeletionApproach(t *testing.T, opts func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
	cluster := newTestClusterWithNewDeletionApproach(t, opts)
	cluster.ServiceProviderProperties.UsesNewClusterDeletionApproach = false
	return cluster
}

func TestClusterClusterServiceDeleteDispatchSyncer_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	verifyClusterServiceDeletionTimestampIsNil := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		require.NoError(t, err)
		assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	}

	verifyClusterServiceDeletionTimestampStamped := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp, "expected ClusterServiceDeletionTimestamp to be stamped")
		assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime),
			"expected ClusterServiceDeletionTimestamp to equal fixedClockTime, got %v", stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time)
	}

	testCases := []struct {
		name                string
		existingCluster     *api.HCPOpenShiftCluster
		firstSeenDeletionAt time.Time
		setupMockCSClient   func(mock *ocm.MockClusterServiceClientSpec)
		wantErr             bool
		wantErrContain      string
		verifyDB            func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:            "when no DeletionTimestamp no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
			verifyDB:        verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceDeletionTimestamp is set no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
				assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime.Add(-30*time.Minute)),
					"expected ClusterServiceDeletionTimestamp unchanged")
			},
		},
		{
			name: "when ClusterServiceID is not set and deletion is first observed then first seen is recorded and no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen within timeout no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			verifyDB:            verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when ClusterServiceID is not set and first seen older than timeout then we give up and stamp",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			verifyDB:            verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when ClusterServiceID is set we trigger CS cluster deletion and stamp",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(nil)
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS cluster deletion returns 404 within timeout no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "when CS cluster deletion returns 404 past timeout then we stamp",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout - time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(fakeOCMNotFoundError())
			},
			verifyDB: verifyClusterServiceDeletionTimestampStamped,
		},
		{
			name: "when CS cluster deletion returns unhandled error we propagate it",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			firstSeenDeletionAt: fixedClockTime.Add(-missingClusterServiceIDTimeout / 2),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					DeleteCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to delete cluster-service Cluster",
		},
		{
			name: "UsesNewClusterDeletionApproach false -- no-op even when DeletionTimestamp is set",
			existingCluster: newTestClusterWithOldDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
			}),
			verifyDB: verifyClusterServiceDeletionTimestampIsNil,
		},
		{
			name: "cluster not found no-op is performed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			clustersForLister := []*api.HCPOpenShiftCluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			firstSeenDeletionTimestampCache := lru.New(10)
			if !tc.firstSeenDeletionAt.IsZero() {
				firstSeenDeletionTimestampCache.Add(
					strings.ToLower(tc.existingCluster.ID.String()),
					tc.firstSeenDeletionAt,
				)
			}

			syncer := &clusterClusterServiceDeleteDispatchSyncer{
				clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
				cooldownChecker:                 &alwaysSyncCooldownChecker{},
				clusterLister:                   &listertesting.SliceClusterLister{Clusters: clustersForLister},
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
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

func TestClusterClusterServiceDeleteDispatchSyncer_SyncOnce_cacheShortCircuit(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	clusterInDB := newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{clusterInDB})
	require.NoError(t, err)

	// Here the cached cluster does not have a DeletionTimestamp set, so the syncer will short-circuit.
	cachedCluster := newTestClusterWithNewDeletionApproach(t, nil)

	syncer := &clusterClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		cooldownChecker:                 &alwaysSyncCooldownChecker{},
		clusterLister:                   &listertesting.SliceClusterLister{Clusters: []*api.HCPOpenShiftCluster{cachedCluster}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		firstSeenDeletionTimestampCache: lru.New(10),
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
	require.NoError(t, err)
	assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
}

func TestClusterClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCachePopulation(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	cluster := newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		c.ServiceProviderProperties.ClusterServiceID = nil
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
	require.NoError(t, err)

	firstSeenDeletionTimestampCache := lru.New(10)
	syncer := &clusterClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		cooldownChecker:                 &alwaysSyncCooldownChecker{},
		clusterLister:                   &listertesting.SliceClusterLister{Clusters: []*api.HCPOpenShiftCluster{cluster}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            ocm.NewMockClusterServiceClientSpec(ctrl),
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	cacheKey := strings.ToLower(cluster.ID.String())
	firstSeenEntry, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	require.True(t, ok, "expected first seen deletion timestamp to be cached")
	assert.True(t, firstSeenEntry.(time.Time).Equal(fixedClockTime))
}

func TestClusterClusterServiceDeleteDispatchSyncer_SyncOnce_firstSeenDeletionCacheCleared(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)
	ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
	ctrl := gomock.NewController(t)

	cluster := newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Minute)}
	})
	mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
	require.NoError(t, err)

	cacheKey := strings.ToLower(cluster.ID.String())
	firstSeenDeletionTimestampCache := lru.New(10)
	firstSeenDeletionTimestampCache.Add(cacheKey, fixedClockTime.Add(-missingClusterServiceIDTimeout/2))

	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCSClient.EXPECT().
		DeleteCluster(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
		Return(nil)

	syncer := &clusterClusterServiceDeleteDispatchSyncer{
		clock:                           clocktesting.NewFakePassiveClock(fixedClockTime),
		cooldownChecker:                 &alwaysSyncCooldownChecker{},
		clusterLister:                   &listertesting.SliceClusterLister{Clusters: []*api.HCPOpenShiftCluster{cluster}},
		resourcesDBClient:               mockResourcesDBClient,
		clusterServiceClient:            mockCSClient,
		firstSeenDeletionTimestampCache: firstSeenDeletionTimestampCache,
	}

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	require.NoError(t, syncer.SyncOnce(ctx, testKey))

	_, ok := firstSeenDeletionTimestampCache.Get(cacheKey)
	assert.False(t, ok, "expected first seen deletion cache entry to be removed after terminal sync")

	stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
	require.NoError(t, err)
	require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp)
	assert.True(t, stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp.Time.Equal(fixedClockTime))
}
