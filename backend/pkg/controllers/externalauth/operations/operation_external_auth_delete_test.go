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

package operations

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

func TestOperationExternalAuthDelete_SynchronizeOperation(t *testing.T) {
	fixture := newExternalAuthTestFixture()

	externalAuthPassingExtraReconcileGate := func() *api.HCPOpenShiftClusterExternalAuth {
		now := time.Now()
		ea := fixture.newExternalAuth()
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: now}
		ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: now}
		return ea
	}

	testCases := []struct {
		name                                string
		existingExternalAuth                *api.HCPOpenShiftClusterExternalAuth
		wantErr                             bool
		verifyDB                            func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		usesNewExternalAuthDeletionApproach bool
		setupCSMock                         func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
	}{
		{
			name:                                "external auth document gone completes operation",
			usesNewExternalAuthDeletionApproach: true,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed skips cluster service",
			existingExternalAuth: func() *api.HCPOpenShiftClusterExternalAuth {
				ea := fixture.newExternalAuth()
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return ea
			}(),
			usesNewExternalAuthDeletionApproach: true,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "shouldReconcile gate not passed when ClusterServiceID is nil",
			existingExternalAuth: func() *api.HCPOpenShiftClusterExternalAuth {
				ea := fixture.newExternalAuth()
				ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				ea.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &metav1.Time{Time: time.Now()}
				ea.ServiceProviderProperties.ClusterServiceID = nil
				return ea
			}(),
			usesNewExternalAuthDeletionApproach: true,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                                "extra reconcile gate passed and CS ready waits without updating operation",
			existingExternalAuth:                externalAuthPassingExtraReconcileGate(),
			usesNewExternalAuthDeletionApproach: true,
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                                "extra reconcile gate passed and CS uninstalling updates operation to deleting",
			existingExternalAuth:                externalAuthPassingExtraReconcileGate(),
			usesNewExternalAuthDeletionApproach: true,
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name:                                "extra reconcile gate passed and CS error marks operation failed",
			existingExternalAuth:                externalAuthPassingExtraReconcileGate(),
			usesNewExternalAuthDeletionApproach: true,
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.NotNil(t, op.Error)
				assert.Equal(t, "something went wrong", op.Error.Message)
			},
		},
		{
			name:                                "extra reconcile gate passed and CS returns 404 waits for ID clearer",
			existingExternalAuth:                externalAuthPassingExtraReconcileGate(),
			usesNewExternalAuthDeletionApproach: true,
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:                 "legacy approach: external auth gone in cluster service marks operation succeeded",
			existingExternalAuth: fixture.newExternalAuth(),
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				notFoundErr, _ := ocmerrors.NewError().Status(http.StatusNotFound).Build()
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, notFoundErr)
				return mockCSClient
			},
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				assert.Error(t, err, "external auth should have been deleted")
			},
		},
		{
			name:                 "legacy approach: external auth still exists in cluster service keeps operation accepted",
			existingExternalAuth: fixture.newExternalAuth(),
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
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			operation := fixture.newOperation(database.OperationRequestDelete)
			// TODO remove this once the new deletion approach is fully rolled out in all ARO-HCP permanent environments, for all regions.
			operation.UsesNewExternalAuthDeletionApproach = tc.usesNewExternalAuthDeletionApproach

			resources := []any{fixture.newCluster(), operation}
			if tc.existingExternalAuth != nil {
				resources = append(resources, tc.existingExternalAuth)
			}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tc.setupCSMock != nil {
				mockCSClient = tc.setupCSMock(ctrl, fixture)
			}

			controller := &operationExternalAuthDelete{
				clock:                utilsclock.RealClock{},
				resourcesDBClient:    mockResourcesDBClient,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
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
