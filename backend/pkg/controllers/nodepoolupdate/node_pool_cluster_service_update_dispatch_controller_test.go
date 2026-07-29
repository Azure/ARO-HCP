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

package nodepoolupdate

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID      = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName   = "test-rg"
	testClusterName         = "test-cluster"
	testNodePoolName        = "test-nodepool"
	testClusterServiceIDStr = "/api/aro_hcp/v1alpha1/clusters/abc123"
	testNodePoolCSIDStr     = testClusterServiceIDStr + "/node_pools/" + testNodePoolName
)

func TestNodePoolUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	csID := api.Must(api.NewInternalID(testNodePoolCSIDStr))

	defaultExistingCSNodePool := api.Must(arohcpv1alpha1.NewNodePool().Build())

	newNodePoolWithConfigDiff := func() *api.HCPOpenShiftClusterNodePool {
		return newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
			np.Properties.Replicas = 5
			np.Properties.Labels = map[string]string{"env": "prod"}
		})
	}

	newFakeOCMParentClusterNotUpdatableError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("Node pools can only be updated on clusters in an updatable state. The cluster requested is in 'installing' state.").
			Build()
		return e
	}

	newFakeOCMNodePoolNotUpdatableError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("Node pool can only be updated in 'ready' state. the node pool requested is in 'installing' state.").
			Build()
		return e
	}

	newFakeOCMUnrelatedBadRequestError := func() error {
		e, _ := ocmerrors.NewError().
			Status(http.StatusBadRequest).
			Reason("some other validation error").
			Build()
		return e
	}

	testCases := []struct {
		name               string
		existingNodePool   *api.HCPOpenShiftClusterNodePool
		existingCSNodePool *arohcpv1alpha1.NodePool
		// When not set, the syncer uses a node pool lister backed by the seeded Cosmos resources.
		nodePoolLister                      listers.NodePoolLister
		setupMockCSClient                   func(mock *ocm.MockClusterServiceClientSpec)
		minimumReconcileTimeCooldownChecker controllerutil.CooldownChecker
		wantErr                             bool
		wantErrContain                      string
	}{
		{
			name: "skip without CS call when no CSID",
			existingNodePool: newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.ClusterServiceID = nil
				np.Properties.Replicas = 5
			}),
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:               "dispatches CS calls when config differs",
			existingNodePool:   newNodePoolWithConfigDiff(),
			existingCSNodePool: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name: "no-op when config matches",
			existingNodePool: newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Replicas = 2
			}),
			existingCSNodePool: mustBuildCSNodePoolFromRP(t, newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Replicas = 2
			})),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(mustBuildCSNodePoolFromRP(t, newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
						np.Properties.Replicas = 2
					})), nil)
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:               "when CS update returns parent cluster not updatable no error is returned",
			existingNodePool:   newNodePoolWithConfigDiff(),
			existingCSNodePool: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMParentClusterNotUpdatableError())
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:               "when CS update returns node pool not updatable no error is returned",
			existingNodePool:   newNodePoolWithConfigDiff(),
			existingCSNodePool: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMNodePoolNotUpdatableError())
			},
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:               "when CS update returns unhandled error error is propagated",
			existingNodePool:   newNodePoolWithConfigDiff(),
			existingCSNodePool: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, errors.New("boom"))
			},
			wantErr:                             true,
			wantErrContain:                      "failed to update cluster-service NodePool",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:               "when CS update returns unrelated bad request error error is propagated",
			existingNodePool:   newNodePoolWithConfigDiff(),
			existingCSNodePool: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMUnrelatedBadRequestError())
			},
			wantErr:                             true,
			wantErrContain:                      "failed to update cluster-service NodePool",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:             "when CS GetNodePool fails error is propagated",
			existingNodePool: newNodePoolWithConfigDiff(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(nil, errors.New("boom"))
			},
			wantErr:                             true,
			wantErrContain:                      "failed to get node pool from Cluster Service",
			minimumReconcileTimeCooldownChecker: &alwaysSyncCooldownChecker{},
		},
		{
			name:                                "minimum reconcile cooldown prevents sync",
			existingNodePool:                    newTestNodePool(),
			minimumReconcileTimeCooldownChecker: &neverSyncCooldownChecker{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			seedResources := []any{}
			if tc.existingNodePool != nil {
				seedResources = append(seedResources, tc.existingNodePool)
			}

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, seedResources)
			require.NoError(t, err)

			nodePoolLister := tc.nodePoolLister
			if nodePoolLister == nil {
				nodePoolLister = &listertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDB}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			syncer := &nodePoolClusterServiceUpdateDispatchSyncer{
				cooldownChecker:                     &alwaysSyncCooldownChecker{},
				minimumReconcileTimeCooldownChecker: tc.minimumReconcileTimeCooldownChecker,
				nodePoolLister:                      nodePoolLister,
				resourcesDBClient:                   mockResourcesDB,
				clusterServiceClient:                mockCSClient,
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
				if tc.wantErrContain != "" {
					assert.Contains(t, err.Error(), tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNeedsWork(t *testing.T) {
	csID := api.Must(api.NewInternalID(testNodePoolCSIDStr))
	now := metav1.Now()

	tests := []struct {
		name     string
		nodePool *api.HCPOpenShiftClusterNodePool
		want     bool
	}{
		{
			name: "proceed when CSID set",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
					ClusterServiceID: &csID,
				},
			},
			want: true,
		},
		{
			name: "skip when deletion timestamp is set",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
					DeletionTimestamp: &now,
					ClusterServiceID:  &csID,
				},
			},
			want: false,
		},
		{
			name: "skip when no CSID",
			nodePool: &api.HCPOpenShiftClusterNodePool{
				ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, needsWork(tt.nodePool))
		})
	}
}

func mustBuildCSNodePoolFromRP(t *testing.T, hcpNodePool *api.HCPOpenShiftClusterNodePool) *arohcpv1alpha1.NodePool {
	t.Helper()

	builder, err := ocm.BuildCSNodePool(context.Background(), hcpNodePool, true)
	require.NoError(t, err)

	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}

func newTestNodePool(opts ...func(*api.HCPOpenShiftClusterNodePool)) *api.HCPOpenShiftClusterNodePool {
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
			"/nodePools/" + testNodePoolName,
	))

	csID := api.Must(api.NewInternalID(testNodePoolCSIDStr))
	nodePool := &api.HCPOpenShiftClusterNodePool{
		CosmosMetadata: arm.CosmosMetadata{
			ResourceID:   resourceID,
			PartitionKey: strings.ToLower(resourceID.SubscriptionID),
		},
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testNodePoolName,
				Type: resourceID.ResourceType.String(),
			},
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: &csID,
		},
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Replicas: 2,
			Version: api.NodePoolVersionProfile{
				ID:           "4.20.8",
				ChannelGroup: "stable",
			},
			Platform: api.NodePoolPlatformProfile{
				VMSize: "Standard_D4s_v3",
				OSDisk: api.OSDiskProfile{
					SizeGiB:                ptr.To(int32(128)),
					DiskType:               api.OsDiskTypeManaged,
					DiskStorageAccountType: api.DiskStorageAccountTypePremium_LRS,
				},
			},
		},
	}

	for _, opt := range opts {
		opt(nodePool)
	}

	return nodePool
}

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}

type neverSyncCooldownChecker struct{}

func (c *neverSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return false
}
