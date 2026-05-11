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
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

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
	testNodePoolName        = "test-nodepool"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testNodePoolCSIDStr     = testClusterServiceIDStr + "/node_pools/" + testNodePoolName
)

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

func TestNodePoolDeletionClusterServiceDeleter_SyncOnce(t *testing.T) {
	fixedNow := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name                                string
		existingNodePool                    *api.HCPOpenShiftClusterNodePool
		cacheNodePool                       *api.HCPOpenShiftClusterNodePool
		expectCSDelete                      bool
		csDeleteErr                         error
		wantClusterServiceDeletionTimestamp bool // whether the controller should have stamped it
		wantErr                             bool
		wantErrContain                      string
	}{
		{
			name:             "no DeletionTimestamp — no-op",
			existingNodePool: newTestNodePool(t, nil),
		},
		{
			name: "ClusterServiceDeletionTimestamp already set — no-op",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
				np.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Minute)}
			}),
		},
		{
			name: "cache says no DeletionTimestamp — short-circuit before Cosmos read",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Hour)}
			}),
			cacheNodePool: newTestNodePool(t, nil),
		},
		{
			name: "DeletionTimestamp set, no ClusterServiceID, within timeout — wait",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-30 * time.Second)}
				np.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
			}),
		},
		{
			name: "DeletionTimestamp set, no ClusterServiceID, past timeout — give up and stamp",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-3 * time.Minute)}
				np.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
			}),
			wantClusterServiceDeletionTimestamp: true,
		},
		{
			name: "DeletionTimestamp set, ClusterServiceID present — issue CS delete and stamp",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Minute)}
			}),
			expectCSDelete:                      true,
			wantClusterServiceDeletionTimestamp: true,
		},
		{
			name: "CS delete returns 404, within timeout — wait, no stamp",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Minute)}
			}),
			expectCSDelete: true,
			csDeleteErr:    fakeOCMNotFoundError(),
		},
		{
			name: "CS delete returns 404, past timeout — stamp",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-3 * time.Minute)}
			}),
			expectCSDelete:                      true,
			csDeleteErr:                         fakeOCMNotFoundError(),
			wantClusterServiceDeletionTimestamp: true,
		},
		{
			name: "CS delete returns transient error — propagated, no stamp",
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: fixedNow.Add(-time.Minute)}
			}),
			expectCSDelete: true,
			csDeleteErr:    errors.New("boom"),
			wantErr:        true,
			wantErrContain: "failed to delete cluster-service NodePool",
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
			if tc.expectCSDelete {
				call := mockCSClient.EXPECT().
					DeleteNodePool(gomock.Any(), api.Must(api.NewInternalID(testNodePoolCSIDStr)))
				if tc.csDeleteErr != nil {
					call.Return(tc.csDeleteErr)
				} else {
					call.Return(nil)
				}
			}

			nodePoolsForLister := []*api.HCPOpenShiftClusterNodePool{}
			switch {
			case tc.cacheNodePool != nil:
				nodePoolsForLister = append(nodePoolsForLister, tc.cacheNodePool)
			case tc.existingNodePool != nil:
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}

			syncer := &nodePoolDeletionClusterServiceDeleter{
				clock:                clocktesting.NewFakePassiveClock(fixedNow),
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

			if tc.existingNodePool != nil {
				stored, err := mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				gotStamp := stored.ServiceProviderProperties.ClusterServiceDeletionTimestamp
				wantStamp := tc.existingNodePool.ServiceProviderProperties.ClusterServiceDeletionTimestamp
				if tc.wantClusterServiceDeletionTimestamp {
					require.NotNil(t, gotStamp, "expected ClusterServiceDeletionTimestamp to be stamped")
					assert.True(t, gotStamp.Time.Equal(fixedNow), "expected ClusterServiceDeletionTimestamp to equal fixedNow; got %v", gotStamp.Time)
				} else if wantStamp == nil {
					assert.Nil(t, gotStamp)
				} else {
					require.NotNil(t, gotStamp)
					assert.True(t, gotStamp.Time.Equal(wantStamp.Time), "expected ClusterServiceDeletionTimestamp unchanged; want %v, got %v", wantStamp.Time, gotStamp.Time)
				}
			}
		})
	}
}

func fakeOCMNotFoundError() error {
	e, _ := ocmerrors.NewError().Status(http.StatusNotFound).Reason("not found").Build()
	return e
}

func newTestNodePool(t *testing.T, opts func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName))
	nodePoolInternalID := api.Must(api.NewInternalID(testNodePoolCSIDStr))
	np := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Platform: api.NodePoolPlatformProfile{
				OSDisk: api.OSDiskProfile{
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
					DiskType:               api.OsDiskTypeManaged,
				},
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nodePoolInternalID,
		},
	}
	if opts != nil {
		opts(np)
	}
	return np
}
