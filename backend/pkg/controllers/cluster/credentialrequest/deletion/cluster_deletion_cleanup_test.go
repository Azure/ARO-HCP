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
	"time"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterDeletionCleanup_SyncOnce(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC))

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
			name:     "credential without DeletionTimestamp is a no-op",
			credName: testCredentialName,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName)
			},
		},
		{
			name:     "credential with DeletionTimestamp but no ServiceProviderCluster returns nil",
			credName: testCredentialName,
			setupDB: func(db *corecosmosstoragetesting.MockResourcesDBClient) {
				createTestCredentialRequest(t, db, testCredentialName, func(cred *coreapi.SystemAdminCredentialRequest) {
					cred.Status.DeletionTimestamp = &now
				})
			},
			serviceProviderClusters: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := utils.ContextWithLogger(context.Background(), testr.New(t))
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			tc.setupDB(db)

			syncer := &credentialRequestDeletion{
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
