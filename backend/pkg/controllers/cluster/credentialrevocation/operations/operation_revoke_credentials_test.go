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
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	utilsclock "k8s.io/utils/clock"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestOperationRevokeCredentialsPoll_ShouldProcess(t *testing.T) {
	syncer := &operationRevokeCredentialsPoll{
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
				Status:  coreapi.ProvisioningStateDeleting,
				Request: "Create",
			},
			expected: false,
		},
		{
			name: "Accepted status should not process (must be Deleting)",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateAccepted,
				Request: coreapi.OperationRequestSystemAdminCredentialRevocation,
			},
			expected: false,
		},
		{
			name: "valid RevokeCredentials operation in Deleting should process",
			op: &coreapi.Operation{
				Status:  coreapi.ProvisioningStateDeleting,
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

func TestOperationRevokeCredentialsPoll_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	revokeOpSuffix := strings.ReplaceAll(testOperationName, "-", "")
	if len(revokeOpSuffix) > 16 {
		revokeOpSuffix = revokeOpSuffix[:16]
	}

	revocationResourceID := metadataapi.Must(coreapi.ToSystemAdminCredentialRevocationResourceID(
		testSubscriptionID, testResourceGroupName, testClusterName, revokeOpSuffix,
	))

	tests := []struct {
		name        string
		setupDB     func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:        "operation not found returns nil",
			setupDB:     func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {},
			expectError: false,
		},
		{
			name: "operation has no SystemAdminCredentialRevocation set waits",
			setupDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
				op.Status = coreapi.ProvisioningStateDeleting
				_, err := db.Operations(testSubscriptionID).Create(ctx, op, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status, "operation should remain Deleting")
			},
		},
		{
			name: "revocation doc still exists waits",
			setupDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
				op.Status = coreapi.ProvisioningStateDeleting
				op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{
					SystemAdminCredentialRevocationResourceID: revocationResourceID,
				}
				_, err := db.Operations(testSubscriptionID).Create(ctx, op, nil)
				require.NoError(t, err)

				cluster := newTestCluster(testOperationName)
				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)

				revocation := &coreapi.SystemAdminCredentialRevocation{
					CosmosMetadata: coreapi.CosmosMetadata{
						ResourceID:   revocationResourceID,
						PartitionKey: strings.ToLower(testSubscriptionID),
					},
					Spec: coreapi.SystemAdminCredentialRevocationSpec{
						OperationID:    testOperationName,
						RevokeOpSuffix: revokeOpSuffix,
					},
				}
				revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
				_, err = revocationCRUD.Create(ctx, revocation, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateDeleting, op.Status, "operation should remain Deleting while revocation exists")
			},
		},
		{
			name: "revocation doc gone marks operation Succeeded and clears cluster sentinel",
			setupDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op := newTestOperation(coreapi.OperationRequestSystemAdminCredentialRevocation)
				op.Status = coreapi.ProvisioningStateDeleting
				op.SystemAdminCredentialRevocation = &coreapi.OperationSystemAdminCredentialRevocation{
					SystemAdminCredentialRevocationResourceID: revocationResourceID,
				}
				_, err := db.Operations(testSubscriptionID).Create(ctx, op, nil)
				require.NoError(t, err)

				// The revocation doc is NOT added, simulating it being gone.
				cluster := newTestCluster(testOperationName)
				_, err = db.HCPClusters(testSubscriptionID, testResourceGroupName).Create(ctx, cluster, nil)
				require.NoError(t, err)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, coreapi.ProvisioningStateSucceeded, op.Status, "operation should be Succeeded")

				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Empty(t, cluster.ServiceProviderProperties.RevokeCredentialsOperationID, "cluster sentinel should be cleared")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := databasetesting.NewMockResourcesDBClient()
			tc.setupDB(t, ctx, db)

			syncer := &operationRevokeCredentialsPoll{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			key := controllerutils.OperationKey{
				SubscriptionID: testSubscriptionID,
				OperationName:  testOperationName,
			}

			err := syncer.SynchronizeOperation(ctx, key)
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
