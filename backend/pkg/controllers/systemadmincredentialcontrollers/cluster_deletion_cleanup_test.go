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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterDeletionCleanupSyncer_SyncOnce(t *testing.T) {
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
			name:    "deletes credentials and sets condition when no outstanding desires",
			cluster: newTestClusterWithDeletion(),
			spc:     newTestSPC(testManagementClusterResourceID),
			credentials: []*api.SystemAdminCredential{
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseIssued),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Credential should be deleted
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for range credIter.Items(ctx) {
					count++
				}
				require.NoError(t, credIter.GetError())
				assert.Equal(t, 0, count, "all credentials should be deleted")

				// SPC should have condition set
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				found := false
				for _, c := range spc.Status.Conditions {
					if c.Type == "SystemAdminCredentialContentDeleted" {
						found = true
						assert.Equal(t, metav1.ConditionTrue, c.Status)
					}
				}
				assert.True(t, found, "SystemAdminCredentialContentDeleted condition should be set")
			},
		},
		{
			name:        "no-op when cluster not found",
			cluster:     nil,
			spc:         nil,
			credentials: nil,
		},
		{
			name:        "no-op when DeletionTimestamp is nil",
			cluster:     newTestCluster(), // no deletion timestamps
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
		{
			name: "no-op when ClusterServiceDeletionTimestamp is nil",
			cluster: func() *api.HCPOpenShiftCluster {
				c := newTestCluster()
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
				// ClusterServiceDeletionTimestamp is NOT set
				return c
			}(),
			spc:         newTestSPC(testManagementClusterResourceID),
			credentials: nil,
		},
		{
			name:    "no-op when condition already set",
			cluster: newTestClusterWithDeletion(),
			spc: func() *api.ServiceProviderCluster {
				spc := newTestSPC(testManagementClusterResourceID)
				spc.Status.Conditions = []metav1.Condition{
					{
						Type:   "SystemAdminCredentialContentDeleted",
						Status: metav1.ConditionTrue,
					},
				}
				return spc
			}(),
			credentials: []*api.SystemAdminCredential{
				// This credential should NOT be deleted since condition is already set
				newTestCredential(testCredName, api.SystemAdminCredentialPhaseIssued),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Credential should still exist (controller returned early)
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for range credIter.Items(ctx) {
					count++
				}
				require.NoError(t, credIter.GetError())
				assert.Equal(t, 1, count, "credential should still exist")
			},
		},
		{
			name:    "waits when outstanding desires remain with no KA client",
			cluster: newTestClusterWithDeletion(),
			spc:     newTestSPC(nil), // no MC = no KA client
			credentials: []*api.SystemAdminCredential{
				newTestCredentialWithDesires(testCredName, api.SystemAdminCredentialPhaseIssued, []api.SystemAdminCredentialDesireRef{
					{Kind: api.SystemAdminCredentialDesireKindApply, Name: "some-desire"},
				}),
			},
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// Credential should still exist (waiting for desires to be cleaned up)
				credIter, err := db.SystemAdminCredentials(testSubscriptionID, testResourceGroupName, testClusterName).List(ctx, nil)
				require.NoError(t, err)
				count := 0
				for range credIter.Items(ctx) {
					count++
				}
				require.NoError(t, credIter.GetError())
				assert.Equal(t, 1, count, "credential should still exist")

				// Condition should NOT be set
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				if database.IsNotFoundError(err) {
					// SPC was auto-created, check for empty conditions
					return
				}
				require.NoError(t, err)
				for _, c := range spc.Status.Conditions {
					assert.NotEqual(t, "SystemAdminCredentialContentDeleted", c.Type, "condition should not be set yet")
				}
			},
		},
		{
			name:    "deletes with no credentials and sets condition",
			cluster: newTestClusterWithDeletion(),
			spc:     newTestSPC(testManagementClusterResourceID),
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient, ka *databasetesting.MockKubeApplierDBClients) {
				// SPC should have condition set even with no credentials
				spc, err := db.ServiceProviderClusters(testSubscriptionID, testResourceGroupName, testClusterName).Get(ctx, api.ServiceProviderClusterResourceName)
				require.NoError(t, err)
				found := false
				for _, c := range spc.Status.Conditions {
					if c.Type == "SystemAdminCredentialContentDeleted" {
						found = true
						assert.Equal(t, metav1.ConditionTrue, c.Status)
					}
				}
				assert.True(t, found, "condition should be set even with no credentials")
			},
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

			syncer := &clusterDeletionCleanupSyncer{
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
