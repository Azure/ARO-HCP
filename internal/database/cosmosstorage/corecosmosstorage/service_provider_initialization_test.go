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

package corecosmosstorage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

func TestProviderInitialization(t *testing.T) {
	for _, scenario := range []struct {
		name                                      string
		initialized, deleting, dispatched, legacy bool
		wantError                                 bool
	}{
		{name: "fresh"},
		{name: "legacy IDs", legacy: true},
		{name: "lost state", initialized: true, wantError: true},
		{name: "delete before initialization", deleting: true},
		{name: "delete after dispatch", deleting: true, dispatched: true, wantError: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := t.Context()
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
			cluster := coreapi.NewDefaultCluster(resourceID, "eastus")
			cluster.ResourceID = resourceID
			cluster.SetPartitionKey(resourceID.SubscriptionID)
			cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = scenario.initialized
			if scenario.deleting {
				now := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
			}
			if scenario.dispatched {
				now := metav1.Now()
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
			}
			if scenario.legacy {
				id := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/existing"))
				cluster.ServiceProviderProperties.PendingClusterServiceID = &id
			}
			_, err := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, cluster, nil)
			require.NoError(t, err)
			provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			if scenario.wantError {
				require.Error(t, err)
				_, err = db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
				return
			}
			require.NoError(t, err)
			require.Equal(t, cluster.ServiceProviderProperties.PendingClusterServiceID, provider.PendingClusterServiceID)
			observed, err := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
			require.NoError(t, err)
			require.True(t, observed.ServiceProviderProperties.ServiceProviderClusterInitialized)
			_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.NoError(t, err)
			after, err := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
			require.NoError(t, err)
			require.Equal(t, observed.GetEtag(), after.GetEtag())
			require.NoError(t, db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Delete(ctx, coreapi.ServiceProviderClusterResourceName))
			_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.Error(t, err, "lost initialized provider must not be recreated")
		})
	}
}

func TestProviderInitializationSourceConflict(t *testing.T) {
	ctx := t.Context()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
	cluster := coreapi.NewDefaultCluster(resourceID, "eastus")
	cluster.ResourceID = resourceID
	cluster.SetPartitionKey(resourceID.SubscriptionID)
	clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
	created, err := clusterCRUD.Create(ctx, cluster, nil)
	require.NoError(t, err)
	conflicting := &conflictingIDDB{ResourcesDBClient: db, beforeBatch: func() {
		now := metav1.Now()
		created.ServiceProviderProperties.DeletionTimestamp = &now
		_, err := clusterCRUD.Replace(ctx, created, nil)
		require.NoError(t, err)
	}}
	_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, conflicting, resourceID)
	require.True(t, cosmosstorageutils.IsPreconditionFailedError(err))
	_, err = db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
}
