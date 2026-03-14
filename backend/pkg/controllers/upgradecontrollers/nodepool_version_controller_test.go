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
	"github.com/golang/groupcache/lru"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	testSubscriptionID    = "00000000-0000-0000-0000-000000000001"
	testResourceGroupName = "test-rg"
	testClusterName       = "test-cluster"
	testNodePoolName      = "test-nodepool"
	testCSClusterIDStr    = "/api/aro_hcp/v1alpha1/clusters/" + testClusterName
	testCSNodePoolIDStr   = testCSClusterIDStr + "/node_pools/" + testNodePoolName
	testClusterExternalID = "11111111-1111-1111-1111-111111111111"
)

// alwaysSyncCooldownChecker is a test helper that always allows sync.
type alwaysSyncCooldownChecker struct{}

func (a *alwaysSyncCooldownChecker) CanSync(ctx context.Context, key any) bool {
	return true
}

var _ controllerutils.CooldownChecker = &alwaysSyncCooldownChecker{}

// createTestNodePoolWithVersion creates a parent cluster and a node pool in the mock database.
func createTestNodePoolWithVersion(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient, desiredVersion string) {
	t.Helper()

	// Create parent cluster first (required by mock DB structure).
	clusterResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName))
	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
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

	// Create node pool with version
	nodePoolResourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName))
	nodePoolInternalID, err := api.NewInternalID(testCSNodePoolIDStr)
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
				ID: desiredVersion,
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
func newCSNodePool(t *testing.T, version string) *arohcpv1alpha1.NodePool {
	t.Helper()

	builder := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName)

	if version != "" {
		builder = builder.Version(arohcpv1alpha1.NewVersion().RawID(version))
	}

	csNodePool, err := builder.Build()
	require.NoError(t, err)
	return csNodePool
}

// newCSCluster creates a Cluster Service cluster for testing.
func newCSCluster(t *testing.T) *arohcpv1alpha1.Cluster {
	t.Helper()

	csCluster, err := arohcpv1alpha1.NewCluster().
		ID(testClusterName).
		ExternalID(testClusterExternalID).
		Build()
	require.NoError(t, err)
	return csCluster
}

func TestNodePoolVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
		mockCS                func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec)
		expectedError         bool
		expectedErrorContains string
	}{
		{
			name: "nodepool not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				// Don't seed any node pool - Get will fail with not found.
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				// No CS mock setup needed - we won't reach CS call.
			},
			expectedError: false,
		},
		{
			name: "cluster service get cluster call returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				mockCS.EXPECT().
					GetCluster(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("cluster service unavailable")).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "cluster service unavailable",
		},
		{
			name: "cluster service get nodepool call returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csCluster := newCSCluster(t)
				mockCS.EXPECT().
					GetCluster(gomock.Any(), gomock.Any()).
					Return(csCluster, nil).
					Times(1)
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
				createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec) {
				t.Helper()
				csCluster := newCSCluster(t)
				mockCS.EXPECT().
					GetCluster(gomock.Any(), gomock.Any()).
					Return(csCluster, nil).
					Times(1)
				csNodePool := newCSNodePool(t, "") // No version
				mockCS.EXPECT().
					GetNodePool(gomock.Any(), gomock.Any()).
					Return(csNodePool, nil).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "node pool version not found in Cluster Service respons",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)

			mockDB := databasetesting.NewMockDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			tt.seedDB(t, ctx, mockDB)
			tt.mockCS(t, mockCS)

			syncer := &nodePoolVersionSyncer{
				cooldownChecker:           &alwaysSyncCooldownChecker{},
				cosmosClient:              mockDB,
				clusterServiceClient:      mockCS,
				clusterToCincinnatiClient: lru.New(100),
			}

			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			err := syncer.SyncOnce(ctx, testKey)

			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)
		})
	}
}

// assertSyncResult is a helper function that validates the result of SyncOnce
func assertSyncResult(t *testing.T, err error, expectedError bool, expectedErrorContains string) {
	t.Helper()
	if expectedError {
		assert.Error(t, err)
		assert.NotEmpty(t, err, expectedErrorContains)
	} else {
		assert.NoError(t, err)
	}
}

