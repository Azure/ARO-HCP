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
	"testing"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
)

func TestClusterServiceIDMigration(t *testing.T) {
	pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
	final := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/final"))
	for _, testCase := range []struct {
		name               string
		pending, final     *metadataapi.InternalID
		deleting, existing bool
	}{
		{name: "pending backfill", pending: &pending},
		{name: "final backfill", final: &final, existing: true},
		{name: "clear stale IDs", existing: true},
		{name: "deleting pending", pending: &pending, deleting: true},
		{name: "deleting final", final: &final, deleting: true},
		{name: "deleting clear", deleting: true, existing: true},
		{name: "completed deletion", deleting: true},
		{name: "empty ID", pending: &metadataapi.InternalID{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			db := corecosmosstoragetesting.NewMockResourcesDBClient()
			resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID("00000000-0000-0000-0000-000000000000", "rg", "cluster"))
			cluster := coreapi.NewDefaultCluster(resourceID, "eastus")
			cluster.SetPartitionKey(resourceID.SubscriptionID)
			cluster.ResourceID = resourceID
			cluster.ServiceProviderProperties.PendingClusterServiceID = testCase.pending
			cluster.ServiceProviderProperties.ClusterServiceID = testCase.final
			if testCase.deleting {
				now := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
			}
			clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
			_, err := clusterCRUD.Create(ctx, cluster, nil)
			require.NoError(t, err)
			providerCRUD := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
			if testCase.existing {
				providerID := metadataapi.Must(azcorearm.ParseResourceID(resourceID.String() + "/serviceProviderClusters/default"))
				provider, err := providerCRUD.Create(ctx, &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{ResourceID: providerID, PartitionKey: resourceID.SubscriptionID}}, nil)
				require.NoError(t, err)
				provider.PendingClusterServiceID = &pending
				provider.ClusterServiceID = &final
				_, err = providerCRUD.Replace(ctx, provider, nil)
				require.NoError(t, err)
			}
			require.NoError(t, corecosmosstorage.SyncClusterServiceIDs(ctx, db, resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name))
			provider, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			if testCase.deleting && !testCase.existing && testCase.pending == nil && testCase.final == nil {
				require.True(t, cosmosstorageutils.IsNotFoundError(err))
				before, err := clusterCRUD.Get(ctx, resourceID.Name)
				require.NoError(t, err)
				require.True(t, before.ServiceProviderProperties.ClusterServiceDeletionCompleted)
				require.NoError(t, corecosmosstorage.SyncClusterServiceIDs(ctx, db, resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name))
				after, err := clusterCRUD.Get(ctx, resourceID.Name)
				require.NoError(t, err)
				require.Equal(t, before.GetEtag(), after.GetEtag())
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.pending, provider.PendingClusterServiceID)
			require.Equal(t, testCase.final, provider.ClusterServiceID)
			before, err := clusterCRUD.Get(ctx, resourceID.Name)
			require.NoError(t, err)
			require.NoError(t, corecosmosstorage.SyncClusterServiceIDs(ctx, db, resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name))
			after, err := clusterCRUD.Get(ctx, resourceID.Name)
			require.NoError(t, err)
			require.Equal(t, before.GetEtag(), after.GetEtag(), "converged backfill must not rewrite the source")
			afterProvider, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, provider.GetEtag(), afterProvider.GetEtag(), "converged backfill must not rewrite the destination")
		})
	}
}

type conflictingIDDB struct {
	corecosmosstorage.ResourcesDBClient
	beforeBatch func()
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
		cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		cluster.ServiceProviderProperties.PendingClusterServiceID = transition.pending
		cluster.ServiceProviderProperties.ClusterServiceID = transition.final
		require.NoError(t, corecosmosstorage.ReplaceClusterWithServiceProviderIDs(ctx, db, cluster))
		observed, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		provider, err := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name).Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.NoError(t, err)
		require.Equal(t, transition.pending, observed.ServiceProviderProperties.PendingClusterServiceID)
		require.Equal(t, transition.final, observed.ServiceProviderProperties.ClusterServiceID)
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
			cluster.ServiceProviderProperties.PendingClusterServiceID = &pending
			err = corecosmosstorage.ReplaceClusterWithServiceProviderIDs(ctx, conflicting, cluster)
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
