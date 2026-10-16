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
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestDispatchRequestCredential_ShouldProcess(t *testing.T) {
	syncer := &dispatchRequestCredential{
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
				Request: coreapi.OperationRequestSystemAdminCredentialRequest,
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
			name: "already has credential resource ID should not process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: coreapi.OperationRequestSystemAdminCredentialRequest,
				SystemAdminCredentialRequest: &coreapi.OperationSystemAdminCredentialRequest{
					SystemAdminCredentialRequestResourceID: metadataapi.Must(azcorearm.ParseResourceID(
						"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/cluster/systemAdminCredentialRequests/cred")),
				},
			},
			expected: false,
		},
		{
			name: "nil SystemAdminCredentialRequest should not process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: coreapi.OperationRequestSystemAdminCredentialRequest,
			},
			expected: false,
		},
		{
			name: "valid RequestCredential operation should process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: coreapi.OperationRequestSystemAdminCredentialRequest,
				SystemAdminCredentialRequest: &coreapi.OperationSystemAdminCredentialRequest{
					CertificateSigningRequest: "-----BEGIN CERTIFICATE REQUEST-----\ntest\n-----END CERTIFICATE REQUEST-----",
				},
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

func TestCredentialDispatchExpiryAndRecovery(t *testing.T) {
	for _, expiredBeforeDispatch := range []bool{true, false} {
		t.Run(fmt.Sprint(expiredBeforeDispatch), func(t *testing.T) {
			ctx := t.Context()
			clock := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
			op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRequest)
			deadline := clock.Now().Add(time.Minute)
			op.CompletionDeadline = &deadline
			cluster := newTestCluster("")
			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{op, cluster})
			require.NoError(t, err)
			c := &dispatchRequestCredential{clock: clock, resourcesDBClient: db, clusterLister: &corelistertesting.SliceClusterLister{Clusters: []*coreapi.Cluster{cluster}}}
			key := controllerutils.OperationKey{SubscriptionID: testSubscriptionID, OperationName: testOperationName}
			if expiredBeforeDispatch {
				clock.Step(time.Minute)
			}
			require.NoError(t, c.SynchronizeOperation(ctx, key))
			live, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)
			if expiredBeforeDispatch {
				require.Equal(t, coreapi.ProvisioningStateFailed, live.Status)
				require.Nil(t, live.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID)
				return
			}
			id := live.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID
			require.NotNil(t, id)
			// Simulate a lost operation link, with the internal document surviving.
			live.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID = nil
			_, err = db.Operations(testSubscriptionID).Replace(ctx, live, nil)
			require.NoError(t, err)
			clock.Step(time.Minute)
			require.NoError(t, c.SynchronizeOperation(ctx, key))
			require.NoError(t, operationutils.LinkCredentialIntent(ctx, db, id, testOperationName, false))
			live, err = db.Operations(testSubscriptionID).Get(ctx, testOperationName)
			require.NoError(t, err)
			require.Equal(t, coreapi.ProvisioningStateFailed, live.Status, "link recovery must not resurrect monitoring")
			require.Equal(t, id.String(), live.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID.String())
			require.NoError(t, c.SynchronizeOperation(ctx, key))
			iter, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).List(ctx, nil)
			require.NoError(t, err)
			count := 0
			for range iter.Items(ctx) {
				count++
			}
			require.NoError(t, iter.GetError())
			require.Equal(t, 1, count)
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
		verify      func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name: "creates credential and stamps credential resource ID",
			resources: []any{
				newTestCluster(""),
				newTestOperation(coreapi.OperationRequestSystemAdminCredentialRequest),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				require.NotNil(t, op.SystemAdminCredentialRequest, "SystemAdminCredentialRequest should be set")
				assert.NotNil(t, op.SystemAdminCredentialRequest.SystemAdminCredentialRequestResourceID, "SystemAdminCredentialRequestResourceID should be stamped")

				// Verify credential was created.
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
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
				newTestOperation(coreapi.OperationRequestSystemAdminCredentialRequest),
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateCanceled, op.Status, "operation should be canceled")
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

			db, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, tc.resources)
			require.NoError(t, err)

			var clusters []*coreapi.Cluster
			for _, r := range tc.resources {
				if c, ok := r.(*coreapi.Cluster); ok {
					clusters = append(clusters, c)
				}
			}
			syncer := &dispatchRequestCredential{
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
