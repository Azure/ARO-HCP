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

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRevokeCredentials_SyncrhonizeOperation(t *testing.T) {
	tests := []struct {
		name                         string
		breakGlassCredentialStatuses []cmv1.BreakGlassCredentialStatus
		revokeCredentialsOperationID string
		expectError                  bool
		verify                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name:                         "no credentials present means operation is successful",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{},
			revokeCredentialsOperationID: testOperationName,
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name: "all revoked or expired credentials means operation is successful",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{
				cmv1.BreakGlassCredentialStatusExpired,
				cmv1.BreakGlassCredentialStatusRevoked,
			},
			revokeCredentialsOperationID: testOperationName,
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name: "credential awaiting revocation does not change operation status",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{
				cmv1.BreakGlassCredentialStatusExpired,
				cmv1.BreakGlassCredentialStatusRevoked,
				cmv1.BreakGlassCredentialStatusAwaitingRevocation,
			},
			revokeCredentialsOperationID: testOperationName,
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.NotEmpty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name: "failed credential changes the operation status to failed",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{
				cmv1.BreakGlassCredentialStatusFailed,
			},
			revokeCredentialsOperationID: testOperationName,
			expectError:                  false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name:                         "mismatched RevokeCredentialsOperationID is left intact",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{},
			revokeCredentialsOperationID: "not-our-operation-id",
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Equal(t, "not-our-operation-id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
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
			operation.Status = arm.ProvisioningStateDeleting

			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			mockCSClient.EXPECT().
				ListBreakGlassCredentials(fixture.clusterInternalID, "").
				DoAndReturn(func(_ ocm.InternalID, _ string) ocm.BreakGlassCredentialListIterator {
					var objs []*cmv1.BreakGlassCredential
					for _, status := range tt.breakGlassCredentialStatuses {
						breakGlassCredential, err := cmv1.NewBreakGlassCredential().
							Status(status).
							Build()
						require.NoError(t, err)
						objs = append(objs, breakGlassCredential)
					}
					return ocm.NewSimpleBreakGlassCredentialsListIterator(objs, nil)
				})

			controller := &operationRevokeCredentials{
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
