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
	"strings"
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
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	readyToDelete := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	testCases := []struct {
		name             string
		existingNodePool *api.HCPOpenShiftClusterNodePool
		childResources   []any
		existingSPNP     *api.ServiceProviderNodePool
		wantDeleted      bool
		wantErr          bool
	}{
		{
			name:             "no DeletionTimestamp -- no-op",
			existingNodePool: newTestNodePool(t, nil),
		},
		{
			name: "DeletionTimestamp set but ClusterServiceDeletionTimestamp not -- no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
		},
		{
			name: "ClusterServiceID still set -- no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
		},
		{
			name:             "all conditions met, no children -- deletes nodepool from Cosmos",
			existingNodePool: newTestNodePool(t, readyToDelete),
			wantDeleted:      true,
		},
		{
			name:             "all conditions met, SPNP has remaining bundles -- backs off",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPNP: newTestSPNP(t, api.MaestroBundleReferenceList{
				{Name: "readonly-bundle-1"},
			}),
			wantDeleted: false,
		},
		{
			name:             "all conditions met, SPNP with empty bundles -- backs off until SPNP removed",
			existingNodePool: newTestNodePool(t, readyToDelete),
			existingSPNP:     newTestSPNP(t, nil),
			wantDeleted:      false,
		},
		{
			name:             "all conditions met, MCC child exists -- backs off",
			existingNodePool: newTestNodePool(t, readyToDelete),
			childResources:   []any{newTestManagementClusterContent(t, "test-mcc")},
			wantDeleted:      false,
		},
		{
			name:             "all conditions met, only controller children -- deletes nodepool",
			existingNodePool: newTestNodePool(t, readyToDelete),
			childResources:   []any{newTestNodePoolController(t, "test-controller")},
			wantDeleted:      true,
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
			if tc.existingSPNP != nil {
				resources = append(resources, tc.existingSPNP)
			}
			resources = append(resources, tc.childResources...)
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}
			spnpForLister := []*api.ServiceProviderNodePool{}
			if tc.existingSPNP != nil {
				spnpForLister = append(spnpForLister, tc.existingSPNP)
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
			} else {
				require.NoError(t, err)
			}

			if tc.existingNodePool == nil {
				return
			}

			_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
				NodePools(testClusterName).Get(ctx, testNodePoolName)
			if tc.wantDeleted {
				assert.True(t, database.IsNotFoundError(err), "expected nodepool to be deleted from Cosmos")
			} else {
				require.NoError(t, err, "expected nodepool to still exist in Cosmos")
			}

			if len(tc.childResources) > 0 && !tc.wantDeleted {
				nodePoolResourceID := key.GetResourceID()
				untypedCRUD, childErr := mockResourcesDBClient.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, childErr)
				childIterator, childErr := untypedCRUD.List(ctx, nil)
				require.NoError(t, childErr)
				var nonControllerCount int
				for _, child := range childIterator.Items(ctx) {
					if !strings.EqualFold(child.ResourceType, api.NodePoolControllerResourceType.String()) {
						nonControllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())
				assert.Greater(t, nonControllerCount, 0, "expected non-controller children to still exist")
			}
		})
	}
}
