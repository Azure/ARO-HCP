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

package frontend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/test-integration/utils/integrationutils"
)

const clusterIDTransactionSubscription = "00000000-0000-0000-0000-000000000000"

type clusterIDConflictDB struct {
	corecosmosstorage.ResourcesDBClient
	beforeBatch func()
}

func (db *clusterIDConflictDB) NewTransaction(partitionKey string) cosmosstorageutils.DBTransaction {
	db.beforeBatch()
	return db.ResourcesDBClient.NewTransaction(partitionKey)
}

func replaceClusterIDsForTransactionTest(ctx context.Context, db corecosmosstorage.ResourcesDBClient, cluster *coreapi.Cluster, provider *coreapi.ServiceProviderCluster) error {
	cluster.ServiceProviderProperties.PendingClusterServiceID = provider.PendingClusterServiceID.DeepCopy()
	cluster.ServiceProviderProperties.ClusterServiceID = provider.ClusterServiceID.DeepCopy()
	return corecosmosstorage.ReplaceClusterWithServiceProviderIDs(ctx, db, cluster)
}

func TestClusterIDTransactionalRollback(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		if withMock {
			t.Skip("sequential mock transactions cannot prove rollback")
		}
		for _, operation := range []string{"initialize", "replace"} {
			for _, conflict := range []string{"source", "destination"} {
				t.Run(operation+"/"+conflict, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
					defer cancel()
					storage, err := integrationutils.NewCosmosFromTestingEnv(ctx, t)
					require.NoError(t, err)
					defer storage.Cleanup(context.Background())
					db := storage.ResourcesDBClient()
					resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(clusterIDTransactionSubscription, "rg", "transaction-test"))
					clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
					providerCRUD := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
					initial := coreapi.NewDefaultCluster(resourceID, "eastus")
					initial.ResourceID = resourceID
					initial.SetPartitionKey(resourceID.SubscriptionID)
					_, err = clusterCRUD.Create(ctx, initial, nil)
					require.NoError(t, err)
					var provider *coreapi.ServiceProviderCluster
					if operation == "replace" {
						provider, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
						require.NoError(t, err)
					}
					cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
					require.NoError(t, err)
					expectedCluster := cluster.DeepCopy()
					expectedProvider := provider.DeepCopy()
					conflicting := &clusterIDConflictDB{ResourcesDBClient: db, beforeBatch: func() {
						if conflict == "source" {
							concurrent := cluster.DeepCopy()
							concurrent.ServiceProviderProperties.ActiveOperationID = "concurrent"
							expectedCluster, err = clusterCRUD.Replace(ctx, concurrent, nil)
						} else if operation == "replace" {
							concurrent := provider.DeepCopy()
							concurrent.Spec.DesiredDNSBaseDomain = "concurrent.example"
							expectedProvider, err = providerCRUD.Replace(ctx, concurrent, nil)
						} else {
							concurrent := &coreapi.ServiceProviderCluster{CosmosMetadata: coreapi.CosmosMetadata{
								ResourceID:   metadataapi.Must(azcorearm.ParseResourceID(resourceID.String() + "/serviceProviderClusters/default")),
								PartitionKey: resourceID.SubscriptionID,
							}}
							concurrent.Spec.DesiredDNSBaseDomain = "concurrent.example"
							expectedProvider, err = providerCRUD.Create(ctx, concurrent, nil)
						}
						require.NoError(t, err)
					}}
					if operation == "initialize" {
						_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, conflicting, resourceID)
					} else {
						attempted := cluster.DeepCopy()
						attempted.ServiceProviderProperties.ActiveOperationID = "must-rollback"
						pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
						replacement := provider.DeepCopy()
						replacement.PendingClusterServiceID = &pending
						err = replaceClusterIDsForTransactionTest(ctx, conflicting, attempted, replacement)
					}
					if operation == "initialize" && conflict == "destination" {
						require.True(t, cosmosstorageutils.IsConflictError(err), "expected create conflict: %v", err)
					} else {
						require.True(t, cosmosstorageutils.IsPreconditionFailedError(err), "expected ETag conflict: %v", err)
					}
					observedCluster, err := clusterCRUD.Get(ctx, resourceID.Name)
					require.NoError(t, err)
					require.Equal(t, expectedCluster, observedCluster, "failed batch must preserve the entire source, including ETag and initialization marker")
					observedProvider, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
					if expectedProvider == nil {
						require.True(t, cosmosstorageutils.IsNotFoundError(err))
					} else {
						require.NoError(t, err)
						require.Equal(t, expectedProvider, observedProvider, "failed batch must preserve concurrent destination state")
					}
				})
			}
		}
	})
}

