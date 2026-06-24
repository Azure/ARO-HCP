// Copyright 2025 Microsoft Corporation
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationSystemAdminRevoke_ShouldProcess(t *testing.T) {
	tests := []struct {
		name      string
		operation *api.Operation
		expected  bool
	}{
		{
			name: "should process RevokeCredentials in Deleting state",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateDeleting,
				Request: database.OperationRequestRevokeCredentials,
			},
			expected: true,
		},
		{
			name: "should not process Accepted state (dispatch handles)",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: database.OperationRequestRevokeCredentials,
			},
			expected: false,
		},
		{
			name: "should not process terminal operation",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateSucceeded,
				Request: database.OperationRequestRevokeCredentials,
			},
			expected: false,
		},
		{
			name: "should not process non-revoke request",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateDeleting,
				Request: database.OperationRequestCreate,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &operationSystemAdminRevoke{}
			result := controller.ShouldProcess(context.Background(), tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestOperationSystemAdminRevoke_NextOperationStatus(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	revokedAt := metav1.NewTime(now)

	tests := []struct {
		name           string
		credentials    []*api.SystemAdminCredential
		expectedStatus arm.ProvisioningState
		expectError    bool
		wantErr        bool
	}{
		{
			name:           "no credentials returns Succeeded",
			credentials:    nil,
			expectedStatus: arm.ProvisioningStateSucceeded,
		},
		{
			name: "all Revoked returns Succeeded",
			credentials: []*api.SystemAdminCredential{
				func() *api.SystemAdminCredential {
					c := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseRevoked)
					c.Status.RevokedAt = &revokedAt
					return c
				}(),
				func() *api.SystemAdminCredential {
					c := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred2bbb22222222", api.SystemAdminCredentialPhaseRevoked)
					c.Status.RevokedAt = &revokedAt
					return c
				}(),
			},
			expectedStatus: arm.ProvisioningStateSucceeded,
		},
		{
			name: "some AwaitingRevocation returns Deleting",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseAwaitingRevocation),
				func() *api.SystemAdminCredential {
					c := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred2bbb22222222", api.SystemAdminCredentialPhaseRevoked)
					c.Status.RevokedAt = &revokedAt
					return c
				}(),
			},
			expectedStatus: arm.ProvisioningStateDeleting,
		},
		{
			name: "any Failed returns Failed",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseFailed),
			},
			expectedStatus: arm.ProvisioningStateFailed,
			expectError:    true,
		},
		{
			name: "unexpected Requested phase returns Deleting",
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseRequested),
			},
			expectedStatus: arm.ProvisioningStateDeleting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			fixture := newClusterTestFixture()
			cluster := fixture.newCluster(nil)
			op := fixture.newOperation(database.OperationRequestRevokeCredentials)
			op.Status = arm.ProvisioningStateDeleting

			resources := []any{cluster, op}
			for _, cred := range tt.credentials {
				resources = append(resources, cred)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			controller := &operationSystemAdminRevoke{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockDB,
			}

			status, opErr, err := controller.nextOperationStatus(ctx, op)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, status)

			if tt.expectError {
				assert.NotNil(t, opErr)
			}
		})
	}
}

func TestOperationSystemAdminRevoke_SynchronizeOperation(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	revokedAt := metav1.NewTime(now)

	tests := []struct {
		name    string
		setup   func(fixture *clusterTestFixture) []any
		verify  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		wantErr bool
	}{
		{
			name: "all revoked transitions to Succeeded and clears cluster flag",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.Status = arm.ProvisioningStateDeleting

				cred := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseRevoked)
				cred.Status.RevokedAt = &revokedAt

				return []any{cluster, op, cred}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)

				// Cluster RevokeCredentialsOperationID should be cleared
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID)
			},
			wantErr: false,
		},
		{
			name: "awaiting revocation stays in Deleting",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.Status = arm.ProvisioningStateDeleting

				cred := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseAwaitingRevocation)

				return []any{cluster, op, cred}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				// Should stay at Deleting (no change needed)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			fixture := newClusterTestFixture()
			resources := tt.setup(fixture)

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			controller := &operationSystemAdminRevoke{
				clock:             clocktesting.NewFakePassiveClock(now),
				resourcesDBClient: mockDB,
			}

			err = controller.SynchronizeOperation(ctx, fixture.operationKey())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB)
			}
		})
	}
}
