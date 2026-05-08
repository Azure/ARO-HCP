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
	"slices"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/operation"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// nodePoolVersionSyncer is a nodePool syncer that synchronizes cluster information
// from CS and internal and helps selecting a valid desiredVersion within the user's
// desired
type nodePoolVersionSyncer struct {
	cooldownChecker                       controllerutils.CooldownChecker
	clusterManagementClusterContentLister listers.ManagementClusterContentLister
	resourcesDBClient                     database.ResourcesDBClient
	clusterServiceClient                  ocm.ClusterServiceClientSpec

	cincinnatiClientCache cincinnati.ClientCache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolVersionSyncer)(nil)

// NewNodePoolVersionController creates a new syncer that reads node pool versions
// from Cluster Service.
// TODO: improve this description
func NewNodePoolVersionController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterManagementClusterContentLister := informers.ManagementClusterContents()
	syncer := &nodePoolVersionSyncer{
		cooldownChecker:                       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterManagementClusterContentLister: clusterManagementClusterContentLister,
		resourcesDBClient:                     resourcesDBClient,
		clusterServiceClient:                  clusterServiceClient,
		cincinnatiClientCache:                 cincinnati.NewClientCache(),
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"NodePoolVersion",
		resourcesDBClient,
		informers,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// SyncOnce synchronizes node pool version information between Cluster Service
// and the ServiceProviderNodePool in Cosmos DB.
//
// The method performs two main operations:
//
// 1. Active Version Tracking:
//   - Reads the current running version from Cluster Service (csNodePool.Version)
//   - Stores it in ServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions
//   - Maintains up to 2 versions during upgrades (newest first, then previous)
//
// 2. Desired Version Validation and Storage:
//   - Reads the customer's desired version from HCPNodePool.Properties.Version.ID
//   - Validates it against upgrade constraints (see below)
//   - The desired version must satisfy:
//   - Not less than the highest active version (no downgrades)
//   - Not greater than the lowest control plane version (node pools cannot exceed CP)
//   - Not skip minor versions (only z-stream or +1 minor allowed)
//   - Have a valid upgrade path in Cincinnati from the current version
//   - If valid, stores it in ServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion
//
// If the desired version is already among the active versions, validation is skipped
// (the upgrade is already in progress or complete).
func (c *nodePoolVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Get node pool from Cosmos to get CS internal ID
	nodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)

	if database.IsNotFoundError(err) {
		return nil // nodepool doesn't exists
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cosmos: %w", err))
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// Get the ServiceProviderCluster for control plane version validation
	clusterResourceID := api.Must(api.ToClusterResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName))
	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, clusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// Get the cluster for Cincinnati client initialization
	cluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cosmos: %w", err))
	}
	if cluster.ServiceProviderProperties.ClusterServiceID == nil {
		// TODO this appears to only be used to look up a clusterservice cluster to get a UUID.  Once the billing changes merge,
		// we'll have UID to key by and won't need this.
		return nil
	}

	// Resolve the cluster UUID from the cached HostedCluster so we can build the Cincinnati client.
	clusterUUID, found, err := maestrohelpers.GetCachedHostedClusterUUIDForCluster(ctx, c.clusterManagementClusterContentLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return err
	}
	if !found {
		// will reappear once the informer relists; without the UUID we cannot build the Cincinnati client
		return nil
	}

	// Read node pool from Cluster Service
	csNodePool, err := c.clusterServiceClient.GetNodePool(ctx, nodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool from CS: %w", err))
	}

	// For now we get the CS desired version
	// In the future it should be good to use the node pool Status information from the node pool CR
	version, ok := csNodePool.GetVersion()
	if !ok {
		return utils.TrackError(fmt.Errorf("node pool version not found in Cluster Service response"))
	}

	actualVersion := semver.MustParse(version.RawID())

	oldActiveVersions := existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions
	existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions = prependActiveVersionIfChanged(oldActiveVersions, actualVersion)

	serviceProviderCosmosNodePoolClient := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	// check if actualVersion from node pool in clusterService is different that the active versions in serviceProviderNodePool
	// if it is different update the ActualVersion in the serviceProviderNodePool
	// TODO: This is a simple gathering of the node pool versions. We should implement this to get the correct information.
	// Possible ways to get this information
	//   - In CS
	// 	 	- nodepool.version: latest version applied. When an upgrade policy completes correctly, this is set to that version.
	//   	- upgradepolicy.targetVersion: if the policy has started this version is applying to the nodepool
	//   - In Hypershift
	//		- .Status.Version: shows the latest applied version https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/nodepool_types.go#L246-L251
	if !slices.Equal(oldActiveVersions, existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions) {
		logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", existingServiceProviderNodePool.Status.NodePoolVersion.ActiveVersions)
		existingServiceProviderNodePool, err = serviceProviderCosmosNodePoolClient.Replace(ctx, existingServiceProviderNodePool, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
		}
	}

	// If there is no nodePool version do not validate and update the desired Version
	if nodePool.Properties.Version.ID == "" {
		return nil
	}
	customerDesiredVersion := semver.MustParse(nodePool.Properties.Version.ID)

	// Short-circuit: skip validation if desired version hasn't changed
	if existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion != nil &&
		customerDesiredVersion.EQ(*existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return nil
	}

	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get subscription: %w", err))
	}

	// Validate the customer's desired version before setting it
	if err := c.validateDesiredNodePoolVersion(ctx, &customerDesiredVersion, existingServiceProviderNodePool, existingServiceProviderCluster, subscription, nodePool.Properties.Version.ChannelGroup, clusterUUID); err != nil {
		return utils.TrackError(fmt.Errorf("invalid desired version: %w", err))
	}

	// Update the serviceProviderNodePool DesiredVersion
	existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion = &customerDesiredVersion
	_, err = serviceProviderCosmosNodePoolClient.Replace(ctx, existingServiceProviderNodePool, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}

	logger.Info("Updated ServiceProviderNodePool with new desired version", "desiredVersion", customerDesiredVersion.String())

	return nil
}

