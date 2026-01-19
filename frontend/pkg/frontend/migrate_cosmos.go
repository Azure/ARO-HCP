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
	// this does the rekeying that we have no other way of knowing is complete
	for _, subscription := range subscriptionIterator.Items(ctx) {
		if _, err := cosmosClient.Subscriptions().Get(ctx, subscription.ResourceID.Name); err != nil {
			panic(err)
		}
	}
	if err := subscriptionIterator.GetError(); err != nil {
		panic(err)
	}

	subscriptionIterator, err = cosmosClient.Subscriptions().List(ctx, nil)
	if err != nil {
		panic(err)
	}
	for _, subscription := range subscriptionIterator.Items(ctx) {
		clusterIterator, err := cosmosClient.HCPClusters(subscription.ResourceID.Name, "").List(ctx, nil)
		if err != nil {
			panic(err)
		}
		for _, cluster := range clusterIterator.Items(ctx) {
			// this does the rekeying that we have no other way of knowing is complete
			currCluster, err := cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Get(ctx, cluster.ID.Name)
			if err != nil {
				panic(err)
			}
			// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
			currCluster, err = cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Replace(ctx, currCluster, nil)
			if err != nil {
				panic(err)
			}

			// this property is required during create.  If we don't have it, then we're missing the data from cluster-service in the cosmos record and need to migrate
			if len(currCluster.CustomerProperties.Platform.SubnetID) == 0 {
				// for old records that just held pointers to cluster-service, we need to get all the data and rewrite the record to contain all the data
				// This will allow a future build to treat cosmos as authoritative
				completeCurrCluster, err := readInternalClusterFromClusterService(ctx, clusterServiceClient, currCluster, azureLocation)
				if err != nil {
					panic(err)
				}
				// our linter makes me leave the currCluster object in an invalidate state that doesn't reflect the actual currCluster.
				// Apologies if this bites you, I don't feel like fighting the (wrong) bot.
				_, err = cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Replace(ctx, completeCurrCluster, nil)
				if err != nil {
					panic(err)
				}
			}

			nodePoolIterator, err := cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).NodePools(cluster.ID.Name).List(ctx, nil)
			if err != nil {
				panic(err)
			}
			for _, nodePool := range nodePoolIterator.Items(ctx) {
				// this does the rekeying that we have no other way of knowing is complete
				currNodePool, err := cosmosClient.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Get(ctx, nodePool.ID.Name)
				if err != nil {
					panic(err)
				}
				// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
				currNodePool, err = cosmosClient.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Replace(ctx, currNodePool, nil)
				if err != nil {
					panic(err)
				}

				// this property is required during create.  If we don't have it, then we're missing the data from cluster-service in the cosmos record and need to migrate
				if len(currNodePool.Properties.Platform.VMSize) == 0 {
					// for old records that just held pointers to cluster-service, we need to get all the data and rewrite the record to contain all the data
					// This will allow a future build to treat cosmos as authoritative
					completeCurrNodePool, err := readInternalNodePoolFromClusterService(ctx, clusterServiceClient, currNodePool, azureLocation)
					if err != nil {
						panic(err)
					}
					// our linter makes me leave the currNodePool object in an invalidate state that doesn't reflect the actual currNodePool.
					// Apologies if this bites you, I don't feel like fighting the (wrong) bot.
					_, err = cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Replace(ctx, completeCurrNodePool, nil)
					if err != nil {
						panic(err)
					}
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
				// this does the rekeying that we have no other way of knowing is complete
				currExternalAuth, err := cosmosClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Get(ctx, externalAuth.ID.Name)
				if err != nil {
					panic(err)
				}
				// this is unconditional because it does the serialization rewrite that we have no other way to be sure is complete
				currExternalAuth, err = cosmosClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Replace(ctx, currExternalAuth, nil)
				if err != nil {
					panic(err)
				}

				// this property is required during create.  If we don't have it, then we're missing the data from cluster-service in the cosmos record and need to migrate
				if len(currExternalAuth.Properties.Issuer.URL) == 0 {
					// for old records that just held pointers to cluster-service, we need to get all the data and rewrite the record to contain all the data
					// This will allow a future build to treat cosmos as authoritative
					completeCurrExternalAuth, err := readInternalExternalAuthFromClusterService(ctx, clusterServiceClient, currExternalAuth, azureLocation)
					if err != nil {
						panic(err)
					}
					// our linter makes me leave the currExternalAuth object in an invalidate state that doesn't reflect the actual currExternalAuth.
					// Apologies if this bites you, I don't feel like fighting the (wrong) bot.
					_, err = cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Replace(ctx, completeCurrExternalAuth, nil)
					if err != nil {
						panic(err)
					}
				}
			}
			if err := externalAuthIterator.GetError(); err != nil {
				panic(err)
			}
		}
		if err := clusterIterator.GetError(); err != nil {
			panic(err)
		}

		operationIterator, err := cosmosClient.Operations(subscription.ResourceID.Name).List(ctx, nil)
		if err != nil {
			panic(err)
		}
		for _, operation := range operationIterator.Items(ctx) {
			// this does the rekeying that we have no other way of knowing is complete
			_, err := cosmosClient.Operations(operation.ResourceID.SubscriptionID).Get(ctx, operation.ResourceID.Name)
			if err != nil {
				panic(err)
			}
		}
		if err := operationIterator.GetError(); err != nil {
			panic(err)
		}
	}
}
