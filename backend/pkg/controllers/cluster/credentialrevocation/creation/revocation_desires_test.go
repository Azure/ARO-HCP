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

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/databasetesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestRevocationDesires_SyncOnce(t *testing.T) {
	testKey := controllerutils.SystemAdminCredentialRevocationKey{
		SubscriptionID:    testSubscriptionID,
		ResourceGroupName: testResourceGroupName,
		HCPClusterName:    testClusterName,
		RevocationName:    testRevocationName,
	}

	tests := []struct {
		name        string
		setupDB     func(db *databasetesting.MockResourcesDBClient)
		expectError bool
	}{
		{
			name:        "revocation not found returns nil",
			setupDB:     func(db *databasetesting.MockResourcesDBClient) {},
			expectError: false,
		},
		{
			name: "revocation with DeletionTimestamp is no-op",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				now := metav1.Now()
				createTestRevocation(t, db, testRevocationName, func(r *coreapi.SystemAdminCredentialRevocation) {
					r.Status.DeletionTimestamp = &now
				})
			},
			expectError: false,
		},
		{
			name: "cluster not found returns nil",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName)
			},
			expectError: false,
		},
		{
			name: "cluster has no ClusterServiceID returns nil",
			setupDB: func(db *databasetesting.MockResourcesDBClient) {
				createTestRevocation(t, db, testRevocationName)
				createTestCluster(t, db)
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := databasetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &revocationDesires{
				resourcesDBClient: db,
			}

			err := syncer.SyncOnce(ctx, testKey)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
