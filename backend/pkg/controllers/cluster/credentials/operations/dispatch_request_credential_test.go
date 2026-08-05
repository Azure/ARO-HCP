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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	operationtesting "github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils/operationtesting"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchRequestCredential_SyncrhonizeOperation(t *testing.T) {
	tests := []struct {
		name                         string
		revokeCredentialsOperationID string
		expectError                  bool
		verify                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture)
	}{
		{
			name:        "successful dispatch records a break-glass credential ID",
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestBreakGlassCredentialIDStr, op.InternalID.String())
			},
		},
		{
			name:                         "in-progress revocation cancels operation",
			revokeCredentialsOperationID: "test-revoke-operation-id",
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateCanceled, op.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			fixture := operationtesting.NewClusterTestFixture()
			cluster := fixture.NewCluster(nil)
			cluster.ServiceProviderProperties.RevokeCredentialsOperationID = tt.revokeCredentialsOperationID
			operation := fixture.NewOperation(database.OperationRequestSystemAdminCredentialRequest)
			operation.InternalID = api.InternalID{}

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if len(tt.revokeCredentialsOperationID) == 0 {
				breakGlassCredential, err := cmv1.NewBreakGlassCredential().
					HREF(operationtesting.TestBreakGlassCredentialIDStr).
					Build()
				require.NoError(t, err)

				mockCSClient.EXPECT().
					PostBreakGlassCredential(gomock.Any(), fixture.ClusterInternalID).
					Return(breakGlassCredential, nil)
			}

			controller := &dispatchRequestCredential{
				clock:                 utilsclock.RealClock{},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
			}

			err = controller.SynchronizeOperation(ctx, fixture.OperationKey())

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}
