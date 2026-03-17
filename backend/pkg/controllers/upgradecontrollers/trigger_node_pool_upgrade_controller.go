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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// triggerNodePoolUpgradeSyncer is a NodePool syncer that triggers node pool upgrades
type triggerNodePoolUpgradeSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.NodePoolSyncer = (*triggerNodePoolUpgradeSyncer)(nil)

// NewTriggerNodePoolUpgradeController creates a new controller that triggers node pool upgrades.
// It monitors node pools where the desired version differs from the actual version and creates
// a NodePoolUpgradePolicy in Cluster Service to initiate the upgrade.
func NewTriggerNodePoolUpgradeController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &triggerNodePoolUpgradeSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"TriggerNodePoolUpgrade",
		cosmosClient,
		informers,
		5*time.Minute,
		syncer,
	)

	return controller
}

func (c *triggerNodePoolUpgradeSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation to trigger a node pool upgrade if needed.
//
// High-level flow:
//  1. Fetch the node pool and service provider node pool state
//  2. Check if desiredVersion differs from latest actual version
//  3. If different, create a NodePoolUpgradePolicy to trigger upgrade
func (c *triggerNodePoolUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingNodePool, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // node pool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}

	existingServiceProviderNodePool, err := controllerutils.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	desiredVersion := existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
	if desiredVersion == nil {
		return nil // No desired version set
	}

	// Get latest actual version from active versions
	var actualLatestVersion *semver.Version
	if len(existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions) > 0 {
		actualLatestVersion = existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions[0].Version
	}

	// If desired version matches latest actual version, nothing to do
	if actualLatestVersion != nil && desiredVersion.EQ(*actualLatestVersion) {
		return nil
	}

	return c.createUpgradePolicyIfNeeded(ctx, desiredVersion, existingNodePool.ServiceProviderProperties.ClusterServiceID)
}

// createUpgradePolicyIfNeeded ensures a node pool upgrade policy exists for the desired version.
// It creates a new policy only if the most recently created policy does not match the desired version.
//
// The method:
//  1. Queries existing upgrade policies from Cluster Service (sorted by creation_timestamp desc)
//  2. Checks if the latest policy matches the desired version - returns nil if it does
//  3. Otherwise, creates a new upgrade policy with the desired version
func (c *triggerNodePoolUpgradeSyncer) createUpgradePolicyIfNeeded(ctx context.Context, desiredVersion *semver.Version, nodePoolServiceID api.InternalID) error {
	logger := utils.LoggerFromContext(ctx)

	// Query existing node pool upgrade policies from Cluster Service
	iterator := c.clusterServiceClient.ListNodePoolUpgradePolicies(nodePoolServiceID, "creation_timestamp desc")

	// Only create a new upgrade policy if the latest created policy doesn't match the desired version
	for policy := range iterator.Items(ctx) {
		// Only check the first (latest) policy
		if latestPolicyVersion, ok := policy.GetVersion(); ok {
			if latestPolicyVersion == desiredVersion.String() {
				return nil
			}
		}
		break // Only need to check the first policy
	}

	if err := iterator.GetError(); err != nil {
		return utils.TrackError(fmt.Errorf("failed to list node pool upgrade policies: %w", err))
	}

	// Create a new node pool upgrade policy for the desired version
	logger.Info("Creating node pool upgrade policy", "desiredVersion", desiredVersion)

	_, policyErr := c.clusterServiceClient.PostNodePoolUpgradePolicy(ctx, nodePoolServiceID, arohcpv1alpha1.NewNodePoolUpgradePolicy().Version(desiredVersion.String()))
	if policyErr != nil {
		return utils.TrackError(fmt.Errorf("failed to create node pool upgrade policy: %w", policyErr))
	}

	logger.Info("Successfully created node pool upgrade policy", "desiredVersion", desiredVersion)

	return nil
}
