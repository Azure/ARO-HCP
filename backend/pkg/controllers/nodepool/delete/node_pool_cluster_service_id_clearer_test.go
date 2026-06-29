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

package delete

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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestNodePoolClusterServiceIDClearer_SyncOnce(t *testing.T) {
	fixedNow := time.Now().UTC().Truncate(time.Second)
	withDeletionStampsNodePoolOptsFunc := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
	}

	verifyClusterServiceIDUnchanged := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err)
		require.NotNil(t, stored.ServiceProviderProperties.ClusterServiceID, "ClusterServiceID should not be nil")
		assert.Equal(t, testNodePoolCSIDStr, stored.ServiceProviderProperties.ClusterServiceID.String(),
			"ClusterServiceID should be unchanged")
	}

	testCases := []struct {
		name              string
		existingNodePool  *api.HCPOpenShiftClusterNodePool
		setupMockCSClient func(mock *ocm.MockClusterServiceClientSpec)
		wantErr           bool
		wantErrContain    string
		verifyDB          func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:             "no DeletionTimestamp -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, nil),
			verifyDB:         verifyClusterServiceIDUnchanged,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not yet -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name: "ClusterServiceID already cleared -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStampsNodePoolOptsFunc(np)
				np.ServiceProviderProperties.ClusterServiceID = nil
			}),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				t.Helper()
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceID, "ClusterServiceID should remain nil")
			},
		},
		{
			name: "CS NodePool still present -- wait",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStampsNodePoolOptsFunc(np)
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr))).
					Return(newCSNodePool(t), nil)
			},
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name: "CS returns 404 -- clear ClusterServiceID",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStampsNodePoolOptsFunc(np)
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr))).
					Return(nil, fakeOCMNotFoundError())
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				t.Helper()
				stored, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				assert.Nil(t, stored.ServiceProviderProperties.ClusterServiceID, "expected ClusterServiceID to be cleared")
			},
		},
		{
			name: "CS returns one of the not handled errors -- propagated, no clear",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStampsNodePoolOptsFunc(np)
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr))).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to get cluster-service NodePool",
		},
		{
			name: "UsesNewNodePoolDeletionApproach false -- no-op even when all clear conditions met",
			existingNodePool: newTestNodePoolWithOldDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStampsNodePoolOptsFunc(np)
			}),
			verifyDB: verifyClusterServiceIDUnchanged,
		},
		{
			name: "node pool not found",
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
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			syncer := &nodePoolClusterServiceIDClearer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
			}

			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			}

			err = syncer.SyncOnce(ctx, key)
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

func newCSNodePool(t *testing.T) *arohcpv1alpha1.NodePool {
	t.Helper()
	np, err := arohcpv1alpha1.NewNodePool().ID(testNodePoolName).Build()
	require.NoError(t, err)
	return np
}
