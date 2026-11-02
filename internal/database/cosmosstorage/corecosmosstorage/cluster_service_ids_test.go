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
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

func TestLegacyClusterIDsAreIgnored(t *testing.T) {
	var cluster coreapi.Cluster
	require.NoError(t, json.Unmarshal([]byte(`{"serviceProviderProperties":{"pendingClusterServiceID":"/api/aro_hcp/v1alpha1/clusters/old-pending","clusterServiceID":"/api/aro_hcp/v1alpha1/clusters/old-final","serviceProviderClusterInitialized":true}}`), &cluster))
	require.True(t, cluster.ServiceProviderProperties.ServiceProviderClusterInitialized)
	encoded, err := json.Marshal(cluster)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "pendingClusterServiceID")
	require.NotContains(t, string(encoded), "clusterServiceID")
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	final := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/final"))
	provider := coreapi.ServiceProviderCluster{PendingClusterServiceID: &pending, ClusterServiceID: &final}
	encoded, err = json.Marshal(provider)
	require.NoError(t, err)
	var observed coreapi.ServiceProviderCluster
	require.NoError(t, json.Unmarshal(encoded, &observed))
	require.Equal(t, provider.PendingClusterServiceID, observed.PendingClusterServiceID)
	require.Equal(t, provider.ClusterServiceID, observed.ClusterServiceID)
}

type conflictingIDDB struct {
	corecosmosstorage.ResourcesDBClient
	beforeBatch func()
}

func TestReadClusterServiceProviderDoesNotFallBack(t *testing.T) {
	ctx := context.Background()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
	initial := coreapi.NewDefaultCluster(resourceID, "eastus")
	initial.ResourceID = resourceID
	initial.SetPartitionKey(resourceID.SubscriptionID)
	_, err := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Create(ctx, initial, nil)
	require.NoError(t, err)
	_, err = corecosmosstorage.ReadClusterServiceProvider(ctx, db, resourceID)
	require.True(t, cosmosstorageutils.IsNotFoundError(err))
	provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
	require.NoError(t, err)
	newID := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	provider.PendingClusterServiceID = &newID
	_, err = db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Replace(ctx, provider, nil)
	require.NoError(t, err)
	observed, err := corecosmosstorage.ReadClusterServiceProvider(ctx, db, resourceID)
	require.NoError(t, err)
	require.Equal(t, &newID, observed.PendingClusterServiceID)
	require.Nil(t, observed.ClusterServiceID, "pending state must not imply a final ID")
}

func TestClusterServiceIDPromotionAndClear(t *testing.T) {
	ctx := context.Background()
	db := corecosmosstoragetesting.NewMockResourcesDBClient()
	resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
	initial := coreapi.NewDefaultCluster(resourceID, "eastus")
	initial.SetPartitionKey(resourceID.SubscriptionID)
	initial.ResourceID = resourceID
	clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
	_, err := clusterCRUD.Create(ctx, initial, nil)
	require.NoError(t, err)
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	final := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/final"))
	for _, transition := range []struct{ pending, final *metadataapi.InternalID }{{&pending, nil}, {nil, &final}, {nil, nil}} {
		provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
		require.NoError(t, err)
		cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		provider.PendingClusterServiceID = transition.pending
		provider.ClusterServiceID = transition.final
		require.NoError(t, corecosmosstorage.ReplaceClusterWithServiceProviderIDs(ctx, db, cluster, provider))
		observed, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		provider, err = db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.True(t, observed.ServiceProviderProperties.ServiceProviderClusterInitialized)
		require.Equal(t, transition.pending, provider.PendingClusterServiceID)
		require.Equal(t, transition.final, provider.ClusterServiceID)
	}
}

func (db *conflictingIDDB) NewTransaction(partitionKey string) cosmosstorageutils.DBTransaction {
	db.beforeBatch()
	return db.ResourcesDBClient.NewTransaction(partitionKey)
}

func TestClusterServiceIDConflicts(t *testing.T) {
	for _, destinationConflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "destination"}[destinationConflict], func(t *testing.T) {
			ctx := context.Background()
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
			clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
			initial := coreapi.NewDefaultCluster(resourceID, "eastus")
			initial.SetPartitionKey(resourceID.SubscriptionID)
			initial.ResourceID = resourceID
			_, err := clusterCRUD.Create(ctx, initial, nil)
			require.NoError(t, err)
			provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
			require.NoError(t, err)
			cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
			require.NoError(t, err)
			providerCRUD := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			conflicting := &conflictingIDDB{ResourcesDBClient: db, beforeBatch: func() {
				if destinationConflict {
					provider.Spec.DesiredDNSBaseDomain = "concurrent.example"
					_, err = providerCRUD.Replace(ctx, provider, nil)
				} else {
					_, err = clusterCRUD.Replace(ctx, cluster.DeepCopy(), nil)
				}
				require.NoError(t, err)
			}}
			pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
			replacement := provider.DeepCopy()
			replacement.PendingClusterServiceID = &pending
			err = corecosmosstorage.ReplaceClusterWithServiceProviderIDs(ctx, conflicting, cluster, replacement)
			require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "both documents require their observed ETag: %v", err)
			observed, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Nil(t, observed.PendingClusterServiceID)
			if destinationConflict {
				require.Equal(t, "concurrent.example", observed.Spec.DesiredDNSBaseDomain)
			}
		})
	}
}
