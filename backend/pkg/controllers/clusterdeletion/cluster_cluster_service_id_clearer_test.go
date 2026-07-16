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
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
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

	withDeletionStampsClusterOptsFunc := func(c *api.HCPOpenShiftCluster) {
		c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
		c.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-30 * time.Minute)}
	}

	verifyClusterServiceIDUnchanged := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceID, "ClusterServiceID should not be nil")
		assert.Equal(t, testClusterServiceIDStr, stored.ServiceProviderProperties.ClusterServiceID.String(),
			"ClusterServiceID should be unchanged")
	}

	testCases := []struct {
		name              string
		existingCluster   *api.HCPOpenShiftCluster
		setupMockCSClient func(mock *ocm.MockClusterServiceClientSpec)
		wantErr           bool
		wantErrContain    string
		verifyDB          func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:            "when CS returns 404 ClusterServiceID is cleared",
			existingCluster: newTestClusterWithNewDeletionApproach(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(nil, fakeOCMNotFoundError())
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceID)
			},
		},
		{
			name:            "when CS returns cluster still exists no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(nil, nil)
			},
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name:            "feature flag false -- no-op even with all timestamps set",
			existingCluster: newTestClusterWithOldDeletionApproach(t, withDeletionStampsClusterOptsFunc),
			verifyDB:        verifyClusterServiceIDUnchanged,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not yet -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedClockTime.Add(-time.Hour)}
			}),
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name: "ClusterServiceID already cleared -- no-op",
			existingCluster: newTestClusterWithNewDeletionApproach(t, func(c *api.HCPOpenShiftCluster) {
				withDeletionStampsClusterOptsFunc(c)
				c.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceID, "ClusterServiceID should remain nil")
			},
		},
		{
			name:            "CS returns unhandled error -- propagated, no clear",
			existingCluster: newTestClusterWithNewDeletionApproach(t, withDeletionStampsClusterOptsFunc),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetClusterStatus(gomock.Any(), api.Must(api.NewInternalID(testClusterServiceIDStr))).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to get cluster-service Cluster",
		},
		{
			name:            "when no DeletionTimestamp no-op is performed",
			existingCluster: newTestClusterWithNewDeletionApproach(t, nil),
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

			syncer := &clusterClusterServiceIDClearer{
				clusterLister:        &listertesting.SliceClusterLister{Clusters: clustersForLister},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
			}

			_, err = syncer.SyncOnce(ctx, testKey)
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
