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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	utilsclock "k8s.io/utils/clock"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchRequestCredential_SyncrhonizeOperation(t *testing.T) {
	expiration := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	breakGlassID := api.Must(api.NewInternalID(testBreakGlassCredentialIDStr))
	breakGlassID2 := api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123/break_glass_credentials/bgc456"))

	tests := []struct {
		name                         string
		revokeCredentialsOperationID string
		seedResources                func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any
		setupMockCS                  func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *clusterTestFixture)
		expectError                  bool
		wantErrContain               string
		verify                       func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture)
	}{
		{
			name: "successful dispatch records a break-glass credential ID",
			seedResources: func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any {
				return []any{cluster, operation}
			},
			setupMockCS: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *clusterTestFixture) {
				breakGlassCredential, err := cmv1.NewBreakGlassCredential().
					HREF(testBreakGlassCredentialIDStr).
					ExpirationTimestamp(expiration).
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					PostBreakGlassCredential(gomock.Any(), fixture.clusterInternalID).
					Return(breakGlassCredential, nil)
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, testBreakGlassCredentialIDStr, op.InternalID.String())

				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.NoError(t, err)
				assert.Equal(t, op.OperationID.Name, cred.OperationID)
				assert.Equal(t, testBreakGlassCredentialIDStr, cred.ClusterServiceInternalID.String())
				assert.True(t, cred.ExpirationTimestamp.Equal(expiration), "expiration: got %v want %v", cred.ExpirationTimestamp, expiration)
			},
		},
		{
			name:                         "in-progress revocation cancels operation",
			revokeCredentialsOperationID: "test-revoke-operation-id",
			seedResources: func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any {
				return []any{cluster, operation}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateCanceled, op.Status)
			},
		},
		{
			name: "retry finds existing admin credential and skips CS POST",
			seedResources: func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any {
				cred, err := database.NewClusterAdminCredential(cluster.ID, breakGlassID, operation.OperationID.Name)
				require.NoError(t, err)
				cred.Status = api.ClusterAdminCredentialStatusCreated
				cred.ExpirationTimestamp = expiration
				return []any{cluster, operation, cred}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, testBreakGlassCredentialIDStr, op.InternalID.String())
			},
		},
		{
			name: "create conflict returns existing admin credential",
			seedResources: func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any {
				// Existing doc shares the CS credential name but belongs to a different operation,
				// so find-by-operation-name misses it and Create hits a conflict.
				cred, err := database.NewClusterAdminCredential(cluster.ID, breakGlassID, "other-operation-id")
				require.NoError(t, err)
				cred.Status = api.ClusterAdminCredentialStatusCreated
				cred.ExpirationTimestamp = expiration
				return []any{cluster, operation, cred}
			},
			setupMockCS: func(t *testing.T, mock *ocm.MockClusterServiceClientSpec, fixture *clusterTestFixture) {
				breakGlassCredential, err := cmv1.NewBreakGlassCredential().
					HREF(testBreakGlassCredentialIDStr).
					ExpirationTimestamp(expiration).
					Build()
				require.NoError(t, err)
				mock.EXPECT().
					PostBreakGlassCredential(gomock.Any(), fixture.clusterInternalID).
					Return(breakGlassCredential, nil)
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, fixture *clusterTestFixture) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, testBreakGlassCredentialIDStr, op.InternalID.String())

				cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).AdminCredentials(testClusterName).Get(ctx, "bgc123")
				require.NoError(t, err)
				assert.Equal(t, "other-operation-id", cred.OperationID)
			},
		},
		{
			name: "multiple admin credentials for the same operation returns error",
			seedResources: func(t *testing.T, fixture *clusterTestFixture, cluster *api.HCPOpenShiftCluster, operation *api.Operation) []any {
				cred1, err := database.NewClusterAdminCredential(cluster.ID, breakGlassID, operation.OperationID.Name)
				require.NoError(t, err)
				cred2, err := database.NewClusterAdminCredential(cluster.ID, breakGlassID2, operation.OperationID.Name)
				require.NoError(t, err)
				return []any{cluster, operation, cred1, cred2}
			},
			expectError:    true,
			wantErrContain: "multiple ClusterAdminCredential docs found",
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
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			operation.InternalID = api.InternalID{}

			resources := tt.seedResources(t, fixture, cluster, operation)
			mockResourcesDBClient, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockCSClient := ocm.NewMockClusterServiceClientSpec(ctrl)
			if tt.setupMockCS != nil {
				tt.setupMockCS(t, mockCSClient, fixture)
			}

			controller := &dispatchRequestCredential{
				clock:                 utilsclock.RealClock{},
				resourcesDBClient:     mockResourcesDBClient,
				clustersServiceClient: mockCSClient,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.expectError {
				require.Error(t, err)
				if tt.wantErrContain != "" {
					assert.Contains(t, err.Error(), tt.wantErrContain)
				}
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockResourcesDBClient, fixture)
			}
		})
	}
}

func TestDispatchRequestCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name              string
		operationOverride func(*api.Operation)
		expectedResult    bool
	}{
		{
			name:              "Accepted status with empty InternalID should be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateAccepted },
			expectedResult:    true,
		},
		{
			name:              "Terminal status should not be processed",
			operationOverride: func(o *api.Operation) { o.Status = arm.ProvisioningStateSucceeded },
			expectedResult:    false,
		},
		{
			name:              "Wrong request type should not be processed",
			operationOverride: func(o *api.Operation) { o.Request = database.OperationRequestRevokeCredentials },
			expectedResult:    false,
		},
		{
			name: "Already linked InternalID should not be processed",
			operationOverride: func(o *api.Operation) {
				o.InternalID = api.Must(api.NewInternalID(testBreakGlassCredentialIDStr))
			},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			fixture := newClusterTestFixture()
			operation := fixture.newOperation(database.OperationRequestRequestCredential)
			operation.Status = arm.ProvisioningStateAccepted
			operation.InternalID = api.InternalID{}
			if tt.operationOverride != nil {
				tt.operationOverride(operation)
			}

			controller := &dispatchRequestCredential{}
			assert.Equal(t, tt.expectedResult, controller.ShouldProcess(ctx, operation))
		})
	}
}
