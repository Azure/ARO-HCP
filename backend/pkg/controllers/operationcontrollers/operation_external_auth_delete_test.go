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
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationExternalAuthDelete_LegacySynchronizeOperation(t *testing.T) {
	tests := []struct {
		name        string
		setupCSMock func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
		expectError bool
		verifyDB    func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture)
	}{
		{
			name: "external auth not found marks operation succeeded and removes external auth",
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify external auth document was deleted
				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				assert.Error(t, err, "external auth should have been deleted")
			},
		},
		{
			name: "external auth still exists keeps operation pending",
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				externalAuth, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(externalAuth, nil)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				// When external auth still exists, operation stays at Accepted
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				// External auth should still exist
				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.NotNil(t, externalAuth)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newExternalAuthTestFixture()
			cluster := fixture.newCluster()
			externalAuth := fixture.newExternalAuth()
			operation := fixture.newOperation(database.OperationRequestDelete)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, externalAuth, operation})
			require.NoError(t, err)

			mockCSClient := tt.setupCSMock(ctrl, fixture)

			controller := &operationExternalAuthDelete{
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}

func TestOperationExternalAuthDelete_NewApproach_SynchronizeOperation(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name               string
		modifyExternalAuth func(ea *api.HCPOpenShiftClusterExternalAuth)
		modifyOperation    func(op *api.Operation)
		setupCSMock        func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
		deleteExternalAuth bool
		expectError        bool
		verifyDB           func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture)
	}{
		{
			name: "cosmos document gone -- marks operation succeeded",
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			deleteExternalAuth: true,
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "cosmos document exists, all timestamps set, CS reports uninstalling -- status set to Deleting",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				csEA, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Status(arohcpv1alpha1.NewExternalAuthStatus().
						State(arohcpv1alpha1.NewExternalAuthState().
							Value(string(ExternalAuthStateUninstalling)))).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(csEA, nil)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name: "cosmos document exists, all timestamps set, CS reports ready -- no-op (waits for Cosmos deletion)",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				csEA, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Status(arohcpv1alpha1.NewExternalAuthStatus().
						State(arohcpv1alpha1.NewExternalAuthState().
							Value(string(ExternalAuthStateReady)))).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(csEA, nil)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "cosmos document exists, all timestamps set, CS reports error -- status set to Failed",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				csEA, _ := arohcpv1alpha1.NewExternalAuth().
					ID(testExternalAuthIDStr).
					Status(arohcpv1alpha1.NewExternalAuthStatus().
						State(arohcpv1alpha1.NewExternalAuthState().
							Value(string(ExternalAuthStateError))).
						Message("something went wrong")).
					Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(csEA, nil)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "something went wrong", op.Error.Message)
			},
		},
		{
			name: "cosmos document exists, all timestamps set, CS returns 404 -- no-op (waits for CS ID clearer)",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				// Operation should remain at Accepted -- we don't update here, the ID clearer handles it
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)

				// External auth should still exist
				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.NotNil(t, externalAuth)
			},
		},
		{
			name: "cosmos document exists but ClusterServiceDeletionTimestamp not set -- no reconcile",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "cosmos document exists but ClusterServiceID is nil -- no reconcile",
			modifyExternalAuth: func(ea *api.HCPOpenShiftClusterExternalAuth) {
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
				ea.ServiceProviderProperties.ClusterServiceID = nil
				ea.ServiceProviderProperties.UsesNewExternalAuthDeletionApproach = true
			},
			modifyOperation: func(op *api.Operation) {
				op.UsesNewExternalAuthDeletionApproach = true
			},
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				return ocm.NewMockClusterServiceClientSpec(ctrl)
			},
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newExternalAuthTestFixture()
			cluster := fixture.newCluster()
			externalAuth := fixture.newExternalAuth()
			operation := fixture.newOperation(database.OperationRequestDelete)

			if tt.modifyExternalAuth != nil {
				tt.modifyExternalAuth(externalAuth)
			}
			if tt.modifyOperation != nil {
				tt.modifyOperation(operation)
			}

			resources := []any{cluster, operation}
			if !tt.deleteExternalAuth {
				resources = append(resources, externalAuth)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := tt.setupCSMock(ctrl, fixture)

			controller := &operationExternalAuthDelete{
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
