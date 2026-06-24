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
	"strings"
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

func TestDispatchSystemAdminRevoke_ShouldProcess(t *testing.T) {
	tests := []struct {
		name      string
		operation *api.Operation
		expected  bool
	}{
		{
			name: "should process RevokeCredentials in Accepted state",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: database.OperationRequestRevokeCredentials,
			},
			expected: true,
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
				Status:  arm.ProvisioningStateAccepted,
				Request: database.OperationRequestCreate,
			},
			expected: false,
		},
		{
			name: "should not process Deleting status (poll controller handles)",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateDeleting,
				Request: database.OperationRequestRevokeCredentials,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &dispatchSystemAdminRevoke{}
			result := controller.ShouldProcess(context.Background(), tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func newTestCredential(subscriptionID, rgName, clusterName, credName string, phase api.SystemAdminCredentialPhase) *api.SystemAdminCredential {
	credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(subscriptionID, rgName, clusterName, credName))
	cred := &api.SystemAdminCredential{}
	cred.SetResourceID(credResourceID)
	cred.SetPartitionKey(strings.ToLower(subscriptionID))
	cred.Spec = api.SystemAdminCredentialSpec{
		Username:            "system-admin",
		OperationID:         "some-op",
		ExpirationTimestamp: metav1.NewTime(time.Date(2025, 6, 16, 10, 0, 0, 0, time.UTC)),
		PublicKeyPEM:        "test-public-key",
		PrivateKeyPEM:       "test-private-key",
	}
	cred.Status = api.SystemAdminCredentialStatus{
		Phase: phase,
	}
	return cred
}

func TestDispatchSystemAdminRevoke_SynchronizeOperation(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(fixture *clusterTestFixture) []any
		verify  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		wantErr bool
	}{
		{
			name: "marks active credentials for revocation and moves to Deleting",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.InternalID = api.InternalID{}

				cred1 := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseRequested)
				cred2 := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred2bbb22222222", api.SystemAdminCredentialPhaseIssued)

				return []any{cluster, op, cred1, cred2}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Operation should be Deleting
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				// Both credentials should be AwaitingRevocation
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseAwaitingRevocation, cred.Status.Phase,
						"credential %s should be AwaitingRevocation", cred.GetResourceID().Name)
					assert.NotNil(t, cred.Status.RevokedAt, "RevokedAt should be set")
				}
				require.NoError(t, credIter.GetError())
			},
			wantErr: false,
		},
		{
			name: "cancels operation when RevokeCredentialsOperationID mismatches",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = "different-operation-id"

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.InternalID = api.InternalID{}

				return []any{cluster, op}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.True(t, op.Status.IsTerminal(), "operation should be terminal (canceled)")
			},
			wantErr: false,
		},
		{
			name: "handles no credentials gracefully",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.InternalID = api.InternalID{}

				return []any{cluster, op}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
			wantErr: false,
		},
		{
			name: "skips already revoked credentials",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName

				op := fixture.newOperation(database.OperationRequestRevokeCredentials)
				op.InternalID = api.InternalID{}

				revokedAt := metav1.NewTime(now.Add(-1 * time.Hour))
				cred := newTestCredential(testSubscriptionID, testResourceGroupName, testClusterName, "cred1aaa11111111", api.SystemAdminCredentialPhaseRevoked)
				cred.Status.RevokedAt = &revokedAt

				return []any{cluster, op, cred}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)

				// Credential should still be Revoked (not changed)
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Equal(t, api.SystemAdminCredentialPhaseRevoked, cred.Status.Phase)
				}
				require.NoError(t, credIter.GetError())
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

			controller := &dispatchSystemAdminRevoke{
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
