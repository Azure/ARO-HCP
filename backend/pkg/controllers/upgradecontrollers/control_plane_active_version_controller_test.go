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
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestControlPlaneActiveVersionSyncer_SyncOnce(t *testing.T) {
	testKey := controllerutils.HCPClusterKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
	}

	clusterInternalID, err := api.NewInternalID(testCSClusterIDStr)
	require.NoError(t, err)

	tests := []struct {
		name                  string
		seedDB                func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
		mockCS                func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID)
		expectedError         bool
		expectedErrorContains string
		validateAfter         func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient)
	}{
		{
			name: "cluster not found in cosmos returns nil",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				// No cluster seeded - Get will return not found.
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				// No CS mock - we never reach GetCluster.
			},
			expectedError: false,
		},
		{
			name: "cluster service GetCluster returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				mockCS.EXPECT().
					GetCluster(ctx, clusterInternalID).
					Return(nil, errors.New("cluster service unavailable")).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "cluster service unavailable",
		},
		{
			name: "cluster version not found in Cluster Service response returns error",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				csCluster := newCSClusterWithoutVersion(t)
				mockCS.EXPECT().
					GetCluster(ctx, clusterInternalID).
					Return(csCluster, nil).
					Times(1)
			},
			expectedError:         true,
			expectedErrorContains: "cluster version not found in Cluster Service response",
		},
		{
			name: "active versions unchanged when CS version matches current - no Replace",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.19.15")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				csCluster := newCSClusterWithVersion(t, "4.19.15")
				mockCS.EXPECT().
					GetCluster(ctx, clusterInternalID).
					Return(csCluster, nil).
					Times(1)
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Len(t, spc.Status.ControlPlaneVersion.ActiveVersions, 1)
				assert.True(t, spc.Status.ControlPlaneVersion.ActiveVersions[0].Version.EQ(semver.MustParse("4.19.15")))
			},
		},
		{
			name: "active versions updated when CS version is new - Replace called",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				// GetOrCreateServiceProviderCluster will create a service provider cluster with empty status if not present.
				// Seed it with one version so we can see a change to two versions.
				createServiceProviderClusterWithVersion(t, ctx, mockDB, "4.19.10")
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				csCluster := newCSClusterWithVersion(t, "4.19.15")
				mockCS.EXPECT().
					GetCluster(ctx, clusterInternalID).
					Return(csCluster, nil).
					Times(1)
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Len(t, spc.Status.ControlPlaneVersion.ActiveVersions, 2)
				// Newest first: 4.19.15, then previous 4.19.10
				assert.True(t, spc.Status.ControlPlaneVersion.ActiveVersions[0].Version.EQ(semver.MustParse("4.19.15")))
				assert.True(t, spc.Status.ControlPlaneVersion.ActiveVersions[1].Version.EQ(semver.MustParse("4.19.10")))
			},
		},
		{
			name: "active versions set when service provider cluster had none - Replace called",
			seedDB: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				createTestHCPCluster(t, ctx, mockDB)
				// Do not seed service provider cluster - GetOrCreateServiceProviderCluster will create one with empty status.
			},
			mockCS: func(t *testing.T, mockCS *ocm.MockClusterServiceClientSpec, ctx context.Context, clusterInternalID api.InternalID) {
				t.Helper()
				csCluster := newCSClusterWithVersion(t, "4.19.20")
				mockCS.EXPECT().
					GetCluster(ctx, clusterInternalID).
					Return(csCluster, nil).
					Times(1)
			},
			expectedError: false,
			validateAfter: func(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
				t.Helper()
				spc, err := mockDB.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				require.Len(t, spc.Status.ControlPlaneVersion.ActiveVersions, 1)
				assert.True(t, spc.Status.ControlPlaneVersion.ActiveVersions[0].Version.EQ(semver.MustParse("4.19.20")))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCtx := utils.ContextWithLogger(context.Background(), logr.Discard())
			ctrl := gomock.NewController(t)

			mockDB := databasetesting.NewMockDBClient()
			mockCS := ocm.NewMockClusterServiceClientSpec(ctrl)

			tt.seedDB(t, runCtx, mockDB)
			tt.mockCS(t, mockCS, runCtx, clusterInternalID)

			syncer := &controlPlaneActiveVersionSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				cosmosClient:         mockDB,
				clusterServiceClient: mockCS,
			}

			err := syncer.SyncOnce(runCtx, testKey)

			assertSyncResult(t, err, tt.expectedError, tt.expectedErrorContains)

			if tt.validateAfter != nil && !tt.expectedError {
				tt.validateAfter(t, runCtx, mockDB)
			}
		})
	}
}

// createTestHCPCluster creates an HCP cluster in the mock database (no node pools).
// Used as the parent resource for control plane active version sync.
func createTestHCPCluster(t *testing.T, ctx context.Context, mockDB *databasetesting.MockDBClient) {
	t.Helper()

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
}

// newCSClusterWithVersion creates a Cluster Service cluster with the given version for testing.
func newCSClusterWithVersion(t *testing.T, version string) *arohcpv1alpha1.Cluster {
	t.Helper()

	csCluster, err := arohcpv1alpha1.NewCluster().
		ID(testClusterName).
		ExternalID(testClusterExternalID).
		Version(arohcpv1alpha1.NewVersion().RawID(version)).
		Build()
	require.NoError(t, err)
	return csCluster
}

// newCSClusterWithoutVersion creates a Cluster Service cluster with no version (GetVersion returns false).
func newCSClusterWithoutVersion(t *testing.T) *arohcpv1alpha1.Cluster {
	t.Helper()

	csCluster, err := arohcpv1alpha1.NewCluster().
		ID(testClusterName).
		ExternalID(testClusterExternalID).
		Build()
	require.NoError(t, err)
	return csCluster
}
