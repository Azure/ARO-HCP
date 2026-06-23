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

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestOperationRequestCredentialDispatch_ShouldProcess(t *testing.T) {
	tests := []struct {
		name   string
		op     *api.Operation
		expect bool
	}{
		{
			name:   "accepts fresh RequestCredential operation with no InternalID",
			op:     testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted),
			expect: true,
		},
		{
			name: "rejects terminal operation",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateSucceeded)
				return op
			}(),
			expect: false,
		},
		{
			name:   "rejects non-RequestCredential operation",
			op:     testOperation(database.OperationRequestRevokeCredentials, arm.ProvisioningStateAccepted),
			expect: false,
		},
		{
			name: "rejects operation that already has InternalID",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted)
				credRID := api.Must(api.ToSystemAdminCredentialResourceID(testSubscriptionID, testResourceGroupName, testClusterName, testCredentialName))
				op.InternalID = api.Must(api.NewInternalID(credRID.String()))
				return op
			}(),
			expect: false,
		},
		{
			name: "rejects operation with no ExternalID",
			op: func() *api.Operation {
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted)
				op.ExternalID = nil
				return op
			}(),
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &operationRequestCredentialDispatch{}
			got := syncer.ShouldProcess(context.Background(), tt.op)
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestOperationRequestCredentialDispatch_SynchronizeOperation(t *testing.T) {
	fixedTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		resources []any
		wantErr   bool
		verifyDB  func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "creates credential and stamps InternalID on operation",
			resources: []any{
				testCluster(),
				testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// Operation should now have an InternalID pointing to the credential
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.NotEmpty(t, op.InternalID.String(), "operation should have InternalID set")
				assert.Contains(t, op.InternalID.String(), "systemadmincredentials/", "InternalID should point to a SystemAdminCredential")

				// A credential should exist
				credentialsCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentials(testClusterName)
				iter, err := credentialsCRUD.List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for _, cred := range iter.Items(ctx) {
					if cred != nil {
						count++
						assert.Equal(t, api.SystemAdminCredentialPhaseRequested, cred.Status.Phase)
						assert.NotEmpty(t, cred.Spec.PublicKeyPEM)
						assert.NotEmpty(t, cred.Spec.PrivateKeyPEM)
						assert.Equal(t, defaultUsername, cred.Spec.Username)
						assert.Equal(t, testOperationName, cred.Spec.OperationID)
					}
				}
				assert.Equal(t, 1, count, "exactly one credential should have been created")
			},
		},
		{
			name: "operation not found is a no-op",
			resources: []any{
				testCluster(),
				// no operation in DB
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				// No credential should exist
				credentialsCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentials(testClusterName)
				iter, err := credentialsCRUD.List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for _, cred := range iter.Items(ctx) {
					if cred != nil {
						count++
					}
				}
				assert.Equal(t, 0, count)
			},
		},
		{
			name: "cluster not found cancels the operation",
			resources: []any{
				// no cluster in DB
				testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				// Operation should have been updated (LastTransitionTime set)
				assert.NotZero(t, op.LastTransitionTime)
			},
		},
		{
			name: "idempotent: existing credential for operation reuses it",
			resources: func() []any {
				cluster := testCluster()
				op := testOperation(database.OperationRequestRequestCredential, arm.ProvisioningStateAccepted)
				return []any{cluster, op}
			}(),
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				op, err := db.Operations(testSubscriptionID).Get(ctx, testOperationName)
				require.NoError(t, err)
				assert.NotEmpty(t, op.InternalID.String())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)
			fakeClock := clocktesting.NewFakePassiveClock(fixedTime)

			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, tt.resources)
			require.NoError(t, err)

			clusterLister := newMockClusterLister(db)

			syncer := &operationRequestCredentialDispatch{
				clock:             fakeClock,
				clusterLister:     clusterLister,
				resourcesDBClient: db,
			}

			err = syncer.SynchronizeOperation(ctx, testOperationKey())
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

// mockClusterLister wraps the mock DB to implement listers.ClusterLister.
type mockClusterLister struct {
	db *databasetesting.MockResourcesDBClient
}

