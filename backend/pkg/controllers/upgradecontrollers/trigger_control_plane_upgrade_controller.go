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

	"github.com/blang/semver/v4"

	utilsclock "k8s.io/utils/clock"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// triggerControlPlaneUpgradeSyncer is a Cluster syncer that triggers control plane upgrades
type triggerControlPlaneUpgradeSyncer struct {
	clock                 utilsclock.PassiveClock
	resourcesDBClient     database.ResourcesDBClient
	clusterServiceClient  ocm.ClusterServiceClientSpec
	activeOperationLister listers.ActiveOperationLister
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
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {
	syncer := &triggerControlPlaneUpgradeSyncer{
		clock:                 clock,
		resourcesDBClient:     resourcesDBClient,
		clusterServiceClient:  clusterServiceClient,
		activeOperationLister: activeOperationLister,
	}

	controller := controllerutils.NewClusterWatchingController(
		"TriggerControlPlaneUpgrade",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)

	return controller
}

// SyncOnce performs a single reconciliation to trigger a control plane upgrade if needed.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Check if desiredVersion differs from latest actual version
//  3. If different, call version service API to trigger upgrade
//  4. The version service API is idempotent and handles the actual upgrade orchestration
func (c *triggerControlPlaneUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) (controllerutil.SyncResult, error) {
	logger := utils.LoggerFromContext(ctx)
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return controllerutil.SyncResult{}, nil
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// if we have no clusterService cluster, we have nothing to trigger.
		return controllerutil.SyncResult{}, nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// here we check to see if we should be triggering an upgrade. We do this by
	// 1. if the cluster was created more than two hours ago, then we can run
	// 2. if there is no active operation that is a create, then we can run
	shouldRun, err := c.shouldTriggerUpgrade(ctx, existingCluster)
	if err != nil {
		logger.Error(err, "error determining if control plane upgrade should be triggered")
	} else if !shouldRun {
		logger.Info("Skipping control plane upgrade trigger", "cluster", existingCluster.Name)
		return controllerutil.SyncResult{}, nil
	}

	desiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if desiredVersion == nil {
		return controllerutil.SyncResult{}, nil // No desired version set
	}

	// No active version yet (installation ongoing); skip upgrade trigger.
	if len(existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) == 0 {
		return controllerutil.SyncResult{}, nil
	}

	// Get latest actual version from active versions
	actualLatestVersion := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions[0].Version

	// If desired version matches latest actual version, nothing to do
	if desiredVersion.EQ(*actualLatestVersion) {
		return controllerutil.SyncResult{}, nil
	}

	return controllerutil.SyncResult{}, c.createUpgradePolicyIfNeeded(ctx, desiredVersion, *existingCluster.ServiceProviderProperties.ClusterServiceID)
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

// shouldTriggerUpgrade decides whether the syncer should trigger a control
// plane upgrade on this pass. It returns true when ANY of:
//
//  1. The cluster's ARM CreatedAt is older than clusterCreateGracePeriod —
//     past that window the create flow is expected to be done and triggering
//     an upgrade cannot race the initial creation.
//  2. There is no active Create operation for the cluster itself — without a
//     create in flight there is nothing to race with, so we can run.
//
// Otherwise (cluster still young, Create in flight) we skip so a freshly
// created cluster doesn't have a control plane upgrade policy posted while
// creation is still in progress.
func (c *triggerControlPlaneUpgradeSyncer) shouldTriggerUpgrade(ctx context.Context, cluster *api.HCPOpenShiftCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)
	if c.clusterOlderThanGracePeriod(cluster) {
		logger.Info("Cluster is older than grace period, skipping upgrade trigger", "cluster", cluster.Name)
		return true, nil
	}
	hasCreate, err := c.clusterHasActiveCreateOperation(ctx, cluster)
	if err != nil {
		logger.Error(err, "Failed to check if cluster has active create operation, checking upgrade trigger", "cluster", cluster.Name)
		return true, err
	}
	if hasCreate {
		logger.Info("Cluster has active create operation", "cluster", cluster.Name, "hasCreate", hasCreate)
		return false, nil
	}
	return true, nil
}

// clusterOlderThanGracePeriod returns true when the cluster's ARM CreatedAt
// is more than clusterCreateGracePeriod in the past. A missing CreatedAt is
// treated as "old enough" so a malformed document does not pin the controller
// in skip-forever mode.
func (c *triggerControlPlaneUpgradeSyncer) clusterOlderThanGracePeriod(cluster *api.HCPOpenShiftCluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return c.clock.Since(*cluster.SystemData.CreatedAt) > clusterCreateGracePeriod
}

// clusterHasActiveCreateOperation reports whether there is a non-terminal
// Create operation whose ExternalID is the cluster itself. Operations on
// child resources (node pools, external auths) under the cluster are
// ignored on purpose: they don't gate control-plane upgrade triggering.
func (c *triggerControlPlaneUpgradeSyncer) clusterHasActiveCreateOperation(ctx context.Context, cluster *api.HCPOpenShiftCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)
	if len(cluster.ServiceProviderProperties.ActiveOperationID) == 0 {
		logger.Info("Cluster has no active create operation", "cluster", cluster.Name)
		return false, nil
	}
	operation, err := c.activeOperationLister.Get(ctx, cluster.ResourceID.SubscriptionID, cluster.ServiceProviderProperties.ActiveOperationID)
	if err != nil {
		return false, fmt.Errorf("failed to get operations %q for cluster: %w", cluster.ServiceProviderProperties.ActiveOperationID, err)
	}
	if operation.Request != database.OperationRequestCreate {
		logger.Info("Cluster has active create operation but it is not a create operation", "cluster", cluster.Name, "operation", operation.Request)
		return false, nil
	}
	if operation.Status.IsTerminal() {
		logger.Info("Cluster has active create operation but it is terminal", "cluster", cluster.Name, "operation", operation.Request)
		return false, nil
	}
	logger.Info("Cluster has active create operation", "cluster", cluster.Name, "operation", operation.Request)
	return true, nil
}
