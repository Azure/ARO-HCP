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

package systemadmincredentialcontrollers

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// generateTestKeypair generates a small RSA keypair for tests.
func generateTestKeypair(t *testing.T) (publicPEM, privatePEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
}

func TestDesiresCreatorSyncer_SyncOnce(t *testing.T) {
	testCredName := "testcred111111aa"

	tests := []struct {
		name               string
		cluster            *api.HCPOpenShiftCluster
		spc                *api.ServiceProviderCluster
		credentials        []*api.SystemAdminCredential
		kubeApplierDesires []any
		verify             func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients)
		wantErr            bool
	}{
		{
			name:    "creates desires for Requested credential",
			cluster: newTestClusterWithCSID(),
			spc:     newTestSPC(testManagementClusterResourceID),
			credentials: func() []*api.SystemAdminCredential {
				cred := newTestCredential(testCredName, api.SystemAdminCredentialPhaseRequested)
				pubPEM, privPEM := generateTestKeypair(t)
				cred.Spec.PublicKeyPEM = pubPEM
				cred.Spec.PrivateKeyPEM = privPEM
				return []*api.SystemAdminCredential{cred}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Check credential was updated with OutstandingDesires
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.NotEmpty(t, cred.Status.OutstandingDesires, "OutstandingDesires should be populated")
					// Should have 5 ApplyDesires + 1 ReadDesire = 6 total
					assert.Len(t, cred.Status.OutstandingDesires, 6, "expected 6 outstanding desires (5 apply + 1 read)")

					// Verify desire kinds
					applyCount := 0
					readCount := 0
					for _, ref := range cred.Status.OutstandingDesires {
						switch ref.Kind {
						case api.SystemAdminCredentialDesireKindApply:
							applyCount++
						case api.SystemAdminCredentialDesireKindRead:
							readCount++
						}
					}
					assert.Equal(t, 5, applyCount, "expected 5 ApplyDesires")
					assert.Equal(t, 1, readCount, "expected 1 ReadDesire")
				}
				require.NoError(t, credIter.GetError())

				// Check that desires were created in kube-applier
				kaClient := ka.For(ctx, testManagementClusterResourceID)
				require.NotNil(t, kaClient)

				applyCRUD, err := kaClient.ApplyDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				applyIter, err := applyCRUD.List(ctx, nil)
				require.NoError(t, err)
				applyCount := 0
				for range applyIter.Items(ctx) {
					applyCount++
				}
				require.NoError(t, applyIter.GetError())
				assert.Equal(t, 5, applyCount, "expected 5 ApplyDesires in kube-applier")

				readCRUD, err := kaClient.ReadDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				readIter, err := readCRUD.List(ctx, nil)
				require.NoError(t, err)
				readCount := 0
				for range readIter.Items(ctx) {
					readCount++
				}
				require.NoError(t, readIter.GetError())
				assert.Equal(t, 1, readCount, "expected 1 ReadDesire in kube-applier")
			},
		},
		{
			name:    "skips non-Requested credentials",
			cluster: newTestClusterWithCSID(),
			spc:     newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseIssued),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Credential should not have OutstandingDesires
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Empty(t, cred.Status.OutstandingDesires)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name:    "idempotent when OutstandingDesires already populated",
			cluster: newTestClusterWithCSID(),
			spc:     newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseRequested, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameCSR(testCredName)},
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameCSRA(testCredName)},
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameRBACGiveCSRPerm(testCredName)},
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameRBACCSRA(testCredName)},
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameRBACRevocation(testCredName)},
					{Kind: api.SystemAdminCredentialDesireKindRead, Name: systemadmincredhelpers.DesireNameCSR(testCredName)},
				}),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Should still have exactly 6 desires (not doubled)
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Len(t, cred.Status.OutstandingDesires, 6)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name:        "cluster not found does nothing",
			cluster:     nil,
			spc:         nil,
			credentials: nil,
		},
		{
			name:        "cluster without ClusterServiceID does nothing",
			cluster:     newTestCluster(), // no CSID
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
		{
			name:        "cluster with DeletionTimestamp does nothing",
			cluster:     newTestClusterWithDeletion(),
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
		{
			name:        "no ManagementClusterResourceID does nothing",
			cluster:     newTestClusterWithCSID(),
			spc:         newTestSPC(nil),
			credentials: nil,
		},
		{
			name:        "no credentials does nothing",
			cluster:     newTestClusterWithCSID(),
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{}
			if tt.cluster != nil {
				resources = append(resources, tt.cluster)
			}
			if tt.spc != nil {
				resources = append(resources, tt.spc)
			}
			for _, cred := range tt.credentials {
				resources = append(resources, cred)
			}

			mockDB, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, resources)
			require.NoError(t, err)

			mockKA := databasetesting.NewMockKubeApplierDBClients()
			mockKAClient, err := databasetesting.NewMockKubeApplierDBClientWithResources(ctx, tt.kubeApplierDesires)
			require.NoError(t, err)
			mockKA.Register(testManagementClusterResourceID, mockKAClient)

			syncer := &desiresCreatorSyncer{
				cooldownChecker:                     &alwaysSyncCooldownChecker{},
				resourcesDBClient:                   mockDB,
				kubeApplierDBClients:                mockKA,
				hostedClusterNamespaceEnvIdentifier: testEnvIdentifier,
			}

			err = syncer.SyncOnce(ctx, newTestClusterKey())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			if tt.verify != nil {
				tt.verify(t, ctx, mockDB, mockKA)
			}
		})
	}
}
