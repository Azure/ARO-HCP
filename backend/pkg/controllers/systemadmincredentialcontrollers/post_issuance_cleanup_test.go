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
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	systemadmincredhelpers "github.com/Azure/ARO-HCP/internal/systemadmincredential"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestPostIssuanceCleanupSyncer_SyncOnce(t *testing.T) {
	testCredName := "testcred111111aa"

	tests := []struct {
		name               string
		spc                *api.ServiceProviderCluster
		credentials        []*api.SystemAdminCredential
		kubeApplierDesires []any
		verify             func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients)
		wantErr            bool
	}{
		{
			name: "cleans up ReadDesire for Issued credential",
			spc:  newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseIssued, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindRead, Name: systemadmincredhelpers.DesireNameCSR(testCredName)},
				}),
			},
			kubeApplierDesires: func() []any {
				rd := newTestClusterScopedReadDesire(systemadmincredhelpers.DesireNameCSR(testCredName))
				return []any{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// OutstandingDesires should be reduced
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Empty(t, cred.Status.OutstandingDesires, "ReadDesire should have been cleaned up")
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "cleans up ReadDesire for Failed credential",
			spc:  newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseFailed, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindRead, Name: systemadmincredhelpers.DesireNameCSR(testCredName)},
				}),
			},
			kubeApplierDesires: func() []any {
				rd := newTestClusterScopedReadDesire(systemadmincredhelpers.DesireNameCSR(testCredName))
				return []any{rd}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Empty(t, cred.Status.OutstandingDesires, "ReadDesire should have been cleaned up")
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "starts ApplyDesire deletion by creating DeleteDesire",
			spc:  newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseIssued, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: systemadmincredhelpers.DesireNameCSR(testCredName)},
				}),
			},
			kubeApplierDesires: func() []any {
				ad := newTestClusterScopedApplyDesire(systemadmincredhelpers.DesireNameCSR(testCredName))
				return []any{ad}
			}(),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// ApplyDesire should still exist (waiting for DeleteDesire success)
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					// Depending on the mock, the ApplyDesire may still be outstanding
					// The point is the DeleteDesire was created
					assert.NotEmpty(t, cred.Status.OutstandingDesires, "ApplyDesire cleanup is multi-step")
				}
				require.NoError(t, credIter.GetError())

				// DeleteDesire should now exist in kube-applier
				kaClient := ka.For(ctx, testManagementClusterResourceID)
				require.NotNil(t, kaClient)
				deleteCRUD, err := kaClient.DeleteDesiresForCluster(testSubscriptionID, testResourceGroupName, testClusterName)
				require.NoError(t, err)
				_, err = deleteCRUD.Get(ctx, systemadmincredhelpers.DesireNameCSR(testCredName))
				require.NoError(t, err, "DeleteDesire should have been created")
			},
		},
		{
			name: "skips non-Issued/Failed credentials",
			spc:  newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseRequested, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindRead, Name: "some-desire"},
				}),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Len(t, cred.Status.OutstandingDesires, 1, "should not have been modified")
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name: "skips credentials with no outstanding desires",
			spc:  newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseIssued),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range credIter.Items(ctx) {
					assert.Empty(t, cred.Status.OutstandingDesires)
				}
				require.NoError(t, credIter.GetError())
			},
		},
		{
			name:        "no credentials does nothing",
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
		{
			name:        "no ManagementClusterResourceID does nothing",
			spc:         newTestSPC(nil),
			credentials: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))

			resources := []any{newTestCluster()}
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

			syncer := &postIssuanceCleanupSyncer{
				cooldownChecker:      &alwaysSyncCooldownChecker{},
				resourcesDBClient:    mockDB,
				kubeApplierDBClients: mockKA,
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
