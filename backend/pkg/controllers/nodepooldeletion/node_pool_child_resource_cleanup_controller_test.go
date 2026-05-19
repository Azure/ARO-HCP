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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func newTestManagementClusterContent(t *testing.T, name string) *api.ManagementClusterContent {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/managementClusterContents/" + name))
	return &api.ManagementClusterContent{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
	}
}

func newTestNodePoolController(t *testing.T, name string) *api.Controller {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName +
			"/hcpOpenShiftControllers/" + name))
	return &api.Controller{
		CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID},
		ExternalID: api.Must(azcorearm.ParseResourceID(
			"/subscriptions/" + testSubscriptionID +
				"/resourceGroups/" + testResourceGroupName +
				"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
				"/nodePools/" + testNodePoolName)),
		Status: api.ControllerStatus{
			Conditions: []metav1.Condition{},
		},
	}
}

func TestNodePoolChildResourceCleanupController_SyncOnce(t *testing.T) {
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	readyToDelete := func(np *api.HCPOpenShiftClusterNodePool) {
		np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
		np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
		np.ServiceProviderProperties.ClusterServiceID = nil
	}

	testCases := []struct {
		name               string
		existingNodePool   *api.HCPOpenShiftClusterNodePool
		childResources     []any
		wantChildrenGone   bool
		wantControllerKept bool
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
			name:             "all conditions met, no children -- no-op",
			existingNodePool: newTestNodePool(t, readyToDelete),
		},
		{
			name:             "all conditions met, MCC child exists -- deletes it",
			existingNodePool: newTestNodePool(t, readyToDelete),
			childResources:   []any{newTestManagementClusterContent(t, "test-mcc")},
			wantChildrenGone: true,
		},
		{
			name:               "all conditions met, controller child exists -- skipped",
			existingNodePool:   newTestNodePool(t, readyToDelete),
			childResources:     []any{newTestNodePoolController(t, "test-controller")},
			wantControllerKept: true,
		},
		{
			name:               "all conditions met, mixed children -- deletes only non-controller",
			existingNodePool:   newTestNodePool(t, readyToDelete),
			childResources:     []any{newTestManagementClusterContent(t, "test-mcc"), newTestNodePoolController(t, "test-controller")},
			wantChildrenGone:   true,
			wantControllerKept: true,
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

			syncer := &nodePoolChildResourceCleanupController{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				nodePoolLister:    &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				resourcesDBClient: mockResourcesDBClient,
			}

			key := controllerutils.HCPNodePoolKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				HCPNodePoolName:   testNodePoolName,
			}

			err = syncer.SyncOnce(ctx, key)
			require.NoError(t, err)

			if tc.wantChildrenGone || tc.wantControllerKept {
				nodePoolResourceID := key.GetResourceID()
				untypedCRUD, err := mockResourcesDBClient.UntypedCRUD(*nodePoolResourceID)
				require.NoError(t, err)
				childIterator, err := untypedCRUD.List(ctx, nil)
				require.NoError(t, err)

				var remainingCount int
				var controllerCount int
				for _, child := range childIterator.Items(ctx) {
					remainingCount++
					if strings.EqualFold(child.ResourceType, api.NodePoolControllerResourceType.String()) {
						controllerCount++
					}
				}
				require.NoError(t, childIterator.GetError())

				if tc.wantChildrenGone && tc.wantControllerKept {
					assert.Equal(t, 1, remainingCount, "expected only controller child to remain")
					assert.Equal(t, 1, controllerCount, "expected the remaining child to be a controller")
				} else if tc.wantChildrenGone {
					assert.Equal(t, 0, remainingCount, "expected no children to remain")
				} else if tc.wantControllerKept {
					assert.Equal(t, 1, controllerCount, "expected controller child to remain")
				}
			}
		})
	}
}
