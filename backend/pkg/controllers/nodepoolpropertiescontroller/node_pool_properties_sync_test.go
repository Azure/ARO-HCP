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

package nodepoolpropertiescontroller

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/api/equality"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

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

func TestNodePoolPropertiesSyncer_SyncOnce(t *testing.T) {
	testCases := []struct {
		name              string
		existingNodePool  *api.HCPOpenShiftClusterNodePool
		existingCluster   *api.HCPOpenShiftCluster
		cacheNodePool     *api.HCPOpenShiftClusterNodePool // when set, lister uses this instead of existingNodePool (e.g. stale cache)
		csNodePool        *arohcpv1alpha1.NodePool
		expectGetNodePool bool
		wantNodePool      *api.HCPOpenShiftClusterNodePool
		getNodePoolErr    error
		wantErr           bool
		wantErrContain    string
	}{
		{
			name:            "short-circuit when version is valid semver and channel group set",
			existingCluster: newTestCluster(t),
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			expectGetNodePool: false,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
		},
		{
			name:            "sync channel group from CS when version is valid semver and channel group empty",
			existingCluster: newTestCluster(t),
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
			}),
			csNodePool:        newCSNodePoolWithVersion(t, "4.21.5", "stable"),
			expectGetNodePool: true,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
		},
		{
			name:              "sync version from CS when version ID is empty",
			existingCluster:   newTestCluster(t),
			existingNodePool:  newTestNodePool(t, nil),
			csNodePool:        newCSNodePoolWithVersion(t, "4.21.5", "stable"),
			expectGetNodePool: true,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
		},
		{
			name:              "sync version from CS when version ID is invalid",
			existingCluster:   newTestCluster(t),
			existingNodePool:  newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) { np.Properties.Version.ID = "4.20" }),
			csNodePool:        newCSNodePoolWithVersion(t, "4.20.16", "stable"),
			expectGetNodePool: true,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.20.16"
				np.Properties.Version.ChannelGroup = "stable"
			}),
		},
		{
			name:              "node pool not found",
			existingCluster:   newTestCluster(t),
			existingNodePool:  nil,
			expectGetNodePool: false,
		},
		{
			name:            "no ClusterServiceID skips CS call",
			existingCluster: newTestCluster(t),
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = ""
				np.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
			}),
			expectGetNodePool: false,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = ""
				np.ServiceProviderProperties.ClusterServiceID = api.InternalID{}
			}),
		},
		{
			name:              "GetNodePool error",
			existingCluster:   newTestCluster(t),
			existingNodePool:  newTestNodePool(t, nil),
			expectGetNodePool: true,
			getNodePoolErr:    errors.New("cs error"),
			wantErr:           true,
			wantErrContain:    "failed to get node pool from Cluster Service",
		},
		{
			name:            "when cache indicates needs work but Cosmos already has properties, skip Cluster Service",
			existingCluster: newTestCluster(t),
			// Cosmos has the node pool with version already set (no work needed).
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			expectGetNodePool: false,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			// Stale cache (empty version); Cosmos is up to date so we skip Cluster Service.
			cacheNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = ""
			}),
		},
		{
			name:            "Cosmos version and channel correct; CS RawID behind desired (upgrade in progress) — do not overwrite",
			existingCluster: newTestCluster(t),
			// During upgrades, desired version in Cosmos can be ahead of Cluster Service's reported RawID until CS catches up; we must not pull CS and overwrite.
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.6"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			csNodePool:        newCSNodePoolWithVersion(t, "4.21.5", "stable"),
			expectGetNodePool: false,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.6"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			cacheNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = ""
			}),
		},
		{
			name:            "Cosmos version and channel correct; CS channel group differs — do not overwrite",
			existingCluster: newTestCluster(t),
			existingNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			csNodePool:        newCSNodePoolWithVersion(t, "4.21.5", "candidate"),
			expectGetNodePool: false,
			wantNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = "stable"
			}),
			// Stale cache: channel empty so we read Cosmos; Cosmos is authoritative once both fields are set.
			cacheNodePool: newTestNodePool(t, func(np *api.HCPOpenShiftClusterNodePool) {
				np.Properties.Version.ID = "4.21.5"
				np.Properties.Version.ChannelGroup = ""
			}),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{tc.existingCluster}
			if tc.existingNodePool != nil {
				resources = append(resources, tc.existingNodePool)
			}
			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, resources)
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
			if tc.cacheNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.cacheNodePool)
			} else if tc.existingNodePool != nil {
				nodePoolsForLister = append(nodePoolsForLister, tc.existingNodePool)
			}
			syncer := &nodePoolPropertiesSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				nodePoolLister:       &listertesting.SliceNodePoolLister{NodePools: nodePoolsForLister},
				cosmosClient:         mockDB,
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

			if tc.wantNodePool != nil {
				updated, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
					NodePools(testClusterName).Get(ctx, testNodePoolName)
				require.NoError(t, err)
				require.True(t, equality.Semantic.DeepEqual(tc.wantNodePool.Properties, updated.Properties), "updated node pool properties do not match expected")
			}
		})
	}
}

func newTestCluster(t *testing.T) *api.HCPOpenShiftCluster {
	t.Helper()
	resourceID := api.Must(azcorearm.ParseResourceID(
		"/subscriptions/" + testSubscriptionID +
			"/resourceGroups/" + testResourceGroupName +
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID := api.Must(api.NewInternalID(testClusterServiceIDStr))
	return &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   resourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ClusterServiceID: clusterInternalID,
		},
	}
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
			Version: api.NodePoolVersionProfile{},
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

func newCSNodePoolWithVersion(t *testing.T, versionID, channelGroup string) *arohcpv1alpha1.NodePool {
	t.Helper()
	np, err := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName).
		Version(arohcpv1alpha1.NewVersion().RawID(versionID).ChannelGroup(channelGroup)).
		Build()
	require.NoError(t, err)
	return np
}
