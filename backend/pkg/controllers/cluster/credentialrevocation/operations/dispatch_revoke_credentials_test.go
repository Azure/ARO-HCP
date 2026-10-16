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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevokeDispatchIdentityRecovery(t *testing.T) {
	ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
	clock := clocktesting.NewFakeClock(time.Unix(100, 0))
	op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
	op.StartTime = clock.Now()
	deadline := clock.Now().Add(time.Minute)
	op.CompletionDeadline = &deadline
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, newTestCluster(testOperationName)})
	require.NoError(t, err)
	c := &dispatchRevokeCredentials{clock: clock, resourcesDBClient: db}
	key := controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}
	require.NoError(t, c.SynchronizeOperation(ctx, key), "legacy dispatch reserves identity first")
	live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	rid := live.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID
	require.Equal(t, coreapi.ProvisioningStateAccepted, live.Status)
	revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
	_, err = revocationCRUD.Get(ctx, rid.Name)
	require.True(t, cosmosstorageutils.IsNotFoundError(err), "reserve identity before creating intent")
	poller := &operationRevokeCredentialsPoll{clock: clock, resourcesDBClient: db}
	require.NoError(t, poller.SynchronizeOperation(ctx, key), "reserved identity must not imply completed revocation")
	live, err = db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateAccepted, live.Status)
	c = &dispatchRevokeCredentials{clock: clock, resourcesDBClient: db}
	require.NoError(t, c.SynchronizeOperation(ctx, key), "normalize legacy cluster reservation")
	require.NoError(t, c.SynchronizeOperation(ctx, key))
	revocation, err := revocationCRUD.Get(ctx, rid.Name)
	require.NoError(t, err)
	require.Equal(t, testOperationName, revocation.Spec.OperationID)
	clock.Step(time.Minute)
	require.NoError(t, poller.SynchronizeOperation(ctx, key))
	require.NoError(t, db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName).Delete(ctx, rid.Name))
	require.NoError(t, c.SynchronizeOperation(ctx, key))
	require.NoError(t, poller.SynchronizeOperation(ctx, key))
	live, err = db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
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

func TestReservedRevokeDispatchWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		age      time.Duration
		lost     bool
		deadline bool
		existing bool
		failed   bool
	}{
		{name: "partial reservation recovers", age: 29 * time.Minute},
		{name: "exact grace", age: 30 * time.Minute, failed: true},
		{name: "past grace", age: 31 * time.Minute, failed: true},
		{name: "reservation lost", age: time.Minute, lost: true, failed: true},
		{name: "ARM deadline", age: 30 * time.Minute, deadline: true, failed: true},
		{name: "existing work survives grace expiry", age: 31 * time.Minute, existing: true},
		{name: "existing work survives ARM expiry", age: 31 * time.Minute, existing: true, deadline: true, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(t.Context(), testr.New(t))
			clk := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
			op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
			op.StartTime = clk.Now()
			rid := metadataapi.Must(coreapihelpers.SystemAdminCredentialRevocationID(op.ExternalID, op.OperationID.Name))
			op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{SystemAdminCredentialRevocationResourceID: rid}
			if tc.deadline {
				deadline := clk.Now().Add(coreapi.SystemAdminCredentialRevocationTimeout)
				op.CompletionDeadline = &deadline
			}
			cluster := newTestCluster(testOperationName)
			stamp := metav1.NewTime(clk.Now())
			cluster.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt = &stamp
			cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = rid
			if tc.lost {
				cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = metadataapi.Must(coreapihelpers.SystemAdminCredentialRevocationID(op.ExternalID, "other"))
			}
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, cluster})
			require.NoError(t, err)
			crud := db.HCPClusters(testSubscriptionID, testResourceGroupName)
			revocations := crud.SystemAdminCredentialRevocations(testClusterName)
			var existing *coreapi.SystemAdminCredentialRevocation
			if tc.existing {
				existing, err = revocations.Create(ctx, &coreapi.SystemAdminCredentialRevocation{
					CosmosMetadata: coreapi.CosmosMetadata{ResourceID: rid, PartitionKey: testSubscriptionID},
					Spec:           coreapi.SystemAdminCredentialRevocationSpec{OperationID: testOperationName, RevokeOpSuffix: rid.Name},
				}, nil)
				require.NoError(t, err)
			}
			before, err := crud.Get(ctx, testClusterName)
			require.NoError(t, err)
			clk.Step(tc.age)
			c := &dispatchRevokeCredentials{clock: clk, resourcesDBClient: db}
			require.NoError(t, c.SynchronizeOperation(ctx, controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}))
			live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)
			revocation, err := revocations.Get(ctx, rid.Name)
			if tc.failed && !tc.existing {
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
			} else {
				require.NoError(t, err)
			}
			if tc.existing {
				require.Equal(t, existing, revocation, "existing work is never rewritten by dispatch or expiry")
			}
			after, err := crud.Get(ctx, testClusterName)
			require.NoError(t, err)
			require.Equal(t, before, after, "dispatch and expiry do not alter the reservation")
			if tc.failed {
				require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
				require.Equal(t, "DeadlineExceeded", live.Error.Code)
			} else {
				require.NoError(t, err)
				require.Equal(t, coreapi.ProvisioningStateDeleting, live.Status)
				require.Nil(t, live.CompletionDeadline, "legacy nil deadline is never backfilled")
			}
		})
	}
}

