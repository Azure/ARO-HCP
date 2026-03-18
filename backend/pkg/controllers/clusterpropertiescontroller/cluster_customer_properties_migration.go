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

package clusterpropertiescontroller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterCustomerPropertiesMigrationController is a Cluster controller that migrates customerProperties from cluster-service
// to cosmos DB. It uses the Version.ID and Version.ChannelGroup fields to know that customerProperties are missing.
// Old records will lack those fields and once we read from cluster-service, we'll have the information we need.
type clusterCustomerPropertiesMigrationController struct {
	cooldownChecker controllerutils.CooldownChecker

	clusterLister        listers.ClusterLister
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterCustomerPropertiesMigrationController)(nil)

func NewClusterCustomerPropertiesMigrationController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()

	syncer := &clusterCustomerPropertiesMigrationController{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ClusterServiceMigration",
		cosmosClient,
		informers,
		60*time.Minute, // Check every 60 minutes
		syncer,
	)

	return controller
}

func (c *clusterCustomerPropertiesMigrationController) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

func (c *clusterCustomerPropertiesMigrationController) NeedsWork(ctx context.Context, existingCluster *api.HCPOpenShiftCluster) bool {
	// Check if we have a cluster service ID to query. We will lack this information for newly created records when we
	// transition to async cluster-service creation.
	if len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return false
	}

	// Check if version information needs to be migrated
	// Records that have this information already, then we have all the other info stored in cosmos, so we don't need to do anything with them
	// Records that don't have this information need to be migrated
	needsVersionID := len(existingCluster.CustomerProperties.Version.ID) == 0
	needsChannelGroup := len(existingCluster.CustomerProperties.Version.ChannelGroup) == 0
	if !needsVersionID && !needsChannelGroup {
		return false
	}

	return true
}

func (c *clusterCustomerPropertiesMigrationController) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// do the super cheap cache check first
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		// we'll be re-fired if it is created again
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(ctx, cachedCluster) {
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		// if the values don't change, then we still have no work to do.
		return nil
	}

	// Get the cluster from Cosmos
	clusterCRUD := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache and obviates the need to any work
	if !c.NeedsWork(ctx, existingCluster) {
		return nil
	}

	// Fetch the cluster from Cluster Service
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Use LegacyCreateInternalClusterFromClusterService to convert the cluster and extract the CustomerProperties
	convertedCluster, err := ocm.LegacyCreateInternalClusterFromClusterService(
		existingCluster.ID,
		existingCluster.Location,
		csCluster,
	)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert cluster from Cluster Service: %w", err))
	}

	// Update only the CustomerProperties from the converted cluster
	existingCluster.CustomerProperties = convertedCluster.CustomerProperties

	// Write the updated cluster back to Cosmos
	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("migrated customer properties from Cluster Service")
	return nil
}
