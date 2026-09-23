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
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
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
			name: "revocation with CredentialsMarkedForDeletion=True is no-op",
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
