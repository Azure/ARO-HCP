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
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRequestCredential_SyncrhonizeOperation(t *testing.T) {
	tests := []struct {
		name                       string
		breakGlassCredentialStatus cmv1.BreakGlassCredentialStatus
		getBreakGlassCredentialErr error
		expectError                bool
		verify                     func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture)
	}{
		{
			name:                       "created credential updates operation status to provisioning",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusCreated,
			expectError:                false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateProvisioning, op.Status)
			},
		},
		{
			name:                       "failed credential updates operation status to failed",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusFailed,
			expectError:                false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)
				assert.Equal(t, arm.CloudErrorCodeInternalServerError, op.Error.Code)
			},
		},
		{
			name:                       "issued credential updates operation status to succeeded",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			expectError:                false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name:                       "unhandled BreakGlassCredentialStatus leads to error",
			breakGlassCredentialStatus: "CompleteFantasy",
			expectError:                true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
		{
			name:                       "GetBreakGlassCredential failure leads to error",
			breakGlassCredentialStatus: cmv1.BreakGlassCredentialStatusIssued,
			getBreakGlassCredentialErr: errors.New("Something went wrong!"),
			expectError:                true,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status) // no state change
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			operation := fixture.newOperation(database.OperationRequestRequestCredential)

			mockDB, err := databasetesting.NewMockDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			breakGlassCredential, err := cmv1.NewBreakGlassCredential().
				Status(tt.breakGlassCredentialStatus).
				Build()
			require.NoError(t, err)

			mockCSClient.EXPECT().
				GetBreakGlassCredential(gomock.Any(), fixture.clusterInternalID).
				Return(breakGlassCredential, tt.getBreakGlassCredentialErr)

			controller := &operationRequestCredential{
				cosmosClient:          mockDB,
				clustersServiceClient: mockCSClient,
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
