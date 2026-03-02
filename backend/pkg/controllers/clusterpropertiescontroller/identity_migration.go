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
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// identityMigrationSyncer is a Cluster syncer that migrates cluster identity information
// from Cluster Service to Cosmos DB. It ensures that the Identity.UserAssignedIdentities
// field is populated for clusters that were created before all identity state was held in Cosmos.
type identityMigrationSyncer struct {
	cooldownChecker controllerutils.CooldownChecker

	clusterLister        listers.ClusterLister
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*identityMigrationSyncer)(nil)

// NewIdentityMigrationController creates a new controller that migrates identity information
// from Cluster Service to Cosmos DB.
// It periodically checks each cluster and populates the Identity.UserAssignedIdentities
// field if it is not set, using SetClusterServiceOnlyFieldsOnCluster to extract the identity data.
func NewIdentityMigrationController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()

	syncer := &identityMigrationSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:        clusterLister,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"IdentityMigration",
		cosmosClient,
		informers,
		60*time.Minute, // Check every 60 minutes
		syncer,
	)

	return controller
}

func (c *identityMigrationSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

func (c *identityMigrationSyncer) NeedsWork(ctx context.Context, existingCluster *api.HCPOpenShiftCluster) bool {
	// Check if we have a cluster service ID to query
	if len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return false
	}

	// Check if identity information needs to be migrated
	// Records that have UserAssignedIdentities already have all the identity info stored in cosmos
	// Records that don't have this information need to be migrated
	if existingCluster.Identity == nil {
		return true
	}
	if len(existingCluster.Identity.UserAssignedIdentities) == 0 {
		return true
	}

	return false
}

// SyncOnce performs a single reconciliation of cluster identity information.
// It checks if the Identity.UserAssignedIdentities field is unset,
// and if so, fetches the values from Cluster Service using
// SetClusterServiceOnlyFieldsOnCluster and updates Cosmos with
// the Identity.UserAssignedIdentities only.
func (c *identityMigrationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	// check if we need to do work again. Sometimes the live data is ahead of the cache and obviates the need to do any work
	if !c.NeedsWork(ctx, existingCluster) {
		return nil
	}

	// Fetch the cluster from Cluster Service
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Use SetClusterServiceOnlyFieldsOnCluster on a deep copy to extract identity data
	clusterCopy := existingCluster.DeepCopy()
	ocm.SetClusterServiceOnlyFieldsOnCluster(clusterCopy, csCluster)

	// nothing to set
	if clusterCopy.Identity == nil || len(clusterCopy.Identity.UserAssignedIdentities) == 0 {
		return nil
	}

	// Only assign the Identity.UserAssignedIdentities from the converted cluster
	if existingCluster.Identity == nil {
		existingCluster.Identity = &arm.ManagedServiceIdentity{}
	}
	existingCluster.Identity.UserAssignedIdentities = clusterCopy.Identity.UserAssignedIdentities

	// Write the updated cluster back to Cosmos
	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("migrated identity information from Cluster Service")
	return nil
}
