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

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/listertesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationExternalAuthCreate_SynchronizeOperation(t *testing.T) {
	defaultExternalAuth := func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth {
		return fixture.newExternalAuth()
	}

	externalAuthWithoutCSID := func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth {
		ea := fixture.newExternalAuth()
		ea.ServiceProviderProperties.ClusterServiceID = nil
		return ea
	}

	externalAuthWithDeletionTimestamp := func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth {
		ea := fixture.newExternalAuth()
		ea.ServiceProviderProperties.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		return ea
	}

	externalAuthWithMismatchedActiveOperationID := func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth {
		ea := fixture.newExternalAuth()
		ea.ServiceProviderProperties.ActiveOperationID = "other-operation"
		return ea
	}

	externalAuthWithEmptyActiveOperationID := func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth {
		ea := fixture.newExternalAuth()
		ea.ServiceProviderProperties.ActiveOperationID = ""
		return ea
	}

	tests := []struct {
		name         string
		externalAuth func(fixture *externalAuthTestFixture) *api.HCPOpenShiftClusterExternalAuth
		setupCSMock  func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
		expectError  bool
		verify       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture)
	}{
		{
			name:         "external auth exists transitions to succeeded",
			externalAuth: defaultExternalAuth,
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
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, externalAuth.Properties.ProvisioningState)
				assert.Empty(t, externalAuth.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name:         "external auth get error returns error",
			externalAuth: defaultExternalAuth,
			setupCSMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, fmt.Errorf("cluster service error"))
				return mockCSClient
			},
			expectError: true,
			verify:      nil,
		},
		{
			name:         "ClusterServiceID nil skips reconciliation",
			externalAuth: externalAuthWithoutCSID,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:         "ActiveOperationID mismatch skips reconciliation",
			externalAuth: externalAuthWithMismatchedActiveOperationID,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:         "empty ActiveOperationID skips reconciliation",
			externalAuth: externalAuthWithEmptyActiveOperationID,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name:         "DeletionTimestamp set skips reconciliation",
			externalAuth: externalAuthWithDeletionTimestamp,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *externalAuthTestFixture) {
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
			externalAuth := tt.externalAuth(fixture)
			operation := fixture.newOperation(database.OperationRequestCreate)

			resources := []any{cluster, externalAuth, operation}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			var mockCSClient ocm.ClusterServiceClientSpec
			if tt.setupCSMock != nil {
				mockCSClient = tt.setupCSMock(ctrl, fixture)
			} else {
				mockCSClient = ocm.NewMockClusterServiceClientSpec(ctrl)
			}

			controller := &operationExternalAuthCreate{
				clock:                  utilsclock.RealClock{},
				resourcesDBClient:      mockResourcesDBClient,
				activeOperationsLister: &listertesting.DBActiveOperationLister{ResourcesDBClient: mockResourcesDBClient},
				externalAuthLister:     &listertesting.DBExternalAuthLister{ResourcesDBClient: mockResourcesDBClient},
				clusterServiceClient:   mockCSClient,
				notificationClient:     nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
