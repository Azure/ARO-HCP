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
	"fmt"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// MigrateCosmosOrDie if migration fails, we panic and exit the process.  This makes it very detectable.
func MigrateCosmosOrDie(ctx context.Context, resourcesDBClient database.ResourcesDBClient) {
	logger := utils.LoggerFromContext(ctx)

	// This is a temporary change. Once deployed to production, we will remove this content and leave it empty
	// for the next small migration we need to do.  Once datasets are large, we will start doing this inside of the backend.

	subscriptionIterator, err := resourcesDBClient.Subscriptions().List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list subscriptions")
		panic(err)
	}

	logger.Info("migrating subscriptions")
	retrievedSubscriptionsForMigrationCount := 0
	for _, subscription := range subscriptionIterator.Items(ctx) {
		retrievedSubscriptionsForMigrationCount++
		currSubscription, err := resourcesDBClient.Subscriptions().Get(ctx, subscription.ResourceID.Name)
		if err != nil {
			logger.Error(err, "failed to get subscription", "subscription", subscription.ResourceID, "subscriptionsRetrieved", retrievedSubscriptionsForMigrationCount)
			panic(err)
		}
		_, err = resourcesDBClient.Subscriptions().Replace(ctx, currSubscription, nil)
		if err != nil {
			logger.Error(err, "failed to replace subscription", "subscription", subscription.ResourceID, "subscriptionsRetrieved", retrievedSubscriptionsForMigrationCount)
			panic(err)
		}
	}
	if err := subscriptionIterator.GetError(); err != nil {
		logger.Error(err, "failed to iterate subscriptions", "subscriptionsRetrieved", retrievedSubscriptionsForMigrationCount)
		panic(err)
	}
	logger.Info(fmt.Sprintf("subscriptions migrated. Retrieved %d subscriptions", retrievedSubscriptionsForMigrationCount))

	logger.Info("migrating resources within subscriptions")
	retrievedSubscriptionsCount := 0
	subscriptionIterator, err = resourcesDBClient.Subscriptions().List(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to list subscriptions")
		panic(err)
	}
	for _, subscription := range subscriptionIterator.Items(ctx) {
		retrievedSubscriptionsCount++

		clusterIterator, err := resourcesDBClient.HCPClusters(subscription.ResourceID.Name, "").List(ctx, nil)
		if err != nil {
			logger.Error(err, "failed to list clusters", "subscription", subscription.ResourceID, "subscriptionsRetrieved", retrievedSubscriptionsCount)
			panic(err)
		}
		retrievedClustersCount := 0
		for _, cluster := range clusterIterator.Items(ctx) {
			retrievedClustersCount++
			currCluster, err := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Get(ctx, cluster.ID.Name)
			if err != nil {
				logger.Error(err, "failed to get cluster", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
				panic(err)
			}
			_, err = resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Replace(ctx, currCluster, nil)
			if err != nil {
				logger.Error(err, "failed to replace cluster", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
				panic(err)
			}

			{ // prevent variable escape
				controllerCRUD := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).Controllers(cluster.ID.Name)
				controllersIterator, err := controllerCRUD.List(ctx, nil)
				if err != nil {
					logger.Error(err, "failed to list cluster controllers", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
					panic(err)
				}
				retrievedClusterControllersCount := 0
				for _, controller := range controllersIterator.Items(ctx) {
					retrievedClusterControllersCount++
					currController, err := controllerCRUD.Get(ctx, controller.ResourceID.Name)
					if err != nil {
						logger.Error(err, "failed to get cluster controller", "clusterController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "clusterControllersRetrievedInCluster", retrievedClusterControllersCount)
						panic(err)
					}
					_, err = controllerCRUD.Replace(ctx, currController, nil)
					if err != nil {
						logger.Error(err, "failed to replace cluster controller", "clusterController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "clusterControllersRetrievedInCluster", retrievedClusterControllersCount)
						panic(err)
					}
				}
				if err := controllersIterator.GetError(); err != nil {
					logger.Error(err, "failed to iterate cluster controllers", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "clusterControllersRetrievedInCluster", retrievedClusterControllersCount)
					panic(err)
				}
				logger.Info(fmt.Sprintf("cluster controllers within cluster migrated. Retrieved %d cluster controllers", retrievedClusterControllersCount), "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
			}

			{ // prevent variable escape
				nodePoolIterator, err := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).NodePools(cluster.ID.Name).List(ctx, nil)
				if err != nil {
					logger.Error(err, "failed to list node pools", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
					panic(err)
				}
				retrievedNodePoolsCount := 0
				for _, nodePool := range nodePoolIterator.Items(ctx) {
					retrievedNodePoolsCount++
					currNodePool, err := resourcesDBClient.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Get(ctx, nodePool.ID.Name)
					if err != nil {
						logger.Error(err, "failed to get node pool", "nodePool", nodePool.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount)
						panic(err)
					}
					_, err = resourcesDBClient.HCPClusters(nodePool.ID.SubscriptionID, nodePool.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Replace(ctx, currNodePool, nil)
					if err != nil {
						logger.Error(err, "failed to replace node pool", "nodePool", nodePool.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount)
						panic(err)
					}

					controllerCRUD := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).NodePools(nodePool.ID.Parent.Name).Controllers(nodePool.ID.Name)
					controllersIterator, err := controllerCRUD.List(ctx, nil)
					if err != nil {
						logger.Error(err, "failed to list node pool controllers", "nodePool", nodePool.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount)
						panic(err)
					}
					retrievedNodePoolControllersCount := 0
					for _, controller := range controllersIterator.Items(ctx) {
						retrievedNodePoolControllersCount++
						currController, err := controllerCRUD.Get(ctx, controller.ResourceID.Name)
						if err != nil {
							logger.Error(err, "failed to get node pool controller", "nodePoolController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount, "nodePoolControllersRetrievedInNodePool", retrievedNodePoolControllersCount)
							panic(err)
						}
						_, err = controllerCRUD.Replace(ctx, currController, nil)
						if err != nil {
							logger.Error(err, "failed to replace node pool controller", "nodePoolController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount, "nodePoolControllersRetrievedInNodePool", retrievedNodePoolControllersCount)
							panic(err)
						}
					}
					if err := controllersIterator.GetError(); err != nil {
						logger.Error(err, "failed to iterate node pool controllers", "nodePool", nodePool.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount, "nodePoolControllersRetrievedInNodePool", retrievedNodePoolControllersCount)
						panic(err)
					}
					logger.Info(fmt.Sprintf("node pool controllers within node pool migrated. Retrieved %d node pool controllers", retrievedNodePoolControllersCount), "nodePool", nodePool.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount, "nodePoolControllersRetrievedInNodePool", retrievedNodePoolControllersCount)
				}
				if err := nodePoolIterator.GetError(); err != nil {
					logger.Error(err, "failed to iterate node pools", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "nodePoolsRetrievedInCluster", retrievedNodePoolsCount)
					panic(err)
				}
				logger.Info(fmt.Sprintf("node pools within cluster migrated. Retrieved %d node pools", retrievedNodePoolsCount), "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
			}

			{ // prevent variable escape
				externalAuthIterator, err := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).ExternalAuth(cluster.ID.Name).List(ctx, nil)
				if err != nil {
					logger.Error(err, "failed to list external auths", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount)
					panic(err)
				}
				retrievedExternalAuthsCount := 0
				for _, externalAuth := range externalAuthIterator.Items(ctx) {
					retrievedExternalAuthsCount++
					currExternalAuth, err := resourcesDBClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Get(ctx, externalAuth.ID.Name)
					if err != nil {
						logger.Error(err, "failed to get external auth", "externalAuth", externalAuth.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
						panic(err)
					}
					_, err = resourcesDBClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Replace(ctx, currExternalAuth, nil)
					if err != nil {
						logger.Error(err, "failed to replace external auth", "externalAuth", externalAuth.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
						panic(err)
					}

					controllerCRUD := resourcesDBClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).Controllers(externalAuth.ID.Name)
					controllersIterator, err := controllerCRUD.List(ctx, nil)
					if err != nil {
						logger.Error(err, "failed to list external auth controllers", "externalAuth", externalAuth.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
						panic(err)
					}
					retrievedExternalAuthControllersCount := 0
					for _, controller := range controllersIterator.Items(ctx) {
						retrievedExternalAuthControllersCount++
						currController, err := controllerCRUD.Get(ctx, controller.ResourceID.Name)
						if err != nil {
							logger.Error(err, "failed to get external auth controller", "externalAuthController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount, "externalAuthControllersRetrievedInExternalAuth", retrievedExternalAuthControllersCount)
							panic(err)
						}
						_, err = controllerCRUD.Replace(ctx, currController, nil)
						if err != nil {
							logger.Error(err, "failed to replace external auth controller", "externalAuthController", controller.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount, "externalAuthControllersRetrievedInExternalAuth", retrievedExternalAuthControllersCount)
							panic(err)
						}
					}
					if err := controllersIterator.GetError(); err != nil {
						logger.Error(err, "failed to iterate external auth controllers", "externalAuth", externalAuth.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount, "externalAuthControllersRetrievedInExternalAuth", retrievedExternalAuthControllersCount)
						panic(err)
					}
					logger.Info(fmt.Sprintf("external auth controllers within external auth migrated. Retrieved %d external auth controllers", retrievedExternalAuthControllersCount), "externalAuth", externalAuth.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
				}
				if err := externalAuthIterator.GetError(); err != nil {
					logger.Error(err, "failed to iterate external auths", "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
					panic(err)
				}
				logger.Info(fmt.Sprintf("external auths within cluster migrated. Retrieved %d external auths", retrievedExternalAuthsCount), "cluster", cluster.ID, "clustersRetrievedInSubscription", retrievedClustersCount, "externalAuthsRetrievedInCluster", retrievedExternalAuthsCount)
			}
		}
		if err := clusterIterator.GetError(); err != nil {
			logger.Error(err, "failed to iterate clusters", "subscription", subscription.ResourceID, "clustersRetrievedInSubscription", retrievedClustersCount)
			panic(err)
		}
		logger.Info(fmt.Sprintf("clusters within subscription migrated. Retrieved %d clusters", retrievedClustersCount), "subscription", subscription.ResourceID)
	}
	logger.Info(fmt.Sprintf("migration of resources within subscriptions completed. %d subscriptions have been retrieved", retrievedSubscriptionsCount))
}
