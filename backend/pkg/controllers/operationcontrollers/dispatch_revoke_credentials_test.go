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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchRevokeCredentials_SyncrhonizeOperation(t *testing.T) {
	tests := []struct {
		name                         string
		revokeCredentialsOperationID string
		expectError                  bool
		verify                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name:                         "successful dispatch updates status to deleting",
			revokeCredentialsOperationID: testOperationName,
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
		{
			name:                         "mismatched revoke operation ID cancels operation",
			revokeCredentialsOperationID: "",
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
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

			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			cluster.ServiceProviderProperties.RevokeCredentialsOperationID = tt.revokeCredentialsOperationID
			operation := fixture.newOperation(database.OperationRequestRevokeCredentials)

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tt.revokeCredentialsOperationID == testOperationName {
				mockCSClient.EXPECT().
					DeleteBreakGlassCredentials(gomock.Any(), fixture.clusterInternalID)
			}

			controller := &dispatchRevokeCredentials{
				clock:                 utilsclock.RealClock{},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

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