func TestRevokeDispatchMissingReservationTime(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sentinel  string
		age       time.Duration
		zeroStart bool
	}{
		{name: "empty sentinel waits", age: time.Hour},
		{name: "different sentinel waits", sentinel: "other", age: time.Hour},
		{name: "matching sentinel without original time waits", sentinel: testOperationName, zeroStart: true},
		{name: "matching legacy recovers original time within grace", sentinel: testOperationName, age: 29 * time.Minute},
		{name: "matching legacy recovers original time beyond grace", sentinel: testOperationName, age: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			clk := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
			op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
			op.StartTime = clk.Now().Add(-tc.age)
			if tc.zeroStart {
				op.StartTime = time.Time{}
			}
			rid := metadataapi.Must(coreapihelpers.SystemAdminCredentialRevocationID(op.ExternalID, op.OperationID.Name))
			op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{SystemAdminCredentialRevocationResourceID: rid}
			cluster := newTestCluster(tc.sentinel)
			cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = rid
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, cluster})
			require.NoError(t, err)
			crud := db.HCPClusters(testSubscriptionID, testResourceGroupName)
			before, err := crud.Get(ctx, testClusterName)
			require.NoError(t, err)
			c := &dispatchRevokeCredentials{clock: clk, resourcesDBClient: db}
			key := controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}
			require.NoError(t, c.SynchronizeOperation(ctx, key))
			_, err = crud.SystemAdminCredentialRevocations(testClusterName).Get(ctx, rid.Name)
			require.True(t, cosmosstorageutils.IsNotFoundError(err), "unknown age never creates missing work")
			live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateAccepted, live.Status)
			require.Nil(t, live.Error, "unknown age is not DeadlineExceeded")
			require.Nil(t, live.CompletionDeadline, "legacy deadline is never backfilled")
			after, err := crud.Get(ctx, testClusterName)
			require.NoError(t, err)
			if tc.sentinel == testOperationName && !tc.zeroStart {
				require.NotNil(t, after.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt)
				require.True(t, op.StartTime.Equal(after.ServiceProviderProperties.SystemAdminCredentialRevocationRequestedAt.Time), "migration must not restart grace")
				require.NoError(t, c.SynchronizeOperation(ctx, key))
				live, err = db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				_, err = crud.SystemAdminCredentialRevocations(testClusterName).Get(ctx, rid.Name)
				if tc.age < coreapi.SystemAdminCredentialRevocationTimeout {
					require.NoError(t, err)
					require.Equal(t, coreapi.ProvisioningStateDeleting, live.Status)
				} else {
					require.True(t, cosmosstorageutils.IsNotFoundError(err))
					require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
					require.Equal(t, "DeadlineExceeded", live.Error.Code)
				}
				require.Nil(t, live.CompletionDeadline)
			} else {
				require.Equal(t, before, after, "unrecoverable time leaves reservation untouched")
			}
		})
	}
}

func TestRevokeDispatchAdoptsReservedDocument(t *testing.T) {
	ctx := t.Context()
	op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
	id := metadataapi.Must(coreapihelpers.ToSystemAdminCredentialRevocationResourceID(testSubscriptionID, testResourceGroupName, testClusterName, "reserved-name"))
	op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{SystemAdminCredentialRevocationResourceID: id}
	revocation := &coreapi.SystemAdminCredentialRevocation{
		CosmosMetadata: coreapi.CosmosMetadata{ResourceID: id, PartitionKey: testSubscriptionID},
		Spec:           coreapi.SystemAdminCredentialRevocationSpec{OperationID: testOperationName, RevokeOpSuffix: id.Name},
	}
	db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, revocation})
	require.NoError(t, err)
	c := &dispatchRevokeCredentials{clock: clocktesting.NewFakeClock(time.Unix(100, 0)), resourcesDBClient: db}
	require.NoError(t, c.SynchronizeOperation(ctx, controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}))
	live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateDeleting, live.Status, "recover crash after create before phase")
	require.Equal(t, id.String(), live.SystemAdminCredentialRevocation.SystemAdminCredentialRevocationResourceID.String())
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
			name: "operation cannot dispatch behind another sentinel",
			resources: []any{
				newTestCluster("different-op-id"),
				newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
				assert.Equal(t, "DeadlineExceeded", op.Error.Code)
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
			for _, r := range tc.resources {
				if op, ok := r.(*coreapi.Operation); ok {
					live, err := db.Operations(testSubscriptionID).Get(ctx, op.OperationID.Name)
					require.NoError(t, err)
					live.StartTime = fixedTime
					_, err = db.Operations(testSubscriptionID).Replace(ctx, live, nil)
					require.NoError(t, err)
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
				require.NoError(t, syncer.SynchronizeOperation(ctx, key))
				require.NoError(t, syncer.SynchronizeOperation(ctx, key))
			}

			if tc.verify != nil {
				tc.verify(t, ctx, db)
			}
		})
	}
}
