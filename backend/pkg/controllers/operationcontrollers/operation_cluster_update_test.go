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

package operationcontrollers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"
	"k8s.io/utils/lru"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterUpdate_SynchronizeOperation(t *testing.T) {
	t.Parallel()
	testClockNow := mustParseTime("2024-06-01T12:00:00Z")
	tests := []struct {
		name                                           string
		clusterState                                   arohcpv1alpha1.ClusterState
		customerVersionID                              string
		serviceProviderClusterStatusConditions         []metav1.Condition
		controlPlaneDesiredVersionControllerConditions []metav1.Condition
		seedMismatchFirstSeenAt                        time.Time
		verify                                         func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name:              "cluster ready transitions operation to succeeded",
			clusterState:      arohcpv1alpha1.ClusterStateReady,
			customerVersionID: "4.19",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "cluster updating transitions operation to updating",
			clusterState:      arohcpv1alpha1.ClusterStateUpdating,
			customerVersionID: "4.19",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "cluster error transitions operation to failed",
			clusterState:      arohcpv1alpha1.ClusterStateError,
			customerVersionID: "4.19",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "cluster pending keeps operation accepted",
			clusterState:      arohcpv1alpha1.ClusterStatePending,
			customerVersionID: "4.19",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "customer minor mismatch with IntentFailed on ControlPlaneDesiredVersion controller marks operation failed",
			clusterState:      arohcpv1alpha1.ClusterStateReady,
			customerVersionID: "4.20",
			controlPlaneDesiredVersionControllerConditions: []metav1.Condition{
				{
					Type:    api.ControllerConditionTypeIntentFailed,
					Status:  metav1.ConditionTrue,
					Reason:  api.VersionUpgradeNotAcceptedReason,
					Message: "no downgrades allowed",
				},
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)
				assert.Contains(t, op.Error.Message, "no downgrades allowed")

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:              "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted",
			clusterState:      arohcpv1alpha1.ClusterStateReady,
			customerVersionID: "4.20",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name:                    "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted when first seen within 29s",
			clusterState:            arohcpv1alpha1.ClusterStateReady,
			customerVersionID:       "4.20",
			seedMismatchFirstSeenAt: testClockNow.Add(-20 * time.Second),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name:                    "customer minor mismatch without IntentFailed fails when mismatch first seen exceeds 29s",
			clusterState:            arohcpv1alpha1.ClusterStateReady,
			customerVersionID:       "4.20",
			seedMismatchFirstSeenAt: testClockNow.Add(-30 * time.Second),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				wantMsg := fmt.Sprintf(
					"timed out after 29s waiting for resolution of desired version from '%s' cluster version",
					cluster.CustomerProperties.Version.ID,
				)
				assert.Equal(t, wantMsg, op.Error.Message)

				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			cluster.CustomerProperties.Version.ID = tt.customerVersionID
			operation := fixture.newOperation(database.OperationRequestUpdate)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)
			resourceId := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
				fixture.clusterResourceID.String(),
				api.ServiceProviderClusterResourceTypeName,
				api.ServiceProviderClusterResourceName,
			)))

			_, err = mockResourcesDBClient.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Create(ctx, &api.ServiceProviderCluster{
				CosmosMetadata: api.CosmosMetadata{ResourceID: resourceId},
				ResourceID:     *resourceId,
				Spec: api.ServiceProviderClusterSpec{
					ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{
						DesiredVersion: ptr.To(semver.MustParse("4.19.0")),
					},
				},
				Status: api.ServiceProviderClusterStatus{
					Conditions: tt.serviceProviderClusterStatusConditions,
				},
			}, nil)
			require.NoError(t, err)

			rid := api.Must(azcorearm.ParseResourceID(
				fixture.clusterResourceID.String() + "/hcpOpenShiftControllers/ControlPlaneDesiredVersion",
			))
			_, err = mockResourcesDBClient.HCPClusters(testSubscriptionID, testResourceGroupName).Controllers(testClusterName).Create(ctx, &api.Controller{
				CosmosMetadata: api.CosmosMetadata{ResourceID: rid},
				ResourceID:     rid,
				ExternalID:     fixture.clusterResourceID,
				Status: api.ControllerStatus{
					Conditions: tt.controlPlaneDesiredVersionControllerConditions,
				},
			}, nil)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			clusterStatus, err := arohcpv1alpha1.NewClusterStatus().
				State(tt.clusterState).
				Build()
			require.NoError(t, err)
			mockCSClient.EXPECT().
				GetClusterStatus(gomock.Any(), fixture.clusterInternalID).
				Return(clusterStatus, nil)

			fakeClock := clocktesting.NewFakeClock(testClockNow)
			controller := &operationClusterUpdate{
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				notificationClient:              nil,
				clock:                           fakeClock,
				desiredVersionMismatchFirstSeen: lru.New(100000),
			}
			if !tt.seedMismatchFirstSeenAt.IsZero() {
				controller.desiredVersionMismatchFirstSeen.Add(operation.ResourceID.String(), tt.seedMismatchFirstSeenAt)
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
