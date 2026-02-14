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

package upgradecontrollers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// dataPlaneVersionSyncer reads the node pool version from Cluster Service
// and actuates the ServiceProviderNodePool data in Cosmos.
type dataPlaneVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*dataPlaneVersionSyncer)(nil)

// NewDataPlaneVersionController creates a new syncer that reads node pool versions
// from Cluster Service.
func NewDataPlaneVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	nodePoolInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &dataPlaneVersionSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"DataPlaneVersions",
		cosmosClient,
		nodePoolInformer,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// TODO this is a dummy controllers
// Error handling will be improved when implementing the real data plane version controller
func (s *dataPlaneVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)
	// Get node pool from Cosmos to get CS internal ID
	nodePool, err := s.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if err != nil {
		return fmt.Errorf("failed to get node pool from cosmos: %w", err)
	}

	// TODO Get or create the ServiceProviderNodePool for passing version information between controllers

	// Read node pool from Cluster Service
	csNodePool, err := s.clusterServiceClient.GetNodePool(ctx, nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err, "failed to get node pool from CS")
		return nil
	}

	version, ok := csNodePool.GetVersion()
	if !ok {
		logger.Error(nil, "node pool version not found in Cluster Service response")
		return nil
	}

	// Log version to test the watching controller
	logger.Info("Active version", "version", version.ID())

	return nil
}

func (s *dataPlaneVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return s.cooldownChecker
}
