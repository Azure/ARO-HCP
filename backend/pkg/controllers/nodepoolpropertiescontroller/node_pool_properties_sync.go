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

package nodepoolpropertiescontroller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type nodePoolPropertiesSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	nodePoolLister       listers.NodePoolLister
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*nodePoolPropertiesSyncer)(nil)

// NewNodePoolPropertiesSyncController creates a new controller that synchronizes
// node pool properties from Cluster Service to Cosmos DB.
func NewNodePoolPropertiesSyncController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	syncer := &nodePoolPropertiesSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:       nodePoolLister,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"NodePoolPropertiesSync",
		cosmosClient,
		informers,
		time.Hour,
		syncer,
	)

	return controller
}

func (c *nodePoolPropertiesSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// needsVersionIDSync returns true when versionID is empty or not valid semver (e.g. only x.y), so existing node pools are migrated from Cluster Service.
func (c *nodePoolPropertiesSyncer) needsVersionIDSync(versionID string) bool {
	_, err := semver.Parse(versionID)
	return err != nil
}

// SyncOnce performs a single reconciliation of node pool properties from Cluster Service to Cosmos.
func (c *nodePoolPropertiesSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if len(cachedNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}
	needsVersionSync := c.needsVersionIDSync(cachedNodePool.Properties.Version.ID)
	needsChannelSync := len(cachedNodePool.Properties.Version.ChannelGroup) == 0
	if !needsVersionSync && !needsChannelSync {
		return nil
	}

	nodePoolCRUD := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName)
	existingNodePool, err := nodePoolCRUD.Get(ctx, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if len(existingNodePool.ServiceProviderProperties.ClusterServiceID.String()) == 0 {
		return nil
	}
	needsVersionSync = c.needsVersionIDSync(existingNodePool.Properties.Version.ID)
	needsChannelSync = len(existingNodePool.Properties.Version.ChannelGroup) == 0
	if !needsVersionSync && !needsChannelSync {
		return nil
	}

	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, existingNodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from Cluster Service: %w", err))
	}

	originalNodePool := existingNodePool.DeepCopy()

	version := csNodePool.Version()
	if needsVersionSync {
		existingNodePool.Properties.Version.ID = version.RawID()
	}
	if needsChannelSync {
		existingNodePool.Properties.Version.ChannelGroup = version.ChannelGroup()
	}

	if equality.Semantic.DeepEqual(originalNodePool, existingNodePool) {
		return nil
	}

	if _, err := nodePoolCRUD.Replace(ctx, existingNodePool, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace NodePool: %w", err))
	}

	logger.Info("synced node pool properties from Cluster Service")
	return nil
}