func TestClusterIDTransactionalLifecycle(t *testing.T) {
	integrationutils.WithAndWithoutCosmos(t, func(t *testing.T, withMock bool) {
		if withMock {
			t.Skip("exercise the production Cosmos batch implementation")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
		defer cancel()
		storage, err := integrationutils.NewCosmosFromTestingEnv(ctx, t)
		require.NoError(t, err)
		defer storage.Cleanup(context.Background())
		db := storage.ResourcesDBClient()
		resourceID := metadataapi.Must(coreapihelpers.ToClusterResourceID(clusterIDTransactionSubscription, "rg", "lifecycle-test"))
		clusterCRUD := db.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName)
		providerCRUD := db.ServiceProviderClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName, resourceID.Name)
		initial := coreapi.NewDefaultCluster(resourceID, "eastus")
		initial.ResourceID = resourceID
		initial.SetPartitionKey(resourceID.SubscriptionID)
		_, err = clusterCRUD.Create(ctx, initial, nil)
		require.NoError(t, err)
		provider, err := corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
		require.NoError(t, err)
		cluster, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		require.True(t, cluster.ServiceProviderProperties.ServiceProviderClusterInitialized)
		require.Nil(t, provider.PendingClusterServiceID)
		require.Nil(t, provider.ClusterServiceID)
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
		require.NoError(t, err)
		after, err := clusterCRUD.Get(ctx, resourceID.Name)
		require.NoError(t, err)
		require.Equal(t, cluster.GetEtag(), after.GetEtag())
		pending := metadataapi.Must(metadataapi.NewInternalID("/api/aro_hcp/v1alpha1/clusters/pending"))
		final := metadataapi.Must(metadataapi.NewInternalID("/api/clusters_mgmt/v1/clusters/final"))
		for _, transition := range []struct{ pending, final *metadataapi.InternalID }{{&pending, nil}, {nil, &final}, {nil, nil}} {
			if transition.pending == nil && transition.final == nil {
				now := metav1.Now()
				cluster.ServiceProviderProperties.DeletionTimestamp = &now
				cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp = &now
			}
			provider.PendingClusterServiceID = transition.pending
			provider.ClusterServiceID = transition.final
			require.NoError(t, replaceClusterIDsForTransactionTest(ctx, db, cluster, provider))
			cluster, err = clusterCRUD.Get(ctx, resourceID.Name)
			require.NoError(t, err)
			provider, err = providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
			require.NoError(t, err)
			require.Equal(t, transition.pending, provider.PendingClusterServiceID)
			require.Equal(t, transition.final, provider.ClusterServiceID)
			require.Equal(t, transition.pending, cluster.ServiceProviderProperties.PendingClusterServiceID)
			require.Equal(t, transition.final, cluster.ServiceProviderProperties.ClusterServiceID)
		}
		require.True(t, cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted)
		require.NoError(t, providerCRUD.Delete(ctx, coreapi.ServiceProviderClusterResourceName))
		_, err = corecosmosstorage.GetOrCreateServiceProviderCluster(ctx, db, resourceID)
		require.Error(t, err, "completed deletion must not recreate provider state")
		_, err = providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
		require.True(t, cosmosstorageutils.IsNotFoundError(err))
	})
}
