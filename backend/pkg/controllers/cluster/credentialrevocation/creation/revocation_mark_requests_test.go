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

package creation

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/operationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevocationMarkRequests_SyncOnce(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	testKey := controllerutils.SystemAdminCredentialRevocationKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		RevocationName:    testRevocationName,
	}

	tests := []struct {
		name        string
		setupDB     func(db *corecosmosstoragetesting.MockResourcesDBClient)
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:        "revocation not found returns nil",
			setupDB:     func(db *corecosmosstoragetesting.MockResourcesDBClient) {},
			expectError: false,
		},
		{
			name: "revocation with DeletionTimestamp is no-op",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				now := metav1.NewTime(fixedTime)
				createTestRevocation(t, db, testRevocationName, func(r *coreapi.SystemAdminCredentialRevocation) {
					r.Status.DeletionTimestamp = &now
				})
			},
			expectError: false,
		},
		{
			name: "revocation with CredentialsMarkedForDeletion=True repeats sweep",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName, func(r *coreapi.SystemAdminCredentialRevocation) {
					meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
						Type:   coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion,
						Status: metav1.ConditionTrue,
						Reason: "CredentialsMarked",
					})
				})
			},
			expectError: false,
		},
		{
			name: "marks existing credential requests and sets condition",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName)
				createTestCredentialRequest(t, db, "cred-01")
				createTestCredentialRequest(t, db, "cred-02")
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				// All credential requests should have DeletionTimestamp set.
				credCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				for _, cred := range iter.Items(ctx) {
					assert.NotNil(t, cred.Status.DeletionTimestamp, "credential %s should have DeletionTimestamp set", cred.ResourceID.Name)
				}
				require.NoError(t, iter.GetError())

				// The revocation should have CredentialsMarkedForDeletion=True.
				revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
				revocation, err := revocationCRUD.Get(ctx, testRevocationName)
				require.NoError(t, err)
				assert.True(t, meta.IsStatusConditionTrue(revocation.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion),
					"CredentialsMarkedForDeletion condition should be True")
			},
		},
		{
			name: "no credential requests still sets condition",
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName)
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				revocationCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
				revocation, err := revocationCRUD.Get(ctx, testRevocationName)
				require.NoError(t, err)
				assert.True(t, meta.IsStatusConditionTrue(revocation.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionCredentialsMarkedForDeletion),
					"CredentialsMarkedForDeletion should be True even with no credential requests")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			createTestCluster(t, db)
			tc.setupDB(db)

			syncer := &revocationMarkRequests{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			err := syncer.SyncOnce(ctx, testKey)
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

func TestRevocationRepeatsSweepAndQueues(t *testing.T) {
	ctx := t.Context()
	clock := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	createTestCluster(t, db)
	a := createTestRevocation(t, db, "a")
	op := cosmosstorageutils.NewOperation(coreapi.OperationRequestSystemAdminCredentialRevocation, a.ResourceID.Parent, metadataapi.InternalID{}, "eastus", "", "", "", nil)
	deadline := clock.Now().Add(time.Minute)
	op.CompletionDeadline = &deadline
	op, err := db.Operations(testSubscriptionID).Create(ctx, op, nil)
	require.NoError(t, err)
	b := createTestRevocation(t, db, "b", func(r *coreapi.SystemAdminCredentialRevocation) { r.Spec.OperationID = op.OperationID.Name })
	c := &revocationMarkRequests{clock: clock, resourcesDBClient: db}
	key := controllerutils.SystemAdminCredentialRevocationKey{SubscriptionID: testSubscriptionID, ResourceGroupName: testResourceGroupName, HCPClusterName: testClusterName, RevocationName: "a"}
	require.NoError(t, c.SyncOnce(ctx, key))
	createTestCredentialRequest(t, db, "late")
	clock.Step(time.Minute)
	require.NoError(t, c.SyncOnce(ctx, key))
	cred, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRequests(testClusterName).Get(ctx, "late")
	require.NoError(t, err)
	require.WithinDuration(t, clock.Now(), cred.Status.DeletionTimestamp.Time, 0, "a second sweep catches late arrivals")
	key.RevocationName = "b"
	require.NoError(t, c.SyncOnce(ctx, key))
	revs := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentialRevocations(testClusterName)
	b, err = revs.Get(ctx, b.ResourceID.Name)
	require.NoError(t, err)
	require.Equal(t, "WaitingForOtherRevocation", meta.FindStatusCondition(b.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive).Reason)
	op, err = db.Operations(testSubscriptionID).Get(ctx, op.OperationID.Name)
	require.NoError(t, err)
	expired, err := operationutils.ExpireCredentialOperation(ctx, clock, db, op, nil)
	require.NoError(t, err)
	require.True(t, expired)
	// Simulate completed teardown of the first owner. The queued document must
	// acquire ownership and do its work despite its failed ARM operation.
	crud := db.HCPClusters(testSubscriptionID, testResourceGroupName)
	cluster, err := crud.Get(ctx, testClusterName)
	require.NoError(t, err)
	cluster.ServiceProviderProperties.SystemAdminCredentialRevocationResourceID = nil
	cluster.ServiceProviderProperties.RevokeCredentialsOperationID = ""
	_, err = crud.Replace(ctx, cluster, nil)
	require.NoError(t, err)
	require.NoError(t, revs.Delete(ctx, a.ResourceID.Name))
	require.NoError(t, c.SyncOnce(ctx, key))
	b, err = revs.Get(ctx, b.ResourceID.Name)
	require.NoError(t, err)
	require.True(t, meta.IsStatusConditionTrue(b.Status.Conditions, coreapi.SystemAdminCredentialRevocationConditionActive))
	op, err = db.Operations(testSubscriptionID).Get(ctx, op.OperationID.Name)
	require.NoError(t, err)
	require.Equal(t, coreapi.ProvisioningStateFailed, op.Status)
}
