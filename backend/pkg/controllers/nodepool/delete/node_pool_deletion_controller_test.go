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
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestNodePoolDeletionController_SyncOnce(t *testing.T) {
	fixedNow := time.Now().UTC().Truncate(time.Second)
	readyToDeleteNodePoolOptsFunc := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	verifyNodePoolStillExists := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		require.NoError(t, err, "expected nodepool to still exist in Cosmos")
	}

	verifyNodePoolDeleted := func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
		t.Helper()
		_, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).
			NodePools(testClusterName).Get(ctx, testNodePoolName)
		assert.True(t, database.IsNotFoundError(err), "expected nodepool to be deleted from Cosmos")
	}

	testCases := []struct {
		name             string
		existingNodePool *api.HCPOpenShiftClusterNodePool
		childResources   []any
		wantErr          bool
		verifyDB         func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:             "no DeletionTimestamp -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, nil),
			verifyDB:         verifyNodePoolStillExists,
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
			verifyDB: verifyNodePoolStillExists,
		},
		{
			name: "ClusterServiceID still set -- no-op",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
			verifyDB: verifyNodePoolStillExists,
		},
		{
			name:             "all conditions met, no children -- deletes nodepool from Cosmos",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			verifyDB:         verifyNodePoolDeleted,
		},
		{
			name:             "all conditions met, SPNP has remaining bundles -- backs off",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources: []any{newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "readonly-bundle-1"},
			})},
			verifyDB: verifyNodePoolStillExists,
		},
		{
			name:             "all conditions met, SPNP with no bundles -- backs off until SPNP removed",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestSPNP(t, nil)},
			verifyDB:         verifyNodePoolStillExists,
		},
		{
			name:             "all conditions met, a non node pool controller cosmos child exists -- backs off",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestManagementClusterContent(t, "test-mcc")},
			verifyDB:         verifyNodePoolStillExists,
		},
		{
			name:             "all conditions met, only controller children -- deletes nodepool",
			existingNodePool: newTestNodePoolWithNewDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestNodePoolController(t, "test-controller")},
			verifyDB:         verifyNodePoolDeleted,
		},
		{
			name:             "UsesNewNodePoolDeletionApproach false -- no-op even when all delete conditions met",
			existingNodePool: newTestNodePoolWithOldDeletionApproach(t, readyToDeleteNodePoolOptsFunc),
			childResources:   []any{newTestNodePoolController(t, "test-controller")},
			verifyDB:         verifyNodePoolStillExists,
		},
		{
			name: "node pool not found -- no-op",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			resources = append(resources, tc.childResources...)
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}
			spnpForLister := []*api.ServiceProviderNodePool{}
			for _, child := range tc.childResources {
				if spnp, ok := child.(*api.ServiceProviderNodePool); ok {
					spnpForLister = append(spnpForLister, spnp)
				}
			}

			syncer := &nodePoolDeletionController{
				cooldownChecker:               &alwaysSyncCooldownChecker{},
				nodePoolLister:                &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				serviceProviderNodePoolLister: &listertesting.SliceServiceProviderNodePoolLister{ServiceProviderNodePools: spnpForLister},
				resourcesDBClient:             mockResourcesDBClient,
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
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
