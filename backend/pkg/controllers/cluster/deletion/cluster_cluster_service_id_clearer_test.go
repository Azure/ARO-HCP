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
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterClusterServiceIDClearer_SyncOnce(t *testing.T) {
	fixedClockTime := time.Now().UTC().Truncate(time.Second)

	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	withDeletionStampsClusterOptsFunc := func(c *coreapi.Cluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
	}

	verifyClusterServiceIDUnchanged := func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.NotNil(t, stored.ClusterServiceID, "ClusterServiceID should not be nil")
		assert.Equal(t, testClusterServiceIDStr, stored.ClusterServiceID.String(),
			"ClusterServiceID should be unchanged")
	}

	testCases := []struct {
		noClusterServiceID bool
		name               string
		existingCluster    *coreapi.Cluster
		setupMockCSClient  func(mock *ocm.MockClusterServiceClientSpec)
		wantErr            bool
		wantErrContain     string
		verifyDB           func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:            "when CS returns 404 ClusterServiceID is cleared",
			existingCluster: newTestCluster(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))).
					Return(nil, fakeOCMNotFoundError())
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				stored, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Nil(t, stored.ClusterServiceID)
			},
		},
		{
			name:            "when CS returns cluster still exists no-op is performed",
			existingCluster: newTestCluster(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))).
					Return(nil, nil)
			},
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not yet -- no-op",
			existingCluster: newTestCluster(t, func(c *coreapi.Cluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			noClusterServiceID: true,
			name:               "ClusterServiceID already cleared -- no-op",
			existingCluster: newTestCluster(t, func(c *coreapi.Cluster) {
				withDeletionStampsClusterOptsFunc(c)
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				stored, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				assert.Nil(t, stored.ClusterServiceID, "ClusterServiceID should remain nil")
			},
		},
		{
			name:            "CS returns unhandled error -- propagated, no clear",
			existingCluster: newTestCluster(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to get cluster-service Cluster",
		},
		{
			name:            "when no DeletionTimestamp no-op is performed",
			existingCluster: newTestCluster(t, nil),
			verifyDB:        verifyClusterServiceIDUnchanged,
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
			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)
			seedProviderIDs(t, ctx, mockResourcesDBClient, tc.existingCluster, !tc.noClusterServiceID)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			clustersForLister := []*coreapi.Cluster{}
			if tc.existingCluster != nil {
				clustersForLister = append(clustersForLister, tc.existingCluster)
			}

			syncer := &clusterClusterServiceIDClearer{
				clusterLister:        &corelistertesting.SliceClusterLister{Clusters: clustersForLister},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
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

func TestClusterClusterServiceIDClearer_NormalizesEmptyIDs(t *testing.T) {
	for _, emptyFinal := range []bool{false, true} {
		for _, completed := range []bool{false, true} {
			t.Run(fmt.Sprintf("emptyFinal=%t/completed=%t", emptyFinal, completed), func(t *testing.T) {
				ctx := t.Context()
				cluster := newTestCluster(t, func(cluster *coreapi.Cluster) {
					now := metav1.Now()
					cluster.ServiceProviderProperties.DeletionTimestamp = &now
					cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
					cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = true
					cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = completed
				})
				db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
				require.NoError(t, err)
				seedProviderIDs(t, ctx, db, cluster, false)
				providers := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName)
				provider, err := providers.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				provider.PendingClusterServiceID = &metadataapi.InternalID{}
				provider.ClusterServiceID = nil
				if emptyFinal {
					provider.ClusterServiceID = &metadataapi.InternalID{}
				}
				_, err = providers.Replace(ctx, provider, nil)
				require.NoError(t, err)
				syncer := &clusterClusterServiceIDClearer{
					clusterLister:        &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}},
					resourcesDBClient:    db,
					clusterServiceClient: ocm.NewMockClusterServiceClientSpec(gomock.NewController(t)),
				}
				key := controllerutils.HCPClusterKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName}
				require.NoError(t, syncer.SyncOnce(ctx, key))
				parent, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.True(t, parent.ServiceProviderProperties.ClusterServiceDeletionCompleted)
				provider, err = providers.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Nil(t, provider.PendingClusterServiceID)
				require.Nil(t, provider.ClusterServiceID)
				require.NoError(t, syncer.SyncOnce(ctx, key))
				repeated, err := providers.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Equal(t, provider.GetEtag(), repeated.GetEtag())
				require.NoError(t, providers.Delete(ctx, coreapi.ServiceProviderClusterResourceName))
				require.NoError(t, syncer.SyncOnce(ctx, key))
				_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, cluster.ID)
				require.Error(t, err)
				_, err = providers.Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
				afterCleanup, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.Equal(t, parent.GetEtag(), afterCleanup.GetEtag())
			})
		}
	}
}
