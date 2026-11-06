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

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

func ReplaceClusterWithServiceProviderIDs(ctx context.Context, db ResourcesDBClient, cluster *coreapi.Cluster, provider *coreapi.ServiceProviderCluster) error {
	cluster = cluster.DeepCopy()
	provider = provider.DeepCopy()
	if provider.PendingClusterServiceID != nil && len(provider.PendingClusterServiceID.String()) == 0 {
		provider.PendingClusterServiceID = nil
	}
	if provider.ClusterServiceID != nil && len(provider.ClusterServiceID.String()) == 0 {
		provider.ClusterServiceID = nil
	}
	cluster.ServiceProviderProperties.ServiceProviderClusterInitialized = true
	if cluster.ServiceProviderProperties.ClusterServiceDeletionTimestamp != nil && provider.PendingClusterServiceID == nil && provider.ClusterServiceID == nil {
		cluster.ServiceProviderProperties.ClusterServiceDeletionCompleted = true
	}
	providerCRUD := db.ServiceProviderClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName, cluster.ID.Name)
	missing := len(provider.GetEtag()) == 0
	var err error
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
