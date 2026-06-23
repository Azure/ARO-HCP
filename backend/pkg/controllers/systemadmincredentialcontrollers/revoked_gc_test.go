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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
)

func TestRevokedGC_SyncOnce(t *testing.T) {
	baseTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		clock   time.Time
		creds   []*api.SystemAdminCredential
		wantErr bool
		// expectedDeletedNames is the set of credential names that should be deleted after SyncOnce
		expectedDeletedNames []string
		// expectedKeptNames is the set of credential names that should remain
		expectedKeptNames []string
	}{
		{
			name:  "revoked credential past retention is deleted",
			clock: baseTime.Add(RevokedGCRetention + time.Hour),
			creds: func() []*api.SystemAdminCredential {
				cred := testCredential(api.SystemAdminCredentialPhaseRevoked)
				revokedAt := metav1.NewTime(baseTime)
				cred.Status.RevokedAt = &revokedAt
				return []*api.SystemAdminCredential{cred}
			}(),
			expectedDeletedNames: []string{testCredentialName},
			expectedKeptNames:    nil,
		},
		{
			name:  "revoked credential within retention is kept",
			clock: baseTime.Add(RevokedGCRetention - time.Hour),
			creds: func() []*api.SystemAdminCredential {
				cred := testCredential(api.SystemAdminCredentialPhaseRevoked)
				revokedAt := metav1.NewTime(baseTime)
				cred.Status.RevokedAt = &revokedAt
				return []*api.SystemAdminCredential{cred}
			}(),
			expectedDeletedNames: nil,
			expectedKeptNames:    []string{testCredentialName},
		},
		{
			name:  "non-revoked credential is not touched",
			clock: baseTime.Add(RevokedGCRetention + time.Hour),
			creds: func() []*api.SystemAdminCredential {
				return []*api.SystemAdminCredential{testCredential(api.SystemAdminCredentialPhaseIssued)}
			}(),
			expectedDeletedNames: nil,
			expectedKeptNames:    []string{testCredentialName},
		},
		{
			name:  "revoked credential with nil RevokedAt is never deleted",
			clock: baseTime.Add(RevokedGCRetention + 100*time.Hour),
			creds: func() []*api.SystemAdminCredential {
				cred := testCredential(api.SystemAdminCredentialPhaseRevoked)
				cred.Status.RevokedAt = nil
				return []*api.SystemAdminCredential{cred}
			}(),
			expectedDeletedNames: nil,
			expectedKeptNames:    []string{testCredentialName},
		},
		{
			name:                 "no credentials is a no-op",
			clock:                baseTime,
			creds:                nil,
			expectedDeletedNames: nil,
			expectedKeptNames:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := testCtx(t)
			fakeClock := clocktesting.NewFakePassiveClock(tt.clock)

			cluster := testCluster()
			db, err := databasetesting.NewMockResourcesDBClientWithResources(ctx, []any{cluster})
			require.NoError(t, err)

			for _, cred := range tt.creds {
				createCredentialInDB(ctx, t, cred, db)
			}

			syncer := &revokedGC{
				clock:             fakeClock,
				resourcesDBClient: db,
			}

			err = syncer.SyncOnce(ctx, testClusterKey())
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			credentialsCRUD := db.HCPClusters(testSubscriptionID, testResourceGroupName).SystemAdminCredentials(testClusterName)
			for _, name := range tt.expectedDeletedNames {
				_, err := credentialsCRUD.Get(ctx, name)
				assert.Error(t, err, "credential %q should have been deleted", name)
			}
			for _, name := range tt.expectedKeptNames {
				cred, err := credentialsCRUD.Get(ctx, name)
				assert.NoError(t, err, "credential %q should still exist", name)
				assert.NotNil(t, cred)
			}
		})
	}
}
