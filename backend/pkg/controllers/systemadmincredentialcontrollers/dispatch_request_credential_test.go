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

package systemadmincredentialcontrollers

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchRequestCredential_ShouldProcess(t *testing.T) {
	syncer := &dispatchRequestCredential{
		clock: utilsclock.RealClock{},
	}

	tests := []struct {
		name     string
		op       *api.Operation
		expected bool
	}{
		{
			name: "terminal operation should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateSucceeded,
				Request: api.OperationRequestRequestCredential,
			},
			expected: false,
		},
		{
			name: "wrong request type should not process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: "Create",
			},
			expected: false,
		},
		{
			name: "already has InternalID should not process",
			op: &api.Operation{
				Status:     arm.ProvisioningStateAccepted,
				Request:    api.OperationRequestRequestCredential,
				InternalID: api.Must(api.NewInternalID("/api/clusters_mgmt/v1/clusters/abc123")),
			},
			expected: false,
		},
		{
			name: "valid RequestCredential operation should process",
			op: &api.Operation{
				Status:  arm.ProvisioningStateAccepted,
				Request: api.OperationRequestRequestCredential,
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := syncer.ShouldProcess(context.Background(), tc.op)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDispatchRequestCredential_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	tests := []struct {
		name        string
		resources   []any
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "creates credential and stamps operation InternalID",
			resources: []any{
				newTestCluster(""),
				newTestOperation("", api.OperationRequestRequestCredential),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Verify operation got InternalID stamped.
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.NotEmpty(t, op.InternalID.String(), "InternalID should be stamped")

				// Verify credential was created.
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 1, count, "should have exactly 1 credential")
			},
		},
		{
			name: "cancels operation when revocation is in progress",
			resources: []any{
				newTestCluster("some-revoke-op-id"),
				newTestOperation("", api.OperationRequestRequestCredential),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateCanceled, op.Status, "operation should be canceled")
			},
		},
		{
			name:        "operation not found returns nil",
			resources:   nil,
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tc.resources)
			require.NoError(t, err)

			syncer := &dispatchRequestCredential{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			key := controllerutils.OperationKey{
				SubscriptionID: testSubscriptionID,
				OperationName:  testOperationName,
			}

			err = syncer.SynchronizeOperation(ctx, key)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tc.verify != nil {
				tc.verify(t, ctx, db)
			}
		})
	}
}
