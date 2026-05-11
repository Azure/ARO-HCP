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

package nodepooldeletion

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
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	withDeletionStamps := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
	}

	testCases := []struct {
		name              string
		existingNodePool  *api.HCPOpenShiftClusterNodePool
		expectGetNodePool bool
		csNodePool        *arohcpv1alpha1.NodePool
		getNodePoolErr    error
		wantCSIDCleared   bool
		wantErr           bool
		wantErrContain    string
	}{
		{
			name:             "no DeletionTimestamp — no-op",
			existingNodePool: newTestNodePool(t, nil),
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not yet — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
		},
		{
			name: "ClusterServiceID already cleared — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
				np.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
			}),
		},
		{
			name: "CS NodePool still present — wait",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
			}),
			expectGetNodePool: true,
			csNodePool:        newCSNodePool(t),
		},
		{
			name: "CS returns 404 — clear ClusterServiceID",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
			}),
			expectGetNodePool: true,
			getNodePoolErr:    fakeOCMNotFoundError(),
			wantCSIDCleared:   true,
		},
		{
			name: "CS returns transient error — propagated, no clear",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				withDeletionStamps(np)
			}),
			expectGetNodePool: true,
			getNodePoolErr:    errors.New("boom"),
			wantErr:           true,
			wantErrContain:    "failed to get cluster-service NodePool",
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
			if tc.expectGetNodePool {
				call := mockCSClient.EXPECT().
					GetNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr)))
				if tc.getNodePoolErr != nil {
					call.Return(nil, tc.getNodePoolErr)
				} else {
					call.Return(tc.csNodePool, nil)
				}
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
				require.Greater(t, len(tc.wantErrContain), 0, "wantErrContain must be set when wantErr is true")
				assert.ErrorContains(t, err, tc.wantErrContain)
				return
			}
			require.NoError(t, err)

			if tc.existingNodePool == nil {
				return
			}
			stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				NodePools(testClusterName).Get(ctx, testNodePoolName)
			require.NoError(t, err)
			if tc.wantCSIDCleared {
				assert.Empty(t, stored.ServiceProviderProperties.ClusterServiceID.String(), "expected ClusterServiceID to be cleared")
			} else {
				assert.Equal(t,
					tc.existingNodePool.ServiceProviderProperties.ClusterServiceID.String(),
					stored.ServiceProviderProperties.ClusterServiceID.String(),
					"ClusterServiceID should be unchanged")
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
