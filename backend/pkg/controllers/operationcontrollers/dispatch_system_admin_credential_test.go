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

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchSystemAdminCredential_ShouldProcess(t *testing.T) {
	tests := []struct {
		name      string
		operation *api.Operation
		expected  bool
	}{
		{
			name: "should process RequestCredential with empty InternalID",
			operation: &api.Operation{
				Status:     arm.ProvisioningStateAccepted,
				Request:    database.OperationRequestRequestCredential,
				InternalID: api.InternalID{},
			},
			expected: true,
		},
		{
			name: "should not process terminal operation",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateSucceeded,
				Request: database.OperationRequestRequestCredential,
			},
			expected: false,
		},
		{
			name: "should not process non-credential request",
			operation: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: database.OperationRequestCreate,
			},
			expected: false,
		},
		{
			name: "should not process when InternalID is set",
			operation: &api.Operation{
				Status:     arm.ProvisioningStateAccepted,
				Request:    database.OperationRequestRequestCredential,
				InternalID: api.Must(api.NewInternalID(api.ToSystemAdminCredentialResourceIDString(testSubscriptionID, testResourceGroupName, testClusterName, "somecred12345678"))),
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &dispatchSystemAdminCredential{}
			result := controller.ShouldProcess(context.Background(), tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDispatchSystemAdminCredential_SynchronizeOperation(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(fixture *clusterTestFixture) []any
		verify  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		wantErr bool
	}{
		{
			name: "creates credential and stamps operation InternalID",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				op := fixture.newOperation(database.OperationRequestRequestCredential)
				op.InternalID = api.InternalID{} // empty for dispatch
				return []any{cluster, op}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Operation should now have InternalID set
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.NotEmpty(t, op.InternalID.String())
				assert.Contains(t, strings.ToLower(op.InternalID.String()), "systemadmincredentials")

				// Credential doc should exist
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for _, cred := range credIter.Items(ctx) {
					count++
					assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
					assert.NotEmpty(t, cred.Spec.PublicKeyPEM)
					assert.NotEmpty(t, cred.Spec.PrivateKeyPEM)
					assert.Equal(t, testOperationName, cred.Spec.OperationID)
					assert.Equal(t, "system-admin", cred.Spec.Username)
				}
				require.NoError(t, credIter.GetError())
				assert.Equal(t, 1, count)
			},
			wantErr: false,
		},
		{
			name: "cancels operation when revocation is in progress",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = "some-revoke-op"
				op := fixture.newOperation(database.OperationRequestRequestCredential)
				op.InternalID = api.InternalID{} // empty for dispatch
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
			name: "idempotent when credential already exists for operation",
			setup: func(fixture *clusterTestFixture) []any {
				cluster := fixture.newCluster(nil)
				op := fixture.newOperation(database.OperationRequestRequestCredential)
				op.InternalID = api.InternalID{} // empty for dispatch

				// Pre-existing credential for this operation
				credResourceID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, "existingcred1234"))
				cred := &api.SystemAdminCredential{}
				cred.SetResourceID(credResourceID)
				cred.SetPartitionKey(strings.ToLower(testSubscriptionID))
				cred.Spec.OperationID = testOperationName
				cred.Status.Phase = api.SystemAdminCredentialPhaseRequested

				return []any{cluster, op, cred}
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Contains(t, strings.ToLower(op.InternalID.String()), "existingcred1234")

				// Should not have created a second credential
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for range credIter.Items(ctx) {
					count++
				}
				require.NoError(t, credIter.GetError())
				assert.Equal(t, 1, count)
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

			controller := &dispatchSystemAdminCredential{
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
