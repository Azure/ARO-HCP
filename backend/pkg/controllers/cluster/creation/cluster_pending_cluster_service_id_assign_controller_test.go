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
	"strings"
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/corelistertesting"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func TestClusterPendingClusterServiceIDAssign_SyncOnce(t *testing.T) {
	clusterInternalID := metadataapi.Must(metadataapi.NewInternalID(testClusterServiceIDStr))

	// placedSPC is a ServiceProviderCluster whose Spec.ManagementClusterResourceID
	// is set (placement resolved by the PlacementController).
	placedSPC := func() *coreapi.ServiceProviderCluster {
		return newTestSPC(func(spc *coreapi.ServiceProviderCluster) {
			spc.Spec.ManagementClusterResourceID = testManagementClusterResourceID()
		})
	}

	tests := []struct {
		name        string
		listCluster *coreapi.HCPOpenShiftCluster
		listSPC     *coreapi.ServiceProviderCluster // seeded into the SPC lister (nil = not found)
		dbCluster   *coreapi.HCPOpenShiftCluster
		expectError bool
		verifyDB    func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient)
	}{
		{
			name:        "assigns PendingClusterServiceID when placement resolved and both IDs nil",
			listCluster: newTestCluster(),
			listSPC:     placedSPC(),
			dbCluster:   newTestCluster(),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
				assert.True(t, strings.HasPrefix(cluster.ServiceProviderProperties.PendingClusterServiceID.String(), "/api/aro_hcp/v1alpha1/clusters/"))
				assert.Len(t, cluster.ServiceProviderProperties.PendingClusterServiceID.ID(), 32)
			},
		},
		{
			name:        "skip when placement not resolved (Spec.ManagementClusterResourceID nil)",
			listCluster: newTestCluster(),
			listSPC:     newTestSPC(), // Spec.ManagementClusterResourceID nil
			dbCluster:   newTestCluster(),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
		{
			name:        "skip when ServiceProviderCluster not found",
			listCluster: newTestCluster(),
			listSPC:     nil,
			dbCluster:   newTestCluster(),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
		{
			name: "skip when PendingClusterServiceID already set",
			listCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &clusterInternalID
			}),
			listSPC: placedSPC(),
			dbCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.PendingClusterServiceID = &clusterInternalID
			}),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				require.NotNil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
				assert.Equal(t, testClusterServiceIDStr, cluster.ServiceProviderProperties.PendingClusterServiceID.String())
			},
		},
		{
			name: "skip when ClusterServiceID already set",
			listCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			listSPC: placedSPC(),
			dbCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				c.ServiceProviderProperties.ClusterServiceID = &clusterInternalID
			}),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
		{
			name: "skip when cluster is being deleted",
			listCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			listSPC: placedSPC(),
			dbCluster: newTestCluster(func(c *coreapi.HCPOpenShiftCluster) {
				now := metav1.Now()
				c.ServiceProviderProperties.DeletionTimestamp = &now
			}),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
		{
			name:        "skip when cluster not found in lister",
			listCluster: nil,
			listSPC:     placedSPC(),
			dbCluster:   newTestCluster(),
			expectError: false,
			verifyDB: func(t *testing.T, ctx context.Context, db *corecosmosstoragetesting.MockResourcesDBClient) {
				cluster, err := db.HCPClusters(testSubscriptionID, testResourceGroupName).Get(ctx, testClusterName)
				require.NoError(t, err)
				assert.Nil(t, cluster.ServiceProviderProperties.PendingClusterServiceID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ctx = utils.ContextWithLogger(ctx, testr.New(t))

			mockDB, err := corecosmosstoragetesting.NewMockResourcesDBClientWithResources(ctx, []any{newTestSubscription(), tt.dbCluster})
			require.NoError(t, err)

			var listerClusters []*coreapi.HCPOpenShiftCluster
			if tt.listCluster != nil {
				listerClusters = []*coreapi.HCPOpenShiftCluster{tt.listCluster}
			}
			var listerSPCs []*coreapi.ServiceProviderCluster
			if tt.listSPC != nil {
				listerSPCs = []*coreapi.ServiceProviderCluster{tt.listSPC}
			}
			syncer := &clusterPendingClusterServiceIDAssignSyncer{
				resourcesDBClient:            mockDB,
				clusterLister:                &corelistertesting.SliceClusterLister{Clusters: listerClusters},
				serviceProviderClusterLister: &corelistertesting.SliceServiceProviderClusterLister{ServiceProviderClusters: listerSPCs},
			}

			key := controllerutils.HCPClusterKey{
				SubscriptionID:    testSubscriptionID,
				ResourceGroupName: testResourceGroupName,
				HCPClusterName:    testClusterName,
			}

			err = syncer.SyncOnce(ctx, key)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.verifyDB != nil {
				tt.verifyDB(t, ctx, mockDB)
			}
		})
	}
}
