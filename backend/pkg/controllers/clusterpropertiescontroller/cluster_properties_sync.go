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

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterPropertiesSyncer is a Cluster syncer that synchronizes cluster properties
// from Cluster Service to Cosmos DB. It ensures that the following fields are populated:
//   - ServiceProviderProperties.Console.URL
//   - ServiceProviderProperties.DNS.BaseDomain
//   - ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL
//   - CustomerProperties.DNS.BaseDomainPrefix
type clusterPropertiesSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*clusterPropertiesSyncer)(nil)

// NewClusterPropertiesSyncController creates a new controller that synchronizes
// cluster properties from Cluster Service to Cosmos DB.
// It periodically checks each cluster and populates the Console.URL, DNS.BaseDomain,
// ManagedIdentitiesDataPlaneIdentityURL, and DNS.BaseDomainPrefix fields if they are not set.
func NewClusterPropertiesSyncController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &clusterPropertiesSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ClusterPropertiesSync",
		cosmosClient,
		informers,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)

	return controller
}

func (c *clusterPropertiesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of cluster properties.
// It checks if the Console.URL, DNS.BaseDomain, ManagedIdentitiesDataPlaneIdentityURL,
// or DNS.BaseDomainPrefix fields are unset, and if so, fetches the values from
// Cluster Service and updates Cosmos.
func (c *clusterPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Get the cluster from Cosmos
	clusterCRUD := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	// Check if we have a cluster service ID to query
	if len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}

	// Check if any of the properties need to be synced
	needsConsoleURL := len(existingCluster.ServiceProviderProperties.Console.URL) == 0
	needsBaseDomain := len(existingCluster.ServiceProviderProperties.DNS.BaseDomain) == 0
	needsManagedIdentitiesDataPlaneIdentityURL := len(existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL) == 0
	needsBaseDomainPrefix := len(existingCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0

	if !needsConsoleURL && !needsBaseDomain && !needsBaseDomainPrefix && !needsManagedIdentitiesDataPlaneIdentityURL {
		return nil
	}

	// Fetch the cluster from Cluster Service
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Take a copy before making changes for comparison
	originalCluster := existingCluster.DeepCopy()

	// Update the properties if they are not set
	if needsConsoleURL {
		existingCluster.ServiceProviderProperties.Console.URL = csCluster.Console().URL()
	}
	if needsBaseDomain {
		existingCluster.ServiceProviderProperties.DNS.BaseDomain = csCluster.DNS().BaseDomain()
	}
	if needsManagedIdentitiesDataPlaneIdentityURL {
		if csCluster.Azure() != nil && csCluster.Azure().OperatorsAuthentication() != nil {
			if mi, ok := csCluster.Azure().OperatorsAuthentication().GetManagedIdentities(); ok {
				existingCluster.ServiceProviderProperties.ManagedIdentitiesDataPlaneIdentityURL = mi.ManagedIdentitiesDataPlaneIdentityUrl()
			}
		}
	}
	if needsBaseDomainPrefix {
		existingCluster.CustomerProperties.DNS.BaseDomainPrefix = csCluster.DomainPrefix()
	}

	// Only write back if something actually changed
	if equality.Semantic.DeepEqual(originalCluster, existingCluster) {
		return nil
	}

	// Write the updated cluster back to Cosmos
	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("synced cluster properties from Cluster Service")
	return nil
}
