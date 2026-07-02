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
	"strings"
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
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	internallistertesting "github.com/Azure/ARO-HCP/internal/database/listertesting"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationClusterUpdate_SynchronizeOperation(t *testing.T) {
	testClockNow := mustParseTime("2024-06-01T12:00:00Z")
	fixture := newClusterTestFixture()

	newClusterWithCustomerVersion := func(versionID string, mutate ...func(*api.HCPOpenShiftCluster)) *api.HCPOpenShiftCluster {
		cluster := fixture.newCluster(nil)
		cluster.CustomerProperties.Version.ID = versionID
		for _, fn := range mutate {
			if fn != nil {
				fn(cluster)
			}
		}
		return cluster
	}

	newCSClusterWithState := func(state arohcpv1alpha1.ClusterState) *arohcpv1alpha1.Cluster {
		allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)
		csCluster, err := arohcpv1alpha1.NewCluster().
			API(arohcpv1alpha1.NewClusterAPI().
				CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(allowAccess))).
			Status(arohcpv1alpha1.NewClusterStatus().State(state)).
			Build()
		require.NoError(t, err)
		return csCluster
	}

	newCSClusterReadyWithNodeDrainMinutes := func(minutes int32) *arohcpv1alpha1.Cluster {
		allowAccess := arohcpv1alpha1.NewCIDRBlockAllowAccess().Mode(ocm.CSCIDRBlockAllowAccessModeAllowAll)
		csCluster, err := arohcpv1alpha1.NewCluster().
			NodeDrainGracePeriod(arohcpv1alpha1.NewValue().
				Unit("minutes").
				Value(float64(minutes))).
			API(arohcpv1alpha1.NewClusterAPI().
				CIDRBlockAccess(arohcpv1alpha1.NewCIDRBlockAccess().
					Allow(allowAccess))).
			Status(arohcpv1alpha1.NewClusterStatus().State(arohcpv1alpha1.ClusterStateReady)).
			Build()
		require.NoError(t, err)
		return csCluster
	}

	newOperationAccepted := func() *api.Operation {
		return fixture.newOperation(database.OperationRequestUpdate)
	}

	newServiceProviderClusterWithSpecControlPlaneVersion := func(version string) *api.ServiceProviderCluster {
		resourceID := api.Must(azcorearm.ParseResourceID(fmt.Sprintf("%s/%s/%s",
			fixture.clusterResourceID.String(),
			api.ServiceProviderClusterResourceTypeName,
			api.ServiceProviderClusterResourceName,
		)))
		parsedVersion, err := semver.ParseTolerant(version)
		require.NoError(t, err)
		return &api.ServiceProviderCluster{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			Spec: api.ServiceProviderClusterSpec{
				ControlPlaneVersion: api.ServiceProviderClusterSpecVersion{
					DesiredVersion: ptr.To(parsedVersion),
				},
			},
			Status: api.ServiceProviderClusterStatus{},
		}
	}
	newDefaultControlPlaneDesiredVersionController := func() *api.Controller {
		resourceID := api.Must(azcorearm.ParseResourceID(fixture.clusterResourceID.String() + "/hcpOpenShiftControllers/ControlPlaneDesiredVersion"))
		return &api.Controller{
			CosmosMetadata: api.CosmosMetadata{ResourceID: resourceID, PartitionKey: strings.ToLower(resourceID.SubscriptionID)},
			ExternalID:     fixture.clusterResourceID,
			Status:         api.ControllerStatus{},
		}
	}

	newControlPlaneDesiredVersionControllerWithConditions := func(conditions []metav1.Condition) *api.Controller {
		controller := newDefaultControlPlaneDesiredVersionController()
		controller.Status.Conditions = conditions
		return controller
	}

	newPassingCachedHostedClusterReadDesire := func() *kubeapplier.ReadDesire {
		return newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
			Spec: testClusterUpdateMatchingHostedClusterSpec(),
		})
	}

	testCases := []struct {
		name            string
		existingCluster *api.HCPOpenShiftCluster
		// When not set, the controller uses a cluster lister that contains the existingCluster
		clusterLister     listers.ClusterLister
		existingOperation *api.Operation
		// When not set, the controller uses an active operations lister that contains the existingOperation
		activeOperationsLister         listers.ActiveOperationLister
		existingServiceProviderCluster *api.ServiceProviderCluster
		// When not set, the controller uses a service provider cluster lister that contains the existingServiceProviderCluster
		serviceProviderClusterLister                 listers.ServiceProviderClusterLister
		existingControlPlaneDesiredVersionController *api.Controller
		// When set, wires a ReadDesireLister containing this cached HostedCluster mirror.
		cachedHostedClusterReadDesire *kubeapplier.ReadDesire
		seedMismatchFirstSeenAt       time.Time
		setupMockCSClient             func(*ocm.MockClusterServiceClientSpec)
		wantErr                       bool
		verifyDB                      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:                           "cs cluster ready transitions operation to succeeded",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                           "cs cluster updating transitions operation to updating",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateUpdating), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                           "cs cluster error transitions operation to failed",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateError), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                           "cs cluster pending keeps operation accepted",
			existingCluster:                newClusterWithCustomerVersion("4.19"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStatePending), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                           "customer minor mismatch with IntentFailed on ControlPlaneDesiredVersion controller marks operation failed",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			existingControlPlaneDesiredVersionController: newControlPlaneDesiredVersionControllerWithConditions([]metav1.Condition{
				{
					Type:    api.ControllerConditionTypeIntentFailed,
					Status:  metav1.ConditionTrue,
					Reason:  api.VersionUpgradeNotAcceptedReason,
					Message: "example intent failed message",
				},
			}),
			cachedHostedClusterReadDesire: newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)
				assert.Contains(t, op.Error.Message, "example intent failed message")

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:                           "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                           "customer minor mismatch without ControlPlaneDesiredVersion IntentFailed leaves operation accepted when first seen within 29s",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			seedMismatchFirstSeenAt:        testClockNow.Add(-20 * time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
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
			name:                           "customer minor mismatch without IntentFailed fails when mismatch first seen exceeds 29s",
			existingCluster:                newClusterWithCustomerVersion("4.20"),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			seedMismatchFirstSeenAt:        testClockNow.Add(-30 * time.Second),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				require.NotNil(t, op.Error)
				assert.Equal(t, arm.CloudErrorCodeInvalidRequestContent, op.Error.Code)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				wantMessageSubstr := fmt.Sprintf(
					"timed out after 29s waiting for resolution of desired version from '%s' cluster version",
					cluster.CustomerProperties.Version.ID,
				)
				assert.Contains(t, op.Error.Message, wantMessageSubstr)

				assert.Equal(t, arm.ProvisioningStateFailed, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Empty(t, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *api.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.ClusterServiceID = nil
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when cluster is deleting",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *api.HCPOpenShiftCluster) {
				cluster.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: testClockNow}
			}),
			existingOperation: newOperationAccepted(),
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:              "cluster not in lister cache leaves operation unchanged",
			existingCluster:   newClusterWithCustomerVersion("4.19"),
			existingOperation: newOperationAccepted(),
			clusterLister:     &listertesting.SliceClusterLister{},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
				assert.Empty(t, cluster.ServiceProviderProperties.ProvisioningState)
			},
		},
		{
			name: "cs cluster ready with node drain spec mismatch keeps operation updating",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *api.HCPOpenShiftCluster) {
				cluster.CustomerProperties.NodeDrainTimeoutMinutes = 60
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire:  newPassingCachedHostedClusterReadDesire(),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterReadyWithNodeDrainMinutes(30), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "cs cluster ready with hypershift autoscaling spec mismatch keeps operation updating",
			existingCluster: newClusterWithCustomerVersion("4.19", func(cluster *api.HCPOpenShiftCluster) {
				cluster.CustomerProperties.Autoscaling.MaxNodesTotal = 10
			}),
			existingOperation:              newOperationAccepted(),
			existingServiceProviderCluster: newServiceProviderClusterWithSpecControlPlaneVersion("4.19"),
			cachedHostedClusterReadDesire: newHostedClusterReadDesire(t, &v1beta1.HostedCluster{
				Spec: func() v1beta1.HostedClusterSpec {
					spec := testClusterUpdateMatchingHostedClusterSpec()
					spec.Autoscaling.MaxNodesTotal = ptr.To[int32](5)
					return spec
				}(),
			}),
			setupMockCSClient: func(mock *ocm.MockClusterServiceClientSpec) {
				mock.EXPECT().
					GetCluster(gomock.Any(), fixture.clusterInternalID).
					Return(newCSClusterWithState(arohcpv1alpha1.ClusterStateReady), nil)
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateUpdating, cluster.ServiceProviderProperties.ProvisioningState)
				assert.Equal(t, testOperationName, cluster.ServiceProviderProperties.ActiveOperationID)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)

			resources := []any{}
			if tc.existingCluster != nil {
				resources = append(resources, tc.existingCluster)
			}
			if tc.existingOperation != nil {
				resources = append(resources, tc.existingOperation)
			}
			if tc.existingServiceProviderCluster != nil {
				resources = append(resources, tc.existingServiceProviderCluster)
			}
			if tc.existingControlPlaneDesiredVersionController != nil {
				resources = append(resources, tc.existingControlPlaneDesiredVersionController)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var readDesireLister dblisters.ReadDesireLister
			if tc.cachedHostedClusterReadDesire != nil {
				readDesireLister = &internallistertesting.SliceReadDesireLister{
					Desires: []*kubeapplier.ReadDesire{tc.cachedHostedClusterReadDesire},
				}
			}

			clusterLister := tc.clusterLister
			if clusterLister == nil {
				clusterLister = &listertesting.DBClusterLister{ResourcesDBClient: mockResourcesDBClient}
			}
			activeOperationsLister := tc.activeOperationsLister
			if activeOperationsLister == nil {
				activeOperationsLister = &listertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient}
			}
			serviceProviderClusterLister := tc.serviceProviderClusterLister
			if serviceProviderClusterLister == nil {
				serviceProviderClusterLister = &listertesting.DBServiceProviderClusterLister{ResourcesDBClient: mockResourcesDBClient}
			}

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tc.setupMockCSClient != nil {
				tc.setupMockCSClient(mockCSClient)
			}

			fakeClock := clocktesting.NewFakeClock(testClockNow)
			controller := &operationClusterUpdate{
				resourcesDBClient:               mockResourcesDBClient,
				clusterServiceClient:            mockCSClient,
				clusterLister:                   clusterLister,
				activeOperationsLister:          activeOperationsLister,
				serviceProviderClusterLister:    serviceProviderClusterLister,
				readDesireLister:                readDesireLister,
				notificationClient:              nil,
				clock:                           fakeClock,
				desiredVersionMismatchFirstSeen: lru.New(100000),
			}
			if !tc.seedMismatchFirstSeenAt.IsZero() {
				require.NotNil(t, tc.existingOperation)
				controller.desiredVersionMismatchFirstSeen.Add(tc.existingOperation.ResourceID.String(), tc.seedMismatchFirstSeenAt)
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tc.verifyDB != nil {
				tc.verifyDB(t, ctx, mockResourcesDBClient)
			}
		})
	}
}