func TestNodePoolVersionSyncer_ValidateDesiredNodePoolVersion(t *testing.T) {
	tests := []struct {
		name                 string
		desiredVersion       string
		activeVersions       []string
		controlPlaneVersions []string
		expectError          bool
		errorContains        string
	}{
		// Control plane constraint tests
		{
			name:                 "desired equals control plane - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "desired less than control plane - pass",
			desiredVersion:       "4.19.5",
			activeVersions:       []string{"4.19.3"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "desired greater than control plane - fail",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot exceed control plane version",
		},
		{
			name:                 "desired same minor higher patch than control plane - fail",
			desiredVersion:       "4.19.15",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot exceed control plane version",
		},
		// Minor version skipping tests
		{
			name:                 "z-stream upgrade - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.5"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false,
		},
		{
			name:                 "y-stream upgrade (+1) - pass",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.20.5"},
			expectError:          false,
		},
		{
			name:                 "skip minor version (+2) - fail",
			desiredVersion:       "4.20.5",
			activeVersions:       []string{"4.18.10"},
			controlPlaneVersions: []string{"4.20.5"},
			expectError:          true,
			errorContains:        "skipping minor versions is not allowed",
		},
		{
			name:                 "major version change - fail",
			desiredVersion:       "5.19.10",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"5.19.10"},
			expectError:          true,
			errorContains:        "major version changes are not supported",
		},
		// Downgrade tests
		{
			name:                 "desired equals highest active - pass",
			desiredVersion:       "4.19.10",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          false, // Short-circuits as version is already active
		},
		{
			name:                 "desired greater than highest active - pass",
			desiredVersion:       "4.19.15",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.15"},
			expectError:          false,
		},
		{
			name:                 "desired less than highest active (partial downgrade) - fail",
			desiredVersion:       "4.19.8",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot downgrade",
		},
		{
			name:                 "desired lower minor than highest active - fail",
			desiredVersion:       "4.18.15",
			activeVersions:       []string{"4.19.10"},
			controlPlaneVersions: []string{"4.19.10"},
			expectError:          true,
			errorContains:        "cannot downgrade",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, logr.Discard())

			desiredVersion := semver.MustParse(tt.desiredVersion)

			// Build ServiceProviderNodePool with active versions
			var nodePoolActiveVersions []api.HCPNodePoolActiveVersion
			for _, v := range tt.activeVersions {
				version := semver.MustParse(v)
				nodePoolActiveVersions = append(nodePoolActiveVersions, api.HCPNodePoolActiveVersion{Version: &version})
			}
			spNodePool := &api.ServiceProviderNodePool{
				Status: api.ServiceProviderNodePoolStatus{
					NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
						ActiveVersions: nodePoolActiveVersions,
					},
				},
			}

			// Build ServiceProviderCluster with control plane versions
			var cpActiveVersions []api.HCPClusterActiveVersion
			for _, v := range tt.controlPlaneVersions {
				version := semver.MustParse(v)
				cpActiveVersions = append(cpActiveVersions, api.HCPClusterActiveVersion{Version: &version, State: configv1.CompletedUpdate})
			}
			spCluster := &api.ServiceProviderCluster{
				Status: api.ServiceProviderClusterStatus{
					ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
						ActiveVersions: cpActiveVersions,
					},
				},
			}

			ctrl := gomock.NewController(t)

			// Create a mock Cincinnati client that returns the desired version as available
			mockCincinnatiClient := cincinatti.NewMockClient(ctrl)
			mockCincinnatiClient.EXPECT().
				GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(configv1.Release{}, []configv1.Release{{Version: tt.desiredVersion}}, nil, nil).
				AnyTimes()

			syncer := &nodePoolVersionSyncer{
				cooldownChecker:           &alwaysSyncCooldownChecker{},
				clusterToCincinnatiClient: lru.New(100),
			}
			// Pre-populate the Cincinnati client cache
			clusterKey := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}
			syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnatiClient)

			err := syncer.validateDesiredNodePoolVersion(
				ctx,
				&desiredVersion,
				spNodePool,
				spCluster,
				"stable",
				clusterKey,
				[16]byte{}, // dummy UUID
			)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNodePoolVersionSyncer_IsVersionInActiveVersions(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		activeVersions []string
		expected       bool
	}{
		{
			name:           "empty active versions",
			version:        "4.19.10",
			activeVersions: []string{},
			expected:       false,
		},
		{
			name:           "version found in single active version",
			version:        "4.19.10",
			activeVersions: []string{"4.19.10"},
			expected:       true,
		},
		{
			name:           "version not found in single active version",
			version:        "4.20.5",
			activeVersions: []string{"4.19.10"},
			expected:       false,
		},
		{
			name:           "version found in multiple active versions",
			version:        "4.19.5",
			activeVersions: []string{"4.19.10", "4.19.5"},
			expected:       true,
		},
		{
			name:           "version not found in multiple active versions",
			version:        "4.20.5",
			activeVersions: []string{"4.19.10", "4.19.5"},
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := semver.MustParse(tt.version)
			var activeVersions []api.HCPNodePoolActiveVersion
			for _, v := range tt.activeVersions {
				version := semver.MustParse(v)
				activeVersions = append(activeVersions, api.HCPNodePoolActiveVersion{Version: &version})
			}
			result := isVersionInActiveVersions(&version, activeVersions)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// createServiceProviderClusterWithVersion creates a ServiceProviderCluster with the given control plane version.
func createServiceProviderClusterWithVersion(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient, controlPlaneVersion string) {
	t.Helper()

	clusterResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName
	// ServiceProviderCluster resource ID format: {clusterResourceID}/{resourceTypeName}/{resourceName}
	spClusterResourceID := clusterResourceID + "/" + api.ServiceProviderClusterResourceTypeName + "/" + api.ServiceProviderClusterResourceName

	cpVersion := semver.MustParse(controlPlaneVersion)
	spCluster := &api.ServiceProviderCluster{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(spClusterResourceID)),
		},
		Status: api.ServiceProviderClusterStatus{
			ControlPlaneVersion: api.ServiceProviderClusterStatusVersion{
				ActiveVersions: []api.HCPClusterActiveVersion{
					{Version: &cpVersion, State: configv1.CompletedUpdate},
				},
			},
		},
	}
	_, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, spCluster, nil)
	require.NoError(t, err)
}

