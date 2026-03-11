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

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationExternalAuthCreate_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *externalAuthTestFixture)
	}{
		{
			name: "external auth exists transitions to succeeded",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
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
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *externalAuthTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Verify external auth provisioning state was also updated
				externalAuth, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).ExternalAuth(testClusterName).Get(ctx, testExternalAuthName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, externalAuth.Properties.ProvisioningState)
				assert.Empty(t, externalAuth.ServiceProviderProperties.ActiveOperationID)
			},
		},
		{
			name: "external auth get error returns error",
			setupMock: func(ctrl *gomock.Controller, fixture *externalAuthTestFixture) ocm.ClusterServiceClientSpec {
				mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
				mockCSClient.EXPECT().
					GetExternalAuth(gomock.Any(), fixture.externalAuthInternalID).
					Return(nil, fmt.Errorf("cluster service error"))
				return mockCSClient
			},
			expectError: true,
			verify:      nil,
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
			operation := fixture.newOperation(database.OperationRequestCreate)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, externalAuth, operation})
			require.NoError(t, err)

			mockCSClient := tt.setupMock(ctrl, fixture)

			controller := &operationExternalAuthCreate{
				cosmosClient:         mockDB,
				clusterServiceClient: mockCSClient,
				notificationClient:   nil,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, fixture)
			}
		})
	}
}
