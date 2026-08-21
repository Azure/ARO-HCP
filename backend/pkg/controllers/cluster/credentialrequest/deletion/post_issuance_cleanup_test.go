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

package deletion

import (
	"context"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestPostIssuanceCleanup_SyncOnce(t *testing.T) {
	tests := []struct {
		name                    string
		credName                string
		setupDB                 func(db *corecosmosstoragetesting.MockResourcesDBClient)
		serviceProviderClusters []*coreapi.ServiceProviderCluster
		expectError             bool
	}{
		{
			name:     "credential not found returns nil",
			credName: "nonexistent",
			setupDB:  func(db *corecosmosstoragetesting.MockResourcesDBClient) {},
		},
		{
			name:     "credential still pending is a no-op",
			credName: testCredentialName,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
		},
		{
			name:     "issued credential without ServiceProviderCluster returns nil",
			credName: testCredentialName,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName,
					withCondition(coreapi.SystemAdminCredentialRequestConditionIssued))
			},
			serviceProviderClusters: nil,
		},
		{
			name:     "failed credential without ServiceProviderCluster returns nil",
			credName: testCredentialName,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName,
					withCondition(coreapi.SystemAdminCredentialRequestConditionFailed))
			},
			serviceProviderClusters: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &postIssuanceCleanup{
				resourcesDBClient:            db,
				kubeApplierDBClients:         nil,
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: tc.serviceProviderClusters},
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
		})
	}
}