// createServiceProviderNodePoolWithVersion creates a ServiceProviderNodePool with the given active version.
func createServiceProviderNodePoolWithVersion(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient, activeVersion string) {
	t.Helper()

	nodePoolResourceID := "/subscriptions/" + testSubscriptionID +
		"/resourceGroups/" + testResourceGroupName +
		"/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/" + testClusterName +
		"/nodePools/" + testNodePoolName
	// ServiceProviderNodePool resource ID format: {nodePoolResourceID}/{resourceTypeName}/{resourceName}
	spNodePoolResourceID := nodePoolResourceID + "/" + api.ServiceProviderNodePoolResourceTypeName + "/" + api.ServiceProviderNodePoolResourceName

	version := semver.MustParse(activeVersion)
	spNodePool := &api.ServiceProviderNodePool{
		CosmosMetadata: api.CosmosMetadata{
			ResourceID: api.Must(azcorearm.ParseResourceID(spNodePoolResourceID)),
		},
		Status: api.ServiceProviderNodePoolStatus{
			NodePoolVersion: api.ServiceProviderNodePoolStatusVersion{
				ActiveVersions: []api.HCPNodePoolActiveVersion{
					{Version: &version},
				},
			},
		},
	}
	_, err := mockDB.ServiceProviderNodePools(testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName).Create(ctx, spNodePool, nil)
	require.NoError(t, err)
}

func newCSNodePoolWithVersion(t *testing.T, version string) *arohcpv1alpha1.NodePool {
	t.Helper()

	csNodePool, err := arohcpv1alpha1.NewNodePool().
		ID(testNodePoolName).
		Version(arohcpv1alpha1.NewVersion().RawID(version)).
		Build()
	require.NoError(t, err)
	return csNodePool
}

func TestNodePoolVersionSyncer_SyncOnce_SkipMinorVersionFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	// Create node pool with desired version 4.20.0 (skips from 4.18.x)
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.20.0")

	// Create ServiceProviderCluster with control plane at 4.20.0 (allowing the desired version)
	createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.20.0")

	// Create ServiceProviderNodePool with active version 4.18.10 (to create skew)
	createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.18.10")

	// Setup CS mocks
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// CS returns node pool with current version 4.18.10
	csNodePool := newCSNodePoolWithVersion(t, "4.18.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "skipping minor versions is not allowed")
}

