// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	"github.com/stretchr/testify/assert"
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
	testCSClusterID       = "/api/aro_hcp/v1alpha1/clusters/clusterTest"
	testCSNodePoolID      = "/api/aro_hcp/v1alpha1/clusters/clusterTest/node_pools/nodePoolTest"
	testVersionID         = "4.19.0"
)

// alwaysAllowCooldown is a test helper that always allows sync.
type alwaysAllowCooldown struct{}

func (a *alwaysAllowCooldown) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutils.CooldownChecker = &alwaysAllowCooldown{}

// instantiateTestNodePool creates a parent cluster and a node pool in the mock database.
func instantiateTestNodePool(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
	t.Helper()

	// Create parent cluster first (required by mock DB structure).
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
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
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName))
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
		Properties: api.HCPOpenShiftClusterNodePoolProperties{
			Version: api.NodePoolVersionProfile{
				ID: testVersionID,
			},
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
		ID("nodePoolTest")

	if withVersion {
		builder = builder.Version(arohcpv1alpha1.NewVersion().ID(testVersionID))
	}

	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}

func TestDataPlaneVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
		mockSetup             func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec)
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name: "successful sync persists versions to ServiceProviderNodePool",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			mockSetup: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csNodePool := newCSNodePool(t, true)
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectedError: false,
		},
		{
			name: "nodepool not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				// Don't seed any node pool - Get will fail with not found.
			},
			mockSetup: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				// No CS mock setup needed - we won't reach CS call.
			},
			expectedError: false,
		},
		{
			name: "cluster service error returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			mockSetup: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("cluster service unavailable")).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "cluster service unavailable",
		},
		{
			name: "missing version returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				instantiateTestNodePool(t, ctx, mockDB)
			},
			mockSetup: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csNodePool := newCSNodePool(t, false) // No version
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDB := databasetesting.NewMockDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			tt.seedDB(t, ctx, mockDB)
			tt.mockSetup(t, mockCS)

			syncer := &dataPlaneVersionSyncer{
				cooldownChecker:      &alwaysAllowCooldown{},
				cosmosClient:         mockDB,
				clusterServiceClient: mockCS,
			}

			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			err := syncer.SyncOnce(ctx, testKey)

			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)
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

func TestDataPlaneVersionSyncer_PersistsVersions(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	// Seed the database with a node pool
	instantiateTestNodePool(t, ctx, mockDB)

	// Setup CS mock to return a node pool with version
	csNodePool := newCSNodePool(t, true)
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &dataPlaneVersionSyncer{
		cooldownChecker:      &alwaysAllowCooldown{},
		cosmosClient:         mockDB,
		clusterServiceClient: mockCS,
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err)

	// Verify the ServiceProviderNodePool was created with correct versions
	spnp, err := mockDB.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err, "ServiceProviderNodePool should exist after sync")

	expectedVersion := semver.MustParse(testVersionID)

	// Verify ActiveVersion was persisted (from Cluster Service)
	require.NotNil(t, spnp.Status.NodePoolVersion.ActiveVersion,
		"ActiveVersion should be set")
	require.True(t, expectedVersion.EQ(*spnp.Status.NodePoolVersion.ActiveVersion),
		"ActiveVersion should match CS version %s, got %s", testVersionID, spnp.Status.NodePoolVersion.ActiveVersion)

	// Verify DesiredVersion was persisted (from customer's HCPNodePool)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should be set")
	require.True(t, expectedVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should match customer version %s, got %s", testVersionID, spnp.Spec.NodePoolVersion.DesiredVersion)
}

// assertSyncResult is a helper function that validates the result of SyncOnce
func assertSyncResult(t *testing.T, err error, expectedError bool, expectedErrorContains string) {
	t.Helper()
	if expectedError {
		assert.Error(t, err)
		if expectedErrorContains != "" {
			assert.ErrorContains(t, err, expectedErrorContains)
		}
	} else {
		assert.NoError(t, err)
	}
}
