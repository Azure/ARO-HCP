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

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

// MigrateCosmosOrDie if migration fails, we panic and exit the process.  This makes it very detectable.
func MigrateCosmosOrDie(ctx context.Context, cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec, azureLocation string) {
	// This is a temporary change. Once deployed to production, we will remove this content and leave it empty
	// for the next small migration we need to do.  Once datasets are large, we will start doing this inside of the backend.

	subscriptionIterator, err := cosmosClient.Subscriptions().List(ctx, nil)
	if err != nil {
		panic(err)
	}
	for _, subscription := range subscriptionIterator.Items(ctx) {
		clusterIterator, err := cosmosClient.HCPClusters(subscription.ResourceID.Name, "").List(ctx, nil)
		if err != nil {
			panic(err)
		}
		for _, cluster := range clusterIterator.Items(ctx) {
			// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
			_, err = cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Replace(ctx, cluster, nil)
			if err != nil {
				panic(err)
			}

			nodePoolIterator, err := cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).NodePools(cluster.ID.Name).List(ctx, nil)
			if err != nil {
				panic(err)
			}
			for _, nodePool := range nodePoolIterator.Items(ctx) {
				// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
				_, err = cosmosClient.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Replace(ctx, nodePool, nil)
				if err != nil {
					panic(err)
				}
			}
			if err := nodePoolIterator.GetError(); err != nil {
				panic(err)
			}

			externalAuthIterator, err := cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).ExternalAuth(cluster.ID.Name).List(ctx, nil)
			if err != nil {
				panic(err)
			}
			for _, externalAuth := range externalAuthIterator.Items(ctx) {
				// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
				_, err = cosmosClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Replace(ctx, externalAuth, nil)
				if err != nil {
					panic(err)
				}
			}
			if err := externalAuthIterator.GetError(); err != nil {
				panic(err)
			}
		}
		if err := clusterIterator.GetError(); err != nil {
			panic(err)
		}
	}
}
