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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestOperationRevokeCredentialsDispatch_ShouldProcess(t *testing.T) {
	tests := []struct {
		name   string
		op     *api.Operation
		expect bool
	}{
		{
			name:   "accepts Accepted RevokeCredentials operation",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted),
			expect: true,
		},
		{
			name:   "rejects terminal operation",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateSucceeded),
			expect: false,
		},
		{
			name:   "rejects non-RevokeCredentials operation",
			op:     testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted),
			expect: false,
		},
		{
			name:   "rejects Deleting status (already dispatched)",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateDeleting),
			expect: false,
		},
		{
			name: "rejects operation with no ExternalID",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				op.ExternalID = nil
				return op
			}(),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &operationRevokeCredentialsDispatch{}
			got := syncer.ShouldProcess(context.Background(), tt.op)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOperationRevokeCredentialsDispatch_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		setupDB  func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients)
		wantErr  bool
		verifyDB func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "cluster not found succeeds the operation",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients) {
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{op})
				require.NoError(t, err)
				return db, databasetesting.NewMockKubeApplierDBClients()
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "sentinel mismatch cancels the operation",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = "different-operation"
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op})
				require.NoError(t, err)
				return db, databasetesting.NewMockKubeApplierDBClients()
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateCanceled, op.Status)
			},
		},
		{
			name: "cluster without ClusterServiceID is a no-op",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.ClusterServiceID = nil
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, op})
				require.NoError(t, err)
				return db, databasetesting.NewMockKubeApplierDBClients()
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				// Should still be Accepted — no transition happened
				assert.Equal(t, arm.ProvisioningStateAccepted, op.Status)
			},
		},
		{
			name: "no MC assigned succeeds immediately",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName
				spc := testSPC(nil) // no MC
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc, op})
				require.NoError(t, err)
				return db, databasetesting.NewMockKubeApplierDBClients()
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateSucceeded, op.Status)
			},
		},
		{
			name: "flips credentials to AwaitingRevocation and moves op to Deleting",
			setupDB: func(ctx context.Context, t *testing.T) (*databasetesting.MockResourcesDBClient, *databasetesting.MockKubeApplierDBClients) {
				cluster := testCluster()
				cluster.ServiceProviderProperties.RevokeCredentialsOperationID = testOperationName
				spc := testSPC(testMCResourceID())
				op := testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted)
				db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc, op})
				require.NoError(t, err)

				// Add two credentials (Requested and Issued)
				cred1 := testCredential(api.SystemAdminCredentialPhaseRequested)
				createCredentialInDB(ctx, t, cred1, db)

				cred2RID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, "fedcba9876543210"))
				cred2 := &api.SystemAdminCredential{
					CosmosMetadata: api.CosmosMetadata{ResourceID: cred2RID},
					Spec: api.SystemAdminCredentialSpec{
						OperationID: "other-op",
						Username:    defaultUsername,
					},
					Status: api.SystemAdminCredentialStatus{Phase: api.SystemAdminCredentialPhaseIssued},
				}
				createCredentialInDB(ctx, t, cred2, db)

				kaClient := databasetesting.NewMockKubeApplierDBClient()
				kaClients := testMockKubeApplierDBClients(kaClient)
				return db, kaClients
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Both credentials should be AwaitingRevocation
				credentialsCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentials(testClusterName)
				iter, err := credentialsCRUD.List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range iter.Items(ctx) {
					if cred != nil {
						assert.Equal(t, api.SystemAdminCredentialPhaseAwaitingRevocation, cred.Status.Phase,
							"credential %q should be AwaitingRevocation", cred.GetResourceID().Name)
					}
				}

				// Operation should be Deleting
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.Equal(t, arm.ProvisioningStateDeleting, op.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)
			fakeClock := clocktesting.NewFakePassiveClock(fixedTime)

			db, kaClients := tt.setupDB(ctx, t)
			clusterLister := newMockClusterLister(db)

			syncer := &operationRevokeCredentialsDispatch{
				clock:                fakeClock,
				clusterLister:        clusterLister,
				resourcesDBClient:    db,
				kubeApplierDBClients: kaClients,
				hostedClusterNSEnvID: testHCPClusterNSEnvID,
			}

			err := syncer.SynchronizeOperation(ctx, testOperationKey())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, db)
			}
		})
	}
}