func newMockClusterLister(db *databasetesting.MockResourcesDBClient) *mockClusterLister {
	return &mockClusterLister{db: db}
}

func (m *mockClusterLister) List(ctx context.Context) ([]*api.HCPOpenShiftCluster, error) {
	iter, err := m.db.HCPClusters("", "").List(ctx, nil)
	if err != nil {
		return nil, err
	}
	var result []*api.HCPOpenShiftCluster
	for _, item := range iter.Items(ctx) {
		if item != nil {
			result = append(result, item)
		}
	}
	return result, iter.GetError()
}

func (m *mockClusterLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) (*api.HCPOpenShiftCluster, error) {
	return m.db.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
}

func (m *mockClusterLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.HCPOpenShiftCluster, error) {
	return m.List(ctx)
}

// mockCredentialLister wraps the mock DB to implement listers.SystemAdminCredentialLister.
type mockCredentialLister struct {
	db *databasetesting.MockResourcesDBClient
}

func newMockCredentialLister(db *databasetesting.MockResourcesDBClient) *mockCredentialLister {
	return &mockCredentialLister{db: db}
}

func (m *mockCredentialLister) List(ctx context.Context) ([]*api.SystemAdminCredential, error) {
	return nil, nil
}

func (m *mockCredentialLister) Get(ctx context.Context, subscriptionID, resourceGroupName, clusterName, credentialName string) (*api.SystemAdminCredential, error) {
	return m.db.HCPClusters(subscriptionID, resourceGroupName).SystemAdminCredentials(clusterName).Get(ctx, credentialName)
}

func (m *mockCredentialLister) ListForResourceGroup(ctx context.Context, subscriptionName, resourceGroupName string) ([]*api.SystemAdminCredential, error) {
	return nil, nil
}

func (m *mockCredentialLister) ListForCluster(ctx context.Context, subscriptionName, resourceGroupName, clusterName string) ([]*api.SystemAdminCredential, error) {
	return nil, nil
}

// mockReadDesireLister wraps a MockKubeApplierDBClient to implement listers.ReadDesireLister.
type mockReadDesireLister struct {
	kaClient *databasetesting.MockKubeApplierDBClient
}

func newMockReadDesireLister(kaClient *databasetesting.MockKubeApplierDBClient) *mockReadDesireLister {
	return &mockReadDesireLister{kaClient: kaClient}
}

func (m *mockReadDesireLister) List(ctx context.Context) ([]*kubeapplier.ReadDesire, error) {
	return nil, nil
}

func (m *mockReadDesireLister) GetForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName, name string) (*kubeapplier.ReadDesire, error) {
	crud, err := m.kaClient.ReadDesiresForCluster(subscriptionID, resourceGroupName, clusterName)
	if err != nil {
		return nil, err
	}
	return crud.Get(ctx, name)
}

func (m *mockReadDesireLister) GetForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName, name string) (*kubeapplier.ReadDesire, error) {
	return nil, nil
}

func (m *mockReadDesireLister) ListForManagementCluster(ctx context.Context, managementClusterResourceID *azcorearm.ResourceID) ([]*kubeapplier.ReadDesire, error) {
	return nil, nil
}

func (m *mockReadDesireLister) ListForCluster(ctx context.Context, subscriptionID, resourceGroupName, clusterName string) ([]*kubeapplier.ReadDesire, error) {
	return nil, nil
}

func (m *mockReadDesireLister) ListForNodePool(ctx context.Context, subscriptionID, resourceGroupName, clusterName, nodePoolName string) ([]*kubeapplier.ReadDesire, error) {
	return nil, nil
}

// testMockKubeApplierDBClients creates a MockKubeApplierDBClients with one registered MC.
func testMockKubeApplierDBClients(kaClient *databasetesting.MockKubeApplierDBClient) *databasetesting.MockKubeApplierDBClients {
	clients := databasetesting.NewMockKubeApplierDBClients()
	clients.Register(testMCResourceID(), kaClient)
	return clients
}
