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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevokeDispatchCompletionRecovery(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	clock := clocktesting.NewFakeClock(time.Unix(100, 0))
	op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
	deadline := clock.Now().Add(time.Minute)
	op.CompletionDeadline = &deadline
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op})
	require.NoError(t, err)
	c := &dispatchRevokeCredentials{clock: clock, resourcesDBClient: db}
	key := controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}
	require.NoError(t, c.SynchronizeOperation(ctx, key), "dispatch does not depend on cluster availability")
	live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	rid := live.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID
	// Model interruption before the link persisted. Teardown must recover it
	// before the internal document disappears, without reviving ARM monitoring.
	live.Status = coreapi.ProvisioningStateAccepted
	live.SystemAdminCredentialRevocation = nil
	_, err = db.Operations(testSubscriptionID).Replace(ctx, live, nil)
	require.NoError(t, err)
	clock.Step(time.Minute)
	require.NoError(t, c.SynchronizeOperation(ctx, key))
	require.NoError(t, operationutils.CompleteCredentialIntent(ctx, db, rid, testOperationName, true))
	require.NoError(t, db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName).Delete(ctx, rid.Name))
	require.NoError(t, c.SynchronizeOperation(ctx, key))
	live, err = db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
	require.True(t, live.SystemAdminCredentialRevocation.ReconciliationComplete)
	require.Equal(t, rid.String(), live.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID.String())
}

func TestDispatchRevokeCredentials_ShouldProcess(t *testing.T) {
	syncer := &dispatchRevokeCredentials{
		clock: utilsclock.RealClock{},
	}

	tests := []struct {
		name     string
		op       *coreapi.Operation
		expected bool
	}{
		{
			name: "terminal operation should not process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateSucceeded,
				Request: coreapi.OperationRequestSystemAdminCredentialRevocation,
			},
			expected: false,
		},
		{
			name: "wrong request type should not process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: "Create",
			},
			expected: false,
		},
		{
			name: "non-Accepted status should not process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateDeleting,
				Request: coreapi.OperationRequestSystemAdminCredentialRevocation,
			},
			expected: false,
		},
		{
			name: "valid RevokeCredentials operation in Accepted should process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: coreapi.OperationRequestSystemAdminCredentialRevocation,
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

func TestDispatchRevokeCredentials_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	tests := []struct {
		name        string
		resources   []any
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:        "operation not found returns nil",
			resources:   nil,
			expectError: false,
		},
		{
			name: "operation queues internal intent behind another sentinel",
			resources: []any{
				newTestCluster("different-op-id"),
				newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status, "queued operation must be dispatched")
				assert.NotNil(t, op.SystemAdminCredentialRevocation, "internal intent must survive operation expiry")
			},
		},
		{
			name: "successful dispatch creates revocation and moves operation to Deleting",
			resources: []any{
				newTestCluster(testOperationName),
				newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status, "operation should transition to Deleting")
				require.NotNil(t, op.SystemAdminCredentialRevocation, "SystemAdminCredentialRevocation should be set")
				assert.NotNil(t, op.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID, "revocation resource ID should be stamped")

				// Verify the revocation document was created.
				revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
				iter, err := revocationCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 1, count, "should have exactly 1 revocation document")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tc.resources)
			require.NoError(t, err)

			var clusters []*coreapi.Cluster
			for _, r := range tc.resources {
				if c, ok := r.(*coreapi.Cluster); ok {
					clusters = append(clusters, c)
				}
			}
			syncer := &dispatchRevokeCredentials{
				clock:             fakeClock,
				resourcesDBClient: db,
				clusterLister:     &corelistertesting.SliceClusterLister{Clusters: clusters},
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
