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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
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

	// Get latest actual version from active versions
	var actualLatestVersion *semver.Version
	if len(existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) > 0 {
		actualLatestVersion = existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions[0].Version
	}

	// If desired version matches latest actual version, nothing to do
	if actualLatestVersion != nil && desiredVersion.EQ(*actualLatestVersion) {
		return nil
	}

	return c.createUpgradePolicyIfNeeded(ctx, desiredVersion, existingCluster.ServiceProviderProperties.ClusterServiceID)
}

// createUpgradePolicyIfNeeded ensures a control plane upgrade policy exists for the desired version.
// It creates a new policy only if the most recently created policy does not match the desired version.
//
// The method:
//  1. Queries existing upgrade policies from Cluster Service (sorted by creation_timestamp desc)
//  2. Checks if the latest policy matches the desired version - returns nil if it does
//  3. Otherwise, creates a new upgrade policy with the desired version
func (c *triggerControlPlaneUpgradeSyncer) createUpgradePolicyIfNeeded(ctx context.Context, desiredVersion *semver.Version, clusterServiceID api.InternalID) error {
	logger := utils.LoggerFromContext(ctx)

	// Query existing control plane upgrade policies from Cluster Service
	iterator := c.clusterServiceClient.ListControlPlaneUpgradePolicies(clusterServiceID, "creation_timestamp desc")

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
		return utils.TrackError(fmt.Errorf("failed to list control plane upgrade policies: %w", err))
	}

	// Create a new control plane upgrade policy for the desired version
	logger.Info("Creating control plane upgrade policy", "desiredVersion", desiredVersion)

	_, policyErr := c.clusterServiceClient.PostControlPlaneUpgradePolicy(ctx, clusterServiceID, arohcpv1alpha1.NewControlPlaneUpgradePolicy().Version(desiredVersion.String()))
	if policyErr != nil {
		return utils.TrackError(fmt.Errorf("failed to create control plane upgrade policy: %w", policyErr))
	}

	logger.Info("Successfully created control plane upgrade policy", "desiredVersion", desiredVersion)

	return nil
}
