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

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterPropertiesSyncer is a Cluster syncer that synchronizes cluster properties
// from Cluster Service to Cosmos DB. It ensures that the following fields are populated:
//   - ServiceProviderProperties.Console.URL
//   - ServiceProviderProperties.DNS.BaseDomain
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
// and DNS.BaseDomainPrefix fields if they are not set.
func NewClusterPropertiesSyncController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	clusterInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &clusterPropertiesSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ClusterPropertiesSync",
		cosmosClient,
		clusterInformer,
		5*time.Minute, // Check every 5 minutes
		syncer,
	)

	return controller
}

func (c *clusterPropertiesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of cluster properties.
// It checks if the Console.URL, DNS.BaseDomain, or DNS.BaseDomainPrefix fields
// are unset, and if so, fetches the values from Cluster Service and updates Cosmos.
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

	// Check if any of the properties need to be synced
	needsConsoleURL := existingCluster.ServiceProviderProperties.Console.URL == ""
	needsBaseDomain := existingCluster.ServiceProviderProperties.DNS.BaseDomain == ""
	needsBaseDomainPrefix := existingCluster.CustomerProperties.DNS.BaseDomainPrefix == ""

	if !needsConsoleURL && !needsBaseDomain && !needsBaseDomainPrefix {
		logger.Info("all properties already set, nothing to sync")
		return nil
	}

	// Check if we have a cluster service ID to query
	if len(existingCluster.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		logger.Info("cluster service ID not set, cannot sync properties")
		return nil
	}

	// Fetch the cluster from Cluster Service
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Update the properties if they are not set
	updated := false

	if needsConsoleURL {
		consoleURL := csCluster.Console().URL()
		if consoleURL != "" {
			existingCluster.ServiceProviderProperties.Console.URL = consoleURL
			updated = true
			logger.Info("setting Console.URL", "url", consoleURL)
		}
	}

	if needsBaseDomain {
		baseDomain := csCluster.DNS().BaseDomain()
		if baseDomain != "" {
			existingCluster.ServiceProviderProperties.DNS.BaseDomain = baseDomain
			updated = true
			logger.Info("setting DNS.BaseDomain", "baseDomain", baseDomain)
		}
	}

	if needsBaseDomainPrefix {
		baseDomainPrefix := csCluster.DomainPrefix()
		if baseDomainPrefix != "" {
			existingCluster.CustomerProperties.DNS.BaseDomainPrefix = baseDomainPrefix
			updated = true
			logger.Info("setting DNS.BaseDomainPrefix", "baseDomainPrefix", baseDomainPrefix)
		}
	}

	if !updated {
		logger.Info("no properties available from Cluster Service to sync")
		return nil
	}

	// Write the updated cluster back to Cosmos
	if _, err := clusterCRUD.Replace(ctx, existingCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace Cluster: %w", err))
	}

	logger.Info("successfully synced cluster properties from Cluster Service")
	return nil
}
