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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestPostIssuanceCleanup_SyncOnce(t *testing.T) {
	tests := []struct {
		name     string
		creds    []*api.SystemAdminCredential
		wantErr  bool
		verifyDB func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name: "Issued credential with no outstanding desires is a no-op",
			creds: []*api.SystemAdminCredential{
				testCredential(api.SystemAdminCredentialPhaseIssued),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Equal(t, api.SystemAdminCredentialPhaseIssued, cred.Status.Phase)
			},
		},
		{
			name: "Requested credential is skipped",
			creds: []*api.SystemAdminCredential{
				func() *api.SystemAdminCredential {
					cred := testCredential(api.SystemAdminCredentialPhaseRequested)
					cred.Status.OutstandingDesires = []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindApply, Name: "some-desire"},
					}
					return cred
				}(),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Len(t, cred.Status.OutstandingDesires, 1, "desires should not be modified for Requested credential")
			},
		},
		{
			name: "AwaitingRevocation credential is skipped",
			creds: []*api.SystemAdminCredential{
				func() *api.SystemAdminCredential {
					cred := testCredential(api.SystemAdminCredentialPhaseAwaitingRevocation)
					cred.Status.OutstandingDesires = []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindApply, Name: "some-desire"},
					}
					return cred
				}(),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				assert.Len(t, cred.Status.OutstandingDesires, 1, "desires should not be modified for AwaitingRevocation credential")
			},
		},
		{
			name:    "no credentials is a no-op",
			creds:   nil,
			wantErr: false,
		},
		{
			name: "Failed credential with outstanding ReadDesire has it cleaned up",
			creds: []*api.SystemAdminCredential{
				func() *api.SystemAdminCredential {
					cred := testCredential(api.SystemAdminCredentialPhaseFailed)
					cred.Status.OutstandingDesires = []api.SystemAdminCredentialDesireRef{
						{Kind: api.SystemAdminCredentialDesireKindRead, Name: "csr-" + testCredentialName},
					}
					return cred
				}(),
			},
			wantErr: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				cred := getCredentialFromDB(ctx, t, db, testCredentialName)
				// The ReadDesire should be removed from OutstandingDesires since it was a no-op delete (not found)
				assert.Empty(t, cred.Status.OutstandingDesires, "ReadDesire should be pruned from outstanding desires")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)

			cluster := testCluster()
			spc := testSPC(testMCResourceID())
			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster, spc})
			require.NoError(t, err)

			for _, cred := range tt.creds {
				createCredentialInDB(ctx, t, cred, db)
			}

			kaClient := databasetesting.NewMockKubeApplierDBClient()
			kaClients := testMockKubeApplierDBClients(kaClient)

			syncer := &postIssuanceCleanup{
				resourcesDBClient:    db,
				kubeApplierDBClients: kaClients,
			}

			err = syncer.SyncOnce(ctx, testClusterKey())
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
