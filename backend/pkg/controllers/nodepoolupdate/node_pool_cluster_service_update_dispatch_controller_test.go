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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	testSubscriptionID       = "00000000-0000-0000-0000-000000000000"
	testResourceGroupName    = "test-rg"
	testClusterName          = "test-cluster"
	testNodePoolName         = "test-nodepool"
	testNodePoolServiceIDStr = "/api/clusters_mgmt/v1/clusters/abc123/node_pools/np123"
)

func TestNodePoolShouldProceed(t *testing.T) {
	csID := api.Must(api.NewInternalID(testNodePoolServiceIDStr))
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
			assert.Equal(t, tt.want, nodePoolShouldProceed(tt.nodePool))
		})
	}
}

func newFakeOCMParentClusterNotUpdatableError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("Node pools can only be updated on clusters in an updatable state, cluster requested is in 'updating' state.").
		Build()
	return e
}

func newFakeOCMNodePoolNotUpdatableError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("Node pool can only be updated in 'ready' state, the node pool requested is in 'updating' state.").
		Build()
	return e
}

func newFakeOCMUnrelatedBadRequestError() error {
	e, _ := ocmerrors.NewError().
		Status(http.StatusBadRequest).
		Reason("some other validation error").
		Build()
	return e
}

func TestNodePoolUpdateDispatchSyncer_SyncOnce(t *testing.T) {
	csID := api.Must(api.NewInternalID(testNodePoolServiceIDStr))

	defaultExistingCSNodePool := api.Must(arohcpv1alpha1.NewNodePool().Replicas(3).Build())

	newNodePoolWithConfigDiff := func() *api.HCPOpenShiftClusterNodePool {
		return newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
			np.Properties.Replicas = 5
			np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(60))
		})
	}

	testCases := []struct {
		name              string
		nodePool          *api.HCPOpenShiftClusterNodePool
		existingCSNP      *arohcpv1alpha1.NodePool
		setupMockCSClient func(mock *ocm.MockClusterServiceClientSpec)
		wantErr           bool
		wantErrContain    string
	}{
		{
			name: "skip without CS call when no CSID",
			nodePool: newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.ServiceProviderProperties.ClusterServiceID = nil
				np.Properties.Replicas = 3
			}),
		},
		{
			name:         "dispatches CS call when config differs",
			nodePool:     newNodePoolWithConfigDiff(),
			existingCSNP: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, nil)
			},
		},
		{
			name: "no-op when config matches",
			nodePool: newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Replicas = 3
				np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(30))
			}),
			existingCSNP: mustBuildCSNodePoolFromRP(t, newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Replicas = 3
				np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(30))
			})),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(mustBuildCSNodePoolFromRP(t, newTestNodePool(func(np *api.HCPOpenShiftClusterNodePool) {
						np.Properties.Replicas = 3
						np.Properties.NodeDrainTimeoutMinutes = ptr.To(int32(30))
					})), nil)
			},
		},
		{
			name:         "when CS node pool update returns parent cluster not updatable no error is returned",
			nodePool:     newNodePoolWithConfigDiff(),
			existingCSNP: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMParentClusterNotUpdatableError())
			},
		},
		{
			name:         "when CS node pool update returns node pool not updatable no error is returned",
			nodePool:     newNodePoolWithConfigDiff(),
			existingCSNP: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMNodePoolNotUpdatableError())
			},
		},
		{
			name:         "when CS node pool update returns unhandled error error is propagated",
			nodePool:     newNodePoolWithConfigDiff(),
			existingCSNP: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, errors.New("boom"))
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service NodePool",
		},
		{
			name:         "when CS node pool update returns unrelated bad request error error is propagated",
			nodePool:     newNodePoolWithConfigDiff(),
			existingCSNP: defaultExistingCSNodePool,
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetNodePool(gomock.Any(), csID).
					Return(defaultExistingCSNodePool, nil)
				mock.EXPECT().
					UpdateNodePool(gomock.Any(), csID, gomock.Any()).
					Return(nil, newFakeOCMUnrelatedBadRequestError())
			},
			wantErr:        true,
			wantErrContain: "failed to update cluster-service NodePool",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockResourcesDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{tc.nodePool})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			syncer := &nodePoolClusterServiceUpdateDispatchSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       newFakeNodePoolLister(tc.nodePool),
				resourcesDBClient:    mockResourcesDB,
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
				if tc.wantErrContain != "" {
					assert.Contains(t, err.Error(), tc.wantErrContain)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func mustBuildCSNodePoolFromRP(t *testing.T, nodePool *api.HCPOpenShiftClusterNodePool) *arohcpv1alpha1.NodePool {
	t.Helper()

	builder, err := ocm.BuildCSNodePool(context.Background(), nodePool, true)
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

	csID := api.Must(api.NewInternalID(testNodePoolServiceIDStr))
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

type fakeNodePoolLister struct {
	nodePool *api.HCPOpenShiftClusterNodePool
}

func newFakeNodePoolLister(nodePool *api.HCPOpenShiftClusterNodePool) *fakeNodePoolLister {
	return &fakeNodePoolLister{nodePool: nodePool}
}

func (f *fakeNodePoolLister) List(_ context.Context) ([]*api.HCPOpenShiftClusterNodePool, error) {
	return []*api.HCPOpenShiftClusterNodePool{f.nodePool}, nil
}

func (f *fakeNodePoolLister) Get(_ context.Context, _, _, _, _ string) (*api.HCPOpenShiftClusterNodePool, error) {
	return f.nodePool, nil
}

func (f *fakeNodePoolLister) ListForResourceGroup(_ context.Context, _, _ string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	return []*api.HCPOpenShiftClusterNodePool{f.nodePool}, nil
}

func (f *fakeNodePoolLister) ListForCluster(_ context.Context, _, _, _ string) ([]*api.HCPOpenShiftClusterNodePool, error) {
	return []*api.HCPOpenShiftClusterNodePool{f.nodePool}, nil
}
