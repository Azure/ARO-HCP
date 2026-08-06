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
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRevokeCredentials_ShouldProcess(t *testing.T) {
	tests := []struct {
		name              string
		operationOverride func(*api.Operation)
		expectedResult    bool
	}{
		{
			name:              "Deleting status should be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateDeleting },
			expectedResult:    true,
		},
		{
			name:              "Terminal ProvisioningState should not be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateCanceled },
			expectedResult:    false,
		},
		{
			name:              "Wrong operation request type should not be processed",
			operationOverride: func(o *api.Operation) { o.Request = cosmosstorageutils.OperationRequestSystemAdminCredentialRequest },
			expectedResult:    false,
		},
		{
			name:              "ProvisioningStateAccepted should not be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateAccepted },
			expectedResult:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			fixture := operationtesting.NewClusterTestFixture()
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation)
			operation.Status = arm.ProvisioningStateDeleting
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			controller := &operationRevokeCredentials{}
			result := controller.ShouldProcess(ctx, operation)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestOperationRevokeCredentials_SynchronizeOperation(t *testing.T) {
	tests := []struct {
		name                         string
		operationOverride            func(*api.Operation)
		breakGlassCredentialStatuses []cmv1.BreakGlassCredentialStatus
		revokeCredentialsOperationID string
		expectError                  bool
		expectCSMockCalled           bool
		verify                       func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture)
	}{
		{
			name:                         "no credentials present means operation is successful",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{},
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  false,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
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
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  false,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
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
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  false,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.NotEmpty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name: "failed credential changes the operation status to failed",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{
				cmv1.BreakGlassCredentialStatusFailed,
			},
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  false,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateFailed, op.Status)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name:                         "mismatched RevokeCredentialsOperationID is left intact",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{},
			revokeCredentialsOperationID: "not-our-operation-id",
			expectError:                  false,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
				assert.Nil(t, op.Error)

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, "not-our-operation-id", cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
		},
		{
			name:                         "unhandled BreakGlassCredentialStatus leads to error",
			breakGlassCredentialStatuses: []cmv1.BreakGlassCredentialStatus{"CompleteFantasy"},
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  true,
			expectCSMockCalled:           true,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status) // no state change

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.RevokeCredentialsOperationID) // no state change
			},
		},
		{
			name:                         "ShouldProcess returns false for terminal status and no state change occurs",
			operationOverride:            func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			revokeCredentialsOperationID: operationtesting.TestOperationName,
			expectError:                  false,
			expectCSMockCalled:           false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient, fixture *operationtesting.ClusterTestFixture) {
				op, err := db.Operations(operationtesting.TestSubscriptionID).Get(ctx, operationtesting.TestOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status) // no state change

				cluster, err := db.HCPClusters(operationtesting.TestSubscriptionID, operationtesting.TestResourceGroupName).Get(ctx, operationtesting.TestClusterName)
				require.NoError(t, err)
				assert.Equal(t, operationtesting.TestOperationName, cluster.ServiceProviderProperties.RevokeCredentialsOperationID) // no state change
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
			operation := fixture.NewOperation(cosmosstorageutils.OperationRequestSystemAdminCredentialRevocation)
			operation.Status = arm.ProvisioningStateDeleting
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			mockResourcesDBClient, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, operation})
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)

			if tt.expectCSMockCalled {
				mockCSClient.EXPECT().
					ListBreakGlassCredentials(fixture.ClusterInternalID, "").
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
					}).MaxTimes(1)
			}

			controller := &operationRevokeCredentials{
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
