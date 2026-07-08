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

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevokedGC_SyncOnce(t *testing.T) {
	fixedTime := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakeClock(fixedTime)

	tests := []struct {
		name        string
		credName    string
		setupDB     func(db *databasetesting.MockResourcesDBClient)
		expectError bool
		verify      func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient)
	}{
		{
			name:     "deletes credential created more than 48h ago regardless of status",
			credName: "old-revoked",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createdAt := metav1.NewTime(fixedTime.Add(-49 * time.Hour))
				createTestCredentialRequest(t, db, "old-revoked",
					withCondition(api.SystemAdminCredentialRequestConditionRevoked),
					withCreationTimestamp(createdAt))
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 0, count, "credential older than the retention window should have been garbage-collected")
			},
		},
		{
			name:     "deletes issued credential created more than 48h ago",
			credName: "old-issued",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createdAt := metav1.NewTime(fixedTime.Add(-49 * time.Hour))
				createTestCredentialRequest(t, db, "old-issued",
					withCondition(api.SystemAdminCredentialRequestConditionIssued),
					withCreationTimestamp(createdAt))
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 0, count, "issued credential older than the retention window should be garbage-collected regardless of status")
			},
		},
		{
			name:     "does not delete credential created less than 48h ago",
			credName: "recent-revoked",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createdAt := metav1.NewTime(fixedTime.Add(-24 * time.Hour))
				createTestCredentialRequest(t, db, "recent-revoked",
					withCondition(api.SystemAdminCredentialRequestConditionRevoked),
					withCreationTimestamp(createdAt))
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 1, count, "credential within the retention window should not be garbage-collected")
			},
		},
		{
			name:     "skips credentials without a creation timestamp",
			credName: "no-ts",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, "no-ts",
					withCondition(api.SystemAdminCredentialRequestConditionRevoked))
			},
			expectError: false,
			verify: func(t *testing.T, ctx context.Context, db *databasetesting.MockResourcesDBClient) {
				credCRUD := db.SystemAdminCredentialRequests(testSubscriptionID, testResourceGroupName, testClusterName)
				iter, err := credCRUD.List(ctx, nil)
				require.NoError(t, err)
				var count int
				for range iter.Items(ctx) {
					count++
				}
				require.NoError(t, iter.GetError())
				assert.Equal(t, 1, count, "credential without a creation timestamp should not be garbage-collected")
			},
		},
		{
			name:        "no credentials returns nil",
			credName:    "nonexistent",
			setupDB:     func(db *databasetesting.MockResourcesDBClient) {},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := databasetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &revokedGC{
				cooldownChecker:   &alwaysSyncCooldownChecker{},
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			key := controllerutils.SystemAdminCredentialRequestKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
				CredentialName:    tc.credName,
			}

			err := syncer.SyncOnce(ctx, key)
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

type alwaysSyncCooldownChecker struct{}

func (c *alwaysSyncCooldownChecker) CanSync(_ context.Context, _ any) bool {
	return true
}
