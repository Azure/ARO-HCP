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

package corecosmosstorage

import (
	"context"
	"reflect"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
)

func ReplaceClusterWithServiceProviderIDs(ctx context.Context, db ResourcesDBClient, cluster *coreapi.Cluster) error {
	cluster = cluster.DeepCopy()
	if cluster.ServiceProviderProperties.PendingClusterServiceID != nil && len(cluster.ServiceProviderProperties.PendingClusterServiceID.String()) == 0 {
		cluster.ServiceProviderProperties.PendingClusterServiceID = nil
	}
	if cluster.ServiceProviderProperties.ClusterServiceID != nil && len(cluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		cluster.ServiceProviderProperties.ClusterServiceID = nil
	}
	cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = true
	if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil && cluster.ServiceProviderProperties.PendingClusterServiceID == nil && cluster.ServiceProviderProperties.ClusterServiceID == nil {
		cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = true
	}
	providerCRUD := db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	provider, err := providerCRUD.Get(ctx, coreapi.ServiceProviderClusterResourceName)
	missing := cosmosstorageutils.IsNotFoundError(err)
	if err != nil && !missing {
		return err
	}
	if missing {
		provider = newInitialServiceProviderCluster(cluster.ID)
		err = nil
	}
	provider.PendingClusterServiceID = cluster.ServiceProviderProperties.PendingClusterServiceID.DeepCopy()
	provider.ClusterServiceID = cluster.ServiceProviderProperties.ClusterServiceID.DeepCopy()
	transaction := db.NewTransaction(cluster.GetPartitionKey())
	if _, err := db.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).AddReplaceToTransaction(ctx, transaction, cluster, nil); err != nil {
		return err
	}
	if missing {
		if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp == nil || provider.PendingClusterServiceID != nil || provider.ClusterServiceID != nil {
			_, err = providerCRUD.AddCreateToTransaction(ctx, transaction, provider, nil)
		}
	} else {
		_, err = providerCRUD.AddReplaceToTransaction(ctx, transaction, provider, nil)
	}
	if err != nil {
		return err
	}
	_, err = transaction.Execute(ctx, nil)
	return err
}

func SyncClusterServiceIDs(ctx context.Context, db ResourcesDBClient, subscriptionID, resourceGroup, clusterName string) error {
	cluster, err := db.HCPClusters(subscriptionID, resourceGroup).Get(ctx, clusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	properties := cluster.ServiceProviderProperties
	idsCanonical := (properties.PendingClusterServiceID == nil || len(properties.PendingClusterServiceID.String()) > 0) && (properties.ClusterServiceID == nil || len(properties.ClusterServiceID.String()) > 0)
	if !idsCanonical || !cluster.ServiceProviderProperties.ServiceProviderClusterInitialized || (cluster.ServiceProviderProperties.DeletionTimestamp != nil && cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil && cluster.ServiceProviderProperties.PendingClusterServiceID == nil && cluster.ServiceProviderProperties.ClusterServiceID == nil && !cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted) {
		return ReplaceClusterWithServiceProviderIDs(ctx, db, cluster)
	}
	provider, err := db.ServiceProviderClusters(subscriptionID, resourceGroup, clusterName).Get(ctx, coreapi.ServiceProviderClusterResourceName)
	if cosmosstorageutils.IsNotFoundError(err) {
		if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil && cluster.ServiceProviderProperties.PendingClusterServiceID == nil && cluster.ServiceProviderProperties.ClusterServiceID == nil {
			return nil
		}
	} else if err != nil {
		return err
	} else if reflect.DeepEqual(provider.PendingClusterServiceID, cluster.ServiceProviderProperties.PendingClusterServiceID) && reflect.DeepEqual(provider.ClusterServiceID, cluster.ServiceProviderProperties.ClusterServiceID) {
		return nil
	}
	return ReplaceClusterWithServiceProviderIDs(ctx, db, cluster)
}
