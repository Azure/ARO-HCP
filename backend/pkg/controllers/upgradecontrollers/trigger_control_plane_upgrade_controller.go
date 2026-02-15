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
	"net/http"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/client-go/tools/cache"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// triggerControlPlaneUpgradeSyncer is a Cluster syncer that triggers control plane upgrades
type triggerControlPlaneUpgradeSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*triggerControlPlaneUpgradeSyncer)(nil)

// NewTriggerControlPlaneUpgradeController creates a new controller that triggers control plane upgrades.
// It monitors clusters where the desired version differs from the actual version and calls
// the version service API to initiate the upgrade.
//
// The version service API is idempotent:
//   - If desiredVersion == current cluster version: NOOP
//   - Otherwise: Initiate the upgrade to desiredVersion
func NewTriggerControlPlaneUpgradeController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	clusterInformer cache.SharedIndexInformer,
) controllerutils.Controller {
	syncer := &triggerControlPlaneUpgradeSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"TriggerControlPlaneUpgrade",
		cosmosClient,
		clusterInformer,
		5*time.Minute,
		syncer,
	)

	return controller
}

func (c *triggerControlPlaneUpgradeSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation to trigger a control plane upgrade if needed.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Check if desiredVersion differs from latest actual version
//  3. If different, call version service API to trigger upgrade
//  4. The version service API is idempotent and handles the actual upgrade orchestration
func (c *triggerControlPlaneUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	desiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if desiredVersion == nil {
		return nil // No desired version set
	}

	// Get actual version from active versions
	var actualLatestVersion *semver.Version
	if len(existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) > 0 {
		actualLatestVersion = existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions[0].Version
	}

	// If desired version matches actual version, nothing to do
	if actualLatestVersion != nil && desiredVersion.EQ(*actualLatestVersion) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)

	// TODO: Make API call to version service to trigger the upgrade
	// The version service API is idempotent:
	// - If desiredVersion == current cluster version: NOOP
	// - Otherwise: Initiate the upgrade to desiredVersion

	// For now, just log that we would trigger an upgrade
	logger.Info("Would trigger control plane upgrade",
		"cluster", key.HCPClusterName,
		"desiredVersion", desiredVersion,
		"actualVersion", actualLatestVersion,
		"clusterServiceID", existingCluster.ServiceProviderProperties.ClusterServiceID,
	)

	return nil
}
