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

package upgradecontrollers

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testNodePoolName      = "test-nodepool"
	testCSClusterID       = "/api/clusters_mgmt/v1/clusters/abc123"
	testCSNodePoolID      = "/api/clusters_mgmt/v1/clusters/abc123/node_pools/np456"
	testVersionID         = "4.14.0"
)

// alwaysAllowCooldown is a test helper that always allows sync.
type alwaysAllowCooldown struct{}

func (a *alwaysAllowCooldown) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutils.CooldownChecker = &alwaysAllowCooldown{}

func mustParseResourceID(t *testing.T, id string) *azcorearm.ResourceID {
	t.Helper()
	rid, err := azcorearm.ParseResourceID(id)
	require.NoError(t, err)
	return rid
}

// instantiateTestNodePool creates a parent cluster and a node pool in the mock database.
func instantiateTestNodePool(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
	t.Helper()

	// Create parent cluster first (required by mock DB structure).
	clusterResourceID := mustParseResourceID(t,
		"/subscriptions/"+testSubscriptionID+
			"/resourceGroups/"+testResourceGroupName+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+testClusterName)
	clusterInternalID, err := api.NewInternalID(testCSClusterID)
	require.NoError(t, err)

	cluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   clusterResourceID,
				Name: testClusterName,
				Type: api.ClusterResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterServiceProviderProperties{
			ProvisioningState: arm.ProvisioningStateSucceeded,
			ClusterServiceID:  clusterInternalID,
		},
	}
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
	require.NoError(t, err)

	// Create node pool.
	nodePoolResourceID := mustParseResourceID(t,
		"/subscriptions/"+testSubscriptionID+
			"/resourceGroups/"+testResourceGroupName+
			"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/"+testClusterName+
			"/nodePools/"+testNodePoolName)
	nodePoolInternalID, err := api.NewInternalID(testCSNodePoolID)
	require.NoError(t, err)

	nodePool := &api.HCPOpenShiftClusterNodePool{
		TrackedResource: arm.TrackedResource{
			Resource: arm.Resource{
				ID:   nodePoolResourceID,
				Name: testNodePoolName,
				Type: api.NodePoolResourceType.String(),
			},
			Location: "eastus",
		},
		ServiceProviderProperties: api.HCPOpenShiftClusterNodePoolServiceProviderProperties{
			ClusterServiceID: nodePoolInternalID,
		},
	}
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Create(ctx, nodePool, nil)
	require.NoError(t, err)
}

// newCSNodePool creates a Cluster Service node pool for testing.
func newCSNodePool(t *testing.T, withVersion bool) *arohcpv1alpha1.NodePool {
	t.Helper()

	builder := arohcpv1alpha1.NewNodePool().
		ID("np456").
		HREF(testCSNodePoolID)

	if withVersion {
		builder = builder.Version(arohcpv1alpha1.NewVersion().ID(testVersionID))
	}

	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}

// syncerTestCase defines a test case for the dataPlaneVersionSyncer.
type syncerTestCase struct {
	name string

	// seedDB populates the mock database with initial items.
	seedDB func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)

	// setupCSMock configures the Cluster Service mock expectations.
	setupCSMock func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec)

	// expectError indicates whether SyncOnce should return an error.
	expectError bool
}

func TestDataPlaneVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	testCases := []syncerTestCase{
		{
			name: "successful sync logs version",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			setupCSMock: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csNodePool := newCSNodePool(t, true)
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectError: false,
		},
		{
			name: "cosmos not found returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				// Don't seed any node pool - Get will fail with not found.
			},
			setupCSMock: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				// No CS mock setup needed - we won't reach CS call.
			},
			expectError: true,
		},
		{
			name: "cluster service error logs and returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			setupCSMock: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("cluster service unavailable")).
					Times(1)
			},
			expectError: false, // Controller logs error but returns nil
		},
		{
			name: "missing version logs and returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			setupCSMock: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csNodePool := newCSNodePool(t, false) // No version
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectError: false, // Controller logs error but returns nil
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDB := databasetesting.NewMockDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			tc.seedDB(t, ctx, mockDB)
			tc.setupCSMock(t, mockCS)

			syncer := &dataPlaneVersionSyncer{
				cooldownChecker:      &alwaysAllowCooldown{},
				cosmosClient:         mockDB,
				clusterServiceClient: mockCS,
			}

			// Create a context with a no-op logger for tests.
			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			err := syncer.SyncOnce(ctx, testKey)

			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDataPlaneVersionSyncer_CooldownChecker(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	cooldownChecker := &alwaysAllowCooldown{}

	syncer := &dataPlaneVersionSyncer{
		cooldownChecker:      cooldownChecker,
		cosmosClient:         mockDB,
		clusterServiceClient: mockCS,
	}

	require.Same(t, cooldownChecker, syncer.CooldownChecker(),
		"CooldownChecker() should return the configured cooldown checker")
}