func (c *nodePoolVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// prependActiveVersionIfChanged takes a slice of active versions and returns an updated slice
// with the new version prepended if it differs from the most recent version.
// If the most recent version matches the new version, returns the original slice unchanged.
// The returned slice is capped to the 2 most recent versions.
func prependActiveVersionIfChanged(currentVersions []api.HCPNodePoolActiveVersion, newVersion semver.Version) []api.HCPNodePoolActiveVersion {
	// Check if the tip (most recent version) is already the new version
	if len(currentVersions) > 0 && currentVersions[0].Version != nil && currentVersions[0].Version.EQ(newVersion) {
		return currentVersions
	}

	// Create new list with at most 2 versions: new version + most recent old version
	newVersions := []api.HCPNodePoolActiveVersion{{Version: &newVersion}}
	if len(currentVersions) > 0 {
		newVersions = append(newVersions, currentVersions[0])
	}
	return newVersions
}

// validateDesiredNodePoolVersion checks that the desired node pool version is a valid upgrade path.
// It validates:
//   - The desired version is not less than the highest active node pool version (no downgrades)
//   - The desired version is not greater than the lowest control plane version
//   - No major version changes (unless FeatureExperimentalReleaseFeatures is registered)
//   - No minor versions are skipped
//   - An upgrade path exists from the current version (all the activeVersions) to the desired version (via Cincinnati)
//
// Returns nil if the desired version is valid, or an error describing why it's invalid.
func (c *nodePoolVersionSyncer) validateDesiredNodePoolVersion(
	ctx context.Context,
	desiredVersion *semver.Version,
	spNodePool *api.ServiceProviderNodePool,
	spCluster *api.ServiceProviderCluster,
	subscription *arm.Subscription,
	channelGroup string,
	clusterUUID uuid.UUID,
) error {
	if desiredVersion == nil {
		return fmt.Errorf("customerDesiredVersion is nil, cannot evaluate upgrade")
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Validating desired nodepool version", "desiredVersion", desiredVersion.String(), "channelGroup", channelGroup)

	// Get all active versions from ServiceProviderNodePool
	nodePoolActiveVersions := spNodePool.Status.NodePoolVersion.ActiveVersions
	lowestCPVersion, _ := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	// This operation is added to normalize the use for the validations so they are shared
	// between the frontend and the backend
	op := operation.Operation{
		Options: validation.AFECsToValidationOptions(subscription.GetRegisteredFeatures()),
	}

	if err := validation.ValidateNodePoolUpgrade(*desiredVersion, nodePoolActiveVersions, lowestCPVersion, op.HasOption(api.FeatureExperimentalReleaseFeatures)); err != nil {
		return err
	}

	// Validate upgrade path exists in Cincinnati for ALL active node pool versions
	for _, activeVersion := range nodePoolActiveVersions {
		if activeVersion.Version != nil && !desiredVersion.EQ(*activeVersion.Version) {
			if err := c.validateUpgradePathAvailable(ctx, activeVersion.Version, desiredVersion, channelGroup, clusterUUID); err != nil {
				return fmt.Errorf("no valid upgrade path from active version %s: %w", activeVersion.Version.String(), err)
			}
		}
	}

	return nil
}

// validateUpgradePathAvailable checks that an upgrade path exists from current to desired version.
func (c *nodePoolVersionSyncer) validateUpgradePathAvailable(
	ctx context.Context,
	currentVersion, desiredVersion *semver.Version,
	channelGroup string,
	clusterUUID uuid.UUID,
) error {
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return fmt.Errorf("failed to get Cincinnati URI: %w", err)
	}

	targetMinorString := fmt.Sprintf("%d.%d", desiredVersion.Major, desiredVersion.Minor)
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinorString)

	cincinnatiClient := c.cincinnatiClientCache.GetOrCreateClient(clusterUUID)
	// Get updates for the current version
	// TODO: for nodePools we should use the arch of that nodePool
	_, candidates, _, err := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, *currentVersion)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to query Cincinnati for upgrade path: %w", err))
	}

	for _, candidate := range candidates {
		candidateVersion := semver.MustParse(candidate.Version)
		if candidateVersion.EQ(*desiredVersion) {
			return nil
		}
	}

	return utils.TrackError(fmt.Errorf("no upgrade path available from %s to %s in channel %s", currentVersion, desiredVersion, cincinnatiChannel))
}