func TestNodePoolVersionSyncer_SyncOnce_DesiredExceedsControlPlaneFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	// Create node pool with desired version 4.19.15 (exceeds control plane 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")

	// Create ServiceProviderCluster with control plane at 4.19.10 (lower than desired)
	createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.19.10")

	// Create ServiceProviderNodePool with active version 4.19.5 (so desired is not already active)
	createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.5")

	// Setup CS mocks
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// CS returns node pool with current version 4.19.5
	csNodePool := newCSNodePoolWithVersion(t, "4.19.5")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot exceed control plane version")
}

func TestNodePoolVersionSyncer_SyncOnce_NoUpgradePathInCincinnatiFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCincinnati := cincinatti.NewMockClient(ctrl)

	// Create node pool with desired version 4.19.10
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.10")

	// Create ServiceProviderCluster with control plane at 4.20.0 (allows the desired version)
	createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.20.0")

	// Setup CS mocks
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// CS returns node pool with current version 4.19.7
	csNodePool := newCSNodePoolWithVersion(t, "4.19.7")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Setup Cincinnati mock to return NO upgrade path (empty candidates)
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.7"},
			[]configv1.Release{}, // Empty - no upgrade path available
			nil,
			nil,
		).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	// Pre-populate Cincinnati client cache
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnati)

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no upgrade path available")
}

func TestNodePoolVersionSyncer_SyncOnce_DowngradeFails(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

	// Create node pool with desired version 4.19.5 (downgrade from 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.5")

	// Create ServiceProviderCluster with control plane at 4.20.0
	createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.20.0")

	// Create ServiceProviderNodePool with active version 4.19.10 (higher than desired)
	createServiceProviderNodePoolWithVersion(t, ctx, mockDB, "4.19.10")

	// Setup CS mocks
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// CS returns node pool with current version 4.19.10
	csNodePool := newCSNodePoolWithVersion(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot downgrade")
}

func TestNodePoolVersionSyncer_SyncOnce_UpgradePathExistsSucceeds(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCincinnati := cincinatti.NewMockClient(ctrl)

	// Create node pool with desired version 4.19.15 (valid upgrade from 4.19.10)
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")

	// Create ServiceProviderCluster with control plane at 4.20.0
	createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.20.0")

	// Setup CS mocks
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// CS returns node pool with current version 4.19.10
	csNodePool := newCSNodePoolWithVersion(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Setup Cincinnati mock to return valid upgrade path including desired version
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{
				{Version: "4.19.12"},
				{Version: "4.19.15"}, // Desired version is in candidates
				{Version: "4.19.18"},
			},
			nil,
			nil,
		).
		Times(1)

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	// Pre-populate Cincinnati client cache
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnati)

	testKey := controllerutils.HCPNodePoolKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		HCPNodePoolName:   testNodePoolName,
	}

	ctx = utils.ContextWithLogger(ctx, logr.Discard())
	err := syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err)

	// Verify the ServiceProviderNodePool was updated correctly
	spnp, err := mockDB.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)

	// Verify DesiredVersion was persisted
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion)
	expectedDesiredVersion := semver.MustParse("4.19.15")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion))
}

