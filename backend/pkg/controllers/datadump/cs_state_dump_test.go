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

package datadump

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/metadataapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestCSStateDump_SyncOnce(t *testing.T) {
	tests := []struct {
		name            string
		createCluster   bool
		createNodePools []*coreapi.NodePool
		setupCSClient   func(*ocm.MockClusterServiceClientSpec, metadataapi.InternalID)
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
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator(nil, nil))
			},
			wantErr: false,
		},
		{
			name:          "CS client errors are logged but do not fail",
			createCluster: true,
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(nil, fmt.Errorf("connection error"))
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator(nil, nil))
			},
			wantErr: false,
		},
		{
			name:          "success dumps cluster and node pool data",
			createCluster: true,
			createNodePools: []*coreapi.NodePool{
				newTestNodePool("test-np-1", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np1"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				csNodePool, _ := arohcpv1alpha1.NewNodePool().
					ID("np1").
					HREF("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np1").
					Replicas(3).
					Build()
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator([]*arohcpv1alpha1.NodePool{csNodePool}, nil))
			},
			wantErr: false,
		},
		{
			name:          "node pool without ClusterServiceID is skipped",
			createCluster: true,
			createNodePools: []*coreapi.NodePool{
				newTestNodePool("test-np-no-csid", ""),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				// The node pool has no Cluster Service id, so the list is empty and nothing is dumped.
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator(nil, nil))
			},
			wantErr: false,
		},
		{
			name:          "node pool missing from cluster-service list is skipped",
			createCluster: true,
			createNodePools: []*coreapi.NodePool{
				newTestNodePool("test-np-missing", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-missing"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator(nil, nil))
			},
			wantErr: false,
		},
		{
			name:          "node pool CS ListNodePools error is logged but does not fail",
			createCluster: true,
			createNodePools: []*coreapi.NodePool{
				newTestNodePool("test-np-err", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-err"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator(nil, fmt.Errorf("node pool connection error")))
			},
			wantErr: false,
		},
		{
			name:          "multiple node pools are all dumped",
			createCluster: true,
			createNodePools: []*coreapi.NodePool{
				newTestNodePool("test-np-a", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-a"),
				newTestNodePool("test-np-b", "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-b"),
			},
			setupCSClient: func(mock *ocm.MockClusterServiceClientSpec, csID metadataapi.InternalID) {
				csCluster, _ := arohcpv1alpha1.NewCluster().
					ID("11111111111111111111111111111111").
					State(arohcpv1alpha1.ClusterStateReady).
					Build()
				mock.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)

				csNodePoolA, _ := arohcpv1alpha1.NewNodePool().
					ID("np-a").
					HREF("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-a").
					Replicas(2).
					Build()
				csNodePoolB, _ := arohcpv1alpha1.NewNodePool().
					ID("np-b").
					HREF("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-b").
					Replicas(5).
					Build()
				mock.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator([]*arohcpv1alpha1.NodePool{csNodePoolA, csNodePoolB}, nil))
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ctx := context.Background()

			mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			syncer := &csStateDump{
				clusterLister:   &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
				nodePoolLister:  &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
				csClient:        mockCSClient,
				nextDumpChecker: &alwaysSyncCooldownChecker{},
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    "test-sub",
				ResourceGroupName: "test-rg",
				HCPClusterName:    "test-cluster",
			}

			csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))

			if tt.createCluster {
				clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
				cluster := &coreapi.Cluster{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   clusterResourceID,
						PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
					},
					TrackedResource: coreapi.TrackedResource{
						Resource: coreapi.Resource{ID: clusterResourceID},
					},
					ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
						ClusterServiceID: &csID,
					},
				}

				clustersCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
				_, err := clustersCRUD.Create(ctx, cluster, nil)
				require.NoError(t, err)
			}

			for _, np := range tt.createNodePools {
				nodePoolsCRUD := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
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

func newTestNodePool(name, clusterServiceIDStr string) *coreapi.NodePool {
	nodePoolResourceID := metadataapi.Must(azcorearm.ParseResourceID(
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/" + name))
	np := &coreapi.NodePool{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: nodePoolResourceID, PartitionKey: strings.ToLower(nodePoolResourceID.SubscriptionID)},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{
				ID:   nodePoolResourceID,
				Name: name,
				Type: coreapi.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
	}
	if clusterServiceIDStr != "" {
		np.ServiceProviderProperties = coreapi.NodePoolServiceProviderProperties{
			ClusterServiceID: metadataapihelpers.Ptr(metadataapi.Must(metadataapi.NewInternalID(clusterServiceIDStr))),
		}
	}
	return np
}

func TestCSStateDump_SyncOnce_DumpsListedNodePoolVersion(t *testing.T) {
	ctrl := gomock.NewController(t)

	var logs []string
	logger := funcr.New(func(prefix, args string) {
		logs = append(logs, args)
	}, funcr.Options{})
	ctx := utils.ContextWithLogger(context.Background(), logger)

	mockResourcesDBClient := corecosmosstoragetesting.NewMockResourcesDBClient()
	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	syncer := &csStateDump{
		clusterLister:   &corelistertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient},
		nodePoolLister:  &corelistertesting.DBNodePoolLister{ResourcesDBClient: mockResourcesDBClient},
		csClient:        mockCSClient,
		nextDumpChecker: &alwaysSyncCooldownChecker{},
	}

	key := controllerutils.HCPClusterKey{
		SubscriptionID:    "test-sub",
		ResourceGroupName: "test-rg",
		HCPClusterName:    "test-cluster",
	}
	csID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111"))
	npCSID := "/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np1"

	clusterResourceID := metadataapi.Must(azcorearm.ParseResourceID("/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster"))
	_, err := mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Create(ctx, &coreapi.Cluster{
		CosmosMetadata: coreapi.CosmosMetadata{
			ResourceID:   clusterResourceID,
			PartitionKey: strings.ToLower(clusterResourceID.SubscriptionID),
		},
		TrackedResource: coreapi.TrackedResource{
			Resource: coreapi.Resource{ID: clusterResourceID},
		},
		ServiceProviderProperties: coreapi.ClusterServiceProviderProperties{
			ClusterServiceID: &csID,
		},
	}, nil)
	require.NoError(t, err, "failed to create test cluster")

	nodePool := newTestNodePool("test-np-1", npCSID)
	_, err = mockResourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err, "failed to create test node pool")

	version, err := arohcpv1alpha1.NewVersion().
		ID("openshift-v4.18.0").
		HREF("/api/aro_hcp/v1alpha1/versions/openshift-v4.18.0").
		RawID("4.18.0").
		ChannelGroup("stable").
		Enabled(true).
		Build()
	require.NoError(t, err, "failed to build node pool version")
	csNodePool, err := arohcpv1alpha1.NewNodePool().
		ID("np1").
		HREF("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/NP1").
		Replicas(3).
		Version(arohcpv1alpha1.NewVersion().Copy(version)).
		Build()
	require.NoError(t, err, "failed to build cluster-service node pool")
	unregistered, err := arohcpv1alpha1.NewNodePool().
		ID("np-extra").
		HREF("/api/aro_hcp/v1alpha1/clusters/11111111111111111111111111111111/node_pools/np-extra").
		Build()
	require.NoError(t, err, "failed to build unregistered cluster-service node pool")

	csCluster, err := arohcpv1alpha1.NewCluster().
		ID("11111111111111111111111111111111").
		State(arohcpv1alpha1.ClusterStateReady).
		Build()
	require.NoError(t, err, "failed to build cluster-service cluster")
	mockCSClient.EXPECT().GetCluster(gomock.Any(), csID).Return(csCluster, nil)
	mockCSClient.EXPECT().ListNodePools(csID, "").Return(ocm.NewSimpleNodePoolListIterator([]*arohcpv1alpha1.NodePool{csNodePool, unregistered}, nil))

	require.NoError(t, syncer.SyncOnce(ctx, key), "state dump sync failed")

	var nodePoolDumps []string
	for _, line := range logs {
		if strings.Contains(line, "cluster-service node pool state dump") {
			nodePoolDumps = append(nodePoolDumps, line)
		}
	}
	require.Len(t, nodePoolDumps, 1, "only the Cosmos node pool should be dumped")
	nodePoolDump := nodePoolDumps[0]
	assert.NotContains(t, nodePoolDump, "np-extra", "unregistered cluster-service node pool was dumped")
	assert.Contains(t, nodePoolDump, npCSID, "dump is missing the node pool cluster-service id")
	assert.Contains(t, nodePoolDump, "test-np-1", "dump is missing the ARM node pool name")
	assert.Contains(t, nodePoolDump, "4.18.0", "dump is missing the embedded node pool version")
	assert.NotContains(t, nodePoolDump, "VersionLink", "dump followed a version link instead of embedding the version")
}

func TestListNodePoolsByID(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	syncer := &csStateDump{csClient: mockCSClient}
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/abc"))

	withHref, err := arohcpv1alpha1.NewNodePool().
		ID("NP1").
		HREF("/api/aro_hcp/v1alpha1/clusters/abc/node_pools/NP1").
		Build()
	require.NoError(t, err, "failed to build node pool with href")
	withoutHref, err := arohcpv1alpha1.NewNodePool().ID("NP2").Build()
	require.NoError(t, err, "failed to build node pool without href")
	withoutID, err := arohcpv1alpha1.NewNodePool().Build()
	require.NoError(t, err, "failed to build node pool without id")

	mockCSClient.EXPECT().ListNodePools(clusterID, "").Return(ocm.NewSimpleNodePoolListIterator(
		[]*arohcpv1alpha1.NodePool{withHref, nil, withoutHref, withoutID},
		nil,
	))

	got, err := syncer.listNodePoolsByID(context.Background(), clusterID)
	require.NoError(t, err, "listing node pools failed")
	assert.Equal(t, map[string]*arohcpv1alpha1.NodePool{
		"/api/aro_hcp/v1alpha1/clusters/abc/node_pools/np1": withHref,
		"/api/aro_hcp/v1alpha1/clusters/abc/node_pools/np2": withoutHref,
	}, got, "node pools should be indexed by lowercased cluster-service id")

	legacyClusterID := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/abc"))
	legacyWithoutHref, err := arohcpv1alpha1.NewNodePool().ID("NP2").Build()
	require.NoError(t, err, "failed to build node pool for a legacy cluster id")
	mockCSClient.EXPECT().ListNodePools(legacyClusterID, "").Return(ocm.NewSimpleNodePoolListIterator(
		[]*arohcpv1alpha1.NodePool{legacyWithoutHref},
		nil,
	))

	got, err = syncer.listNodePoolsByID(context.Background(), legacyClusterID)
	require.NoError(t, err, "listing node pools for a legacy cluster id failed")
	assert.Equal(t, map[string]*arohcpv1alpha1.NodePool{
		"/api/aro_hcp/v1alpha1/clusters/abc/node_pools/np2": legacyWithoutHref,
	}, got, "a missing href should use the ARO-HCP node pool path")
}

func TestListNodePoolsByID_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
	syncer := &csStateDump{csClient: mockCSClient}
	clusterID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/abc"))

	mockCSClient.EXPECT().ListNodePools(clusterID, "").Return(
		ocm.NewSimpleNodePoolListIterator(nil, fmt.Errorf("node pool connection error")),
	)

	got, err := syncer.listNodePoolsByID(context.Background(), clusterID)
	assert.Error(t, err, "list error should be returned")
	assert.Nil(t, got, "node pool map should be empty when the list fails")
}

func TestCSStateDump_SyncOnce_CooldownPreventsSync(t *testing.T) {
	syncer := &csStateDump{
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
