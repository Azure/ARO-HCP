// Copyright 2025 Microsoft Corporation
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

package datadumpcontrollers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func TestCSStateDump_SyncOnce(t *testing.T) {
	tests := []struct {
		name            string
		createCluster   bool
		createNodePools []*api.HCPOpenShiftClusterNodePool
		setupCSClient   func(*ocm.MockClusterServiceClientSpec, api.InternalID)
		wantErr         bool
	}{
		{
			name:          "cluster not found in DB returns nil",
			createCluster: false,
			wantErr:       false,
		},
		{
			name:          "success logs cluster data with no node pools",
			createCluster: true,
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
			},
			wantErr: false,
		},
		{
			name:          "CS client errors are logged but do not fail",
			createCluster: true,
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(nil, fmt.Errorf("connection error"))
			},
			wantErr: false,
		},
		{
			name:          "success dumps cluster and node pool data",
			createCluster: true,
			createNodePools: []*api.HCPOpenShiftClusterNodePool{
				newTestNodePool("test-np-1", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np1"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				csNodePool, _ := arohcpv1alpha1.NewNodePool().
					ID("np1").
					Replicas(3).
					Build()
				npCSID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np1"))
				mock.EXPECT().GetNodePool(gomock.Any(), npCSID).Return(csNodePool, nil)
			},
			wantErr: false,
		},
		{
			name:          "node pool without ClusterServiceID is skipped",
			createCluster: true,
			createNodePools: []*api.HCPOpenShiftClusterNodePool{
				newTestNodePool("test-np-no-csid", ""),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				// No GetNodePool call expected because ClusterServiceID is empty
			},
			wantErr: false,
		},
		{
			name:          "node pool CS GetNodePool error is logged but does not fail",
			createCluster: true,
			createNodePools: []*api.HCPOpenShiftClusterNodePool{
				newTestNodePool("test-np-err", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-err"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				npCSID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-err"))
				mock.EXPECT().GetNodePool(gomock.Any(), npCSID).Return(nil, fmt.Errorf("node pool connection error"))
			},
			wantErr: false,
		},
		{
			name:          "multiple node pools are all dumped",
			createCluster: true,
			createNodePools: []*api.HCPOpenShiftClusterNodePool{
				newTestNodePool("test-np-a", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-a"),
				newTestNodePool("test-np-b", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-b"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID api.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)

				csNodePoolA, _ := arohcpv1alpha1.NewNodePool().ID("np-a").Replicas(2).Build()
				npCSIDA := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-a"))
				mock.EXPECT().GetNodePool(gomock.Any(), npCSIDA).Return(csNodePoolA, nil)

				csNodePoolB, _ := arohcpv1alpha1.NewNodePool().ID("np-b").Replicas(5).Build()
				npCSIDB := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-b"))
				mock.EXPECT().GetNodePool(gomock.Any(), npCSIDB).Return(csNodePoolB, nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ctx := context.Background()

			mockDBClient := databasetesting.NewMockDBClient()
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			syncer := &csStateDump{
				cooldownChecker: &alwaysSyncCooldownChecker{},
				cosmosClient:    mockDBClient,
				csClient:        mockCSClient,
				nextDumpChecker: &alwaysSyncCooldownChecker{},
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    "test-sub",
				ResourceGroupName: "test-rg",
				HCPClusterName:    "test-cluster",
			}

			csID := api.Must(api.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))

			if tt.createCluster {
				clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
				cluster := &api.HCPOpenShiftCluster{
					TrackedResource: arm.TrackedResource{
						Resource: arm.Resource{ID: clusterResourceID},
					},
					ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
						ClusterServiceID: csID,
					},
				}

				clustersCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
				_, err := clustersCRUD.Create(ctx, cluster, nil)
				require.NoError(t, err)
			}

			for _, np := range tt.createNodePools {
				nodePoolsCRUD := mockDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
				_, err := nodePoolsCRUD.Create(ctx, np, nil)
				require.NoError(t, err)
			}

			if tt.setupCSClient != nil {
				tt.setupCSClient(mockCSClient, csID)
			}

			err := syncer.SyncOnce(ctx, key)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func newTestNodePool(name, clusterServiceIDStr string) *api.HCPOpenShiftClusterNodePool {
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/" + name))
	np := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodePoolResourceID,
				Name: name,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
	if clusterServiceIDStr != "" {
		np.ServiceProviderProperties = api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: api.Must(api.NewInternalID(clusterServiceIDStr)),
		}
	}
	return np
}

func TestCSStateDump_SyncOnce_CooldownPreventsSync(t *testing.T) {
	syncer := &csStateDump{
		cooldownChecker: &alwaysSyncCooldownChecker{},
		nextDumpChecker: &neverSyncCooldownChecker{},
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}

	err := syncer.SyncOnce(context.Background(), key)
	assert.NoError(t, err)
}

func TestCsObjectToMap(t *testing.T) {
	csCluster, _ := arohcpv1alpha1.NewCluster().
		ID("test-cluster-id").
		State(arohcpv1alpha1.ClusterStateReady).
		Build()

	tests := []struct {
		name    string
		input   any
		wantNil bool
		wantErr bool
	}{
		{
			name:    "nil input",
			input:   nil,
			wantNil: true,
			wantErr: false,
		},
		{
			name:    "cluster service cluster",
			input:   csCluster,
			wantNil: false,
			wantErr: false,
		},
		{
			name:    "unsupported type returns error",
			input:   struct{ Name string }{Name: "test"},
			wantNil: true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := csObjectToMap(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.wantNil {
				assert.Nil(t, result)
			} else if !tt.wantErr {
				assert.NotNil(t, result)
			}
		})
	}
}

// alwaysSyncCooldownChecker is a simple mock implementation of CooldownChecker
type alwaysSyncCooldownChecker struct{}

func (m *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

// neverSyncCooldownChecker is a mock that never allows sync
type neverSyncCooldownChecker struct{}

func (m *neverSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return false
}