func TestNodePoolVersionSyncer_SyncOnce_DesiredVersionUnchangedOnFailure_ChangedOnSuccess(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)

	mockDB := databasetesting.NewMockDBClient()
	mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)
	mockCincinnati := cincinatti.NewMockClient(ctrl)

	// Seed the database with a node pool
	createTestNodePoolWithVersion(t, ctx, mockDB, "4.19.15")

	// Setup CS mock to return a cluster with UUID
	csCluster := newCSCluster(t)
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)

	// Setup CS mock to return a node pool with version
	csNodePool := newCSNodePool(t, "4.19.10")
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Setup Cincinnati mock to return valid upgrade path
	mockCincinnati.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{{Version: "4.19.15"}},
			nil,
			nil,
		).
		AnyTimes()

	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           &alwaysSyncCooldownChecker{},
		cosmosClient:              mockDB,
		clusterServiceClient:      mockCS,
		clusterToCincinnatiClient: lru.New(100),
	}

	// Pre-populate Cincinnati client cache
	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}
	syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnati)

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

	expectedActiveVersion := semver.MustParse("4.19.10")
	expectedDesiredVersion := semver.MustParse("4.19.15")

	// Verify ActiveVersion was persisted (from Cluster Service)
	require.NotNil(t, spnp.Status.NodePoolVersion.ActiveVersions,
		"ActiveVersion should be set")
	require.True(t, expectedActiveVersion.EQ(*spnp.Status.NodePoolVersion.ActiveVersions[0].Version),
		"ActiveVersion should match CS version %s, got %s", "4.19.10", spnp.Status.NodePoolVersion.ActiveVersions[0].Version)

	// Verify DesiredVersion was persisted (from customer's HCPNodePool)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should be set")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should match customer version %s, got %s", "4.19.15", spnp.Spec.NodePoolVersion.DesiredVersion)

	// --- Phase 2: Change version, Cincinnati fails, desired should NOT change ---

	// Update the HCPNodePool with a new desired version
	nodePool, err := mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Get(ctx, testNodePoolName)
	require.NoError(t, err)
	nodePool.Properties.Version.ID = "4.19.20"
	_, err = mockDB.HCPClusters(testSubscriptionID, testResourceGroupName).
		NodePools(testClusterName).Replace(ctx, nodePool, nil)
	require.NoError(t, err)

	// Setup CS mocks for second sync
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Replace Cincinnati mock with one that fails (no upgrade path)
	mockCincinnatiFailing := cincinatti.NewMockClient(ctrl)
	mockCincinnatiFailing.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{}, // Empty - no upgrade path available
			nil,
			nil,
		).
		AnyTimes()
	syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnatiFailing)

	// SyncOnce should fail because Cincinnati doesn't have upgrade path
	err = syncer.SyncOnce(ctx, testKey)
	require.Error(t, err, "SyncOnce should fail when Cincinnati has no upgrade path")
	assert.Contains(t, err.Error(), "no upgrade path available")

	// Verify that DesiredVersion was NOT changed (still 4.19.15)
	spnp, err = mockDB.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should still be set")
	require.True(t, expectedDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should NOT have changed after failed validation, expected %s, got %s",
		"4.19.15", spnp.Spec.NodePoolVersion.DesiredVersion)

	// --- Phase 3: Cincinnati succeeds, desired should change ---

	// Setup CS mocks for third sync
	mockCS.EXPECT().
		GetCluster(gomock.Any(), gomock.Any()).
		Return(csCluster, nil).
		Times(1)
	mockCS.EXPECT().
		GetNodePool(gomock.Any(), gomock.Any()).
		Return(csNodePool, nil).
		Times(1)

	// Replace Cincinnati mock with one that succeeds
	mockCincinnatiSucceeding := cincinatti.NewMockClient(ctrl)
	mockCincinnatiSucceeding.EXPECT().
		GetUpdates(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(
			configv1.Release{Version: "4.19.10"},
			[]configv1.Release{{Version: "4.19.20"}}, // Valid upgrade path
			nil,
			nil,
		).
		AnyTimes()
	syncer.clusterToCincinnatiClient.Add(clusterKey, mockCincinnatiSucceeding)

	// SyncOnce should succeed now
	err = syncer.SyncOnce(ctx, testKey)
	require.NoError(t, err, "SyncOnce should succeed when Cincinnati has valid upgrade path")

	// Verify that DesiredVersion HAS changed to 4.19.20
	spnp, err = mockDB.ServiceProviderNodePools(
		testSubscriptionID, testResourceGroupName, testClusterName, testNodePoolName,
	).Get(ctx, api.ServiceProviderNodePoolResourceName)
	require.NoError(t, err)
	expectedNewDesiredVersion := semver.MustParse("4.19.20")
	require.NotNil(t, spnp.Spec.NodePoolVersion.DesiredVersion,
		"DesiredVersion should be set")
	require.True(t, expectedNewDesiredVersion.EQ(*spnp.Spec.NodePoolVersion.DesiredVersion),
		"DesiredVersion should have changed after successful validation, expected %s, got %s",
		"4.19.20", spnp.Spec.NodePoolVersion.DesiredVersion)
}
