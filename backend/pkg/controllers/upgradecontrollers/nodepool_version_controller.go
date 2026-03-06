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
	"slices"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/golang/groupcache/lru"
	"github.com/google/uuid"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolVersionSyncer is a nodePool syncer that synchronizes cluster information
// from CS and internal and helps selecting a valid desiredVersion within the user's
// desired
type nodePoolVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	cincinnatiClientLock      sync.RWMutex
	clusterToCincinnatiClient *lru.Cache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolVersionSyncer)(nil)

// NewNodePoolVersionController creates a new syncer that reads node pool versions
// from Cluster Service.
// TODO: improve this description
func NewNodePoolVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &nodePoolVersionSyncer{
		cooldownChecker:           controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:              cosmosClient,
		clusterServiceClient:      clusterServiceClient,
		clusterToCincinnatiClient: lru.New(100000),
	}

	controller := controllerutils.NewNodePoolWatchingController(
		"NodePoolVersion",
		cosmosClient,
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
	// Get node pool from Cosmos to get CS internal ID
	nodePool, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)

	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // nodepool doesn't exists
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cosmos: %w", err))
	}

	existingServiceProviderNodePool, err := controllerutils.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	// Get the ServiceProviderCluster for control plane version validation
	clusterResourceID := api.Must(api.ToClusterResourceID(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName))
	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, clusterResourceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// Get the cluster for Cincinnati client initialization
	cluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cosmos: %w", err))
	}

	// Get the cluster from Cluster Service to obtain the cluster UUID for Cincinnati
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	clusterUUID, err := uuid.Parse(csCluster.ExternalID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid cluster UUID from Cluster Service: %w", err))
	}

	clusterKey := controllerutils.HCPClusterKey{
		SubscriptionID:    key.SubscriptionID,
		ResourceGroupName: key.ResourceGroupName,
		HCPClusterName:    key.HCPClusterName,
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

	serviceProviderCosmosNodePoolClient := c.cosmosClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
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
		logger := utils.LoggerFromContext(ctx)
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

	// Validate the customer's desired version before setting it
	if err := c.validateDesiredNodePoolVersion(ctx, &customerDesiredVersion, existingServiceProviderNodePool, existingServiceProviderCluster, nodePool.Properties.Version.ChannelGroup, clusterKey, clusterUUID); err != nil {
		return utils.TrackError(fmt.Errorf("invalid desired version: %w", err))
	}

	// Update the serviceProviderNodePool DesiredVersion
	existingServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion = &customerDesiredVersion
	_, err = serviceProviderCosmosNodePoolClient.Replace(ctx, existingServiceProviderNodePool, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}

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
//   - No minor versions are skipped
//   - An upgrade path exists from the current version (all the activeVersions) to the desired version (via Cincinnati)
//
// Returns nil if the desired version is valid, or an error describing why it's invalid.
func (c *nodePoolVersionSyncer) validateDesiredNodePoolVersion(
	ctx context.Context,
	desiredVersion *semver.Version,
	spNodePool *api.ServiceProviderNodePool,
	spCluster *api.ServiceProviderCluster,
	channelGroup string,
	clusterKey controllerutils.HCPClusterKey,
	clusterUUID uuid.UUID,
) error {
	if desiredVersion == nil {
		return fmt.Errorf("customerDesiredVersion is nil, cannot evaluate upgrade")
	}

	// Get all active versions from ServiceProviderNodePool
	nodePoolActiveVersions := spNodePool.Status.NodePoolVersion.ActiveVersions

	// If desired version is already among active versions, validation passes (upgrade already in progress or complete)
	if isVersionInActiveVersions(desiredVersion, nodePoolActiveVersions) {
		return nil
	}

	// Get the lowest and highest node pool active versions
	lowestNodePoolVersion := findLowestNodePoolVersion(nodePoolActiveVersions)
	highestNodePoolVersion := findHighestNodePoolVersion(nodePoolActiveVersions)

	// Get the lowest control plane version (most restrictive upper bound)
	lowestControlPlaneVersion := findLowestControlPlaneVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	// Node pool version >= highest active version (no partial downgrades)
	if highestNodePoolVersion != nil && desiredVersion.LT(*highestNodePoolVersion) {
		return fmt.Errorf(
			"invalid node pool version %s: cannot downgrade from current version %s",
			desiredVersion.String(), highestNodePoolVersion.String(),
		)
	}

	// Node pool version <= control plane version (check against lowest CP version)
	// TODO: We may relax this constraint in the future
	if lowestControlPlaneVersion != nil && desiredVersion.GT(*lowestControlPlaneVersion) {
		return fmt.Errorf(
			"invalid node pool version %s: cannot exceed control plane version %s",
			desiredVersion.String(), lowestControlPlaneVersion.String(),
		)
	}

	if lowestNodePoolVersion != nil {
		// TODO: Add support for major version upgrades (e.g., 4.20 → 5.0) when needed
		// No major version upgrades implemented
		if desiredVersion.Major != lowestNodePoolVersion.Major {
			return fmt.Errorf(
				"invalid upgrade path from %s to %s: major version changes are not supported",
				lowestNodePoolVersion.String(), desiredVersion.String(),
			)
		}
		// Allow same minor (z-stream upgrade) or next minor (y-stream upgrade)
		// Cannot skip minor versions (check against lowest node pool version)
		// TODO: We will relax this constraint in the future to allow skipping minor versions
		if desiredVersion.Minor > lowestNodePoolVersion.Minor+1 {
			return fmt.Errorf(
				"invalid upgrade path from %s to %s: skipping minor versions is not allowed",
				lowestNodePoolVersion.String(), desiredVersion.String(),
			)
		}
	}

	// Validate upgrade path exists in Cincinnati for ALL active node pool versions
	for _, activeVersion := range nodePoolActiveVersions {
		if activeVersion.Version != nil && !desiredVersion.EQ(*activeVersion.Version) {
			if err := c.validateUpgradePathAvailable(ctx, activeVersion.Version, desiredVersion, channelGroup, clusterKey, clusterUUID); err != nil {
				return fmt.Errorf("no valid upgrade path from active version %s: %w", activeVersion.Version.String(), err)
			}
		}
	}

	return nil
}

// isVersionInActiveVersions checks if the given version is already in the list of active versions.
func isVersionInActiveVersions(version *semver.Version, activeVersions []api.HCPNodePoolActiveVersion) bool {
	if version == nil {
		return false
	}
	for _, av := range activeVersions {
		if av.Version != nil && av.Version.EQ(*version) {
			return true
		}
	}
	return false
}

// findLowestNodePoolVersion returns the lowest (oldest) version from the node pool active versions.
// ActiveVersions is ordered with the most recent version first, so the lowest is the last element.
// Returns nil if no versions are present.
func findLowestNodePoolVersion(activeVersions []api.HCPNodePoolActiveVersion) *semver.Version {
	if len(activeVersions) == 0 {
		return nil
	}
	return activeVersions[len(activeVersions)-1].Version
}

// findHighestNodePoolVersion returns the highest (most recent) version from the node pool active versions.
// This represents the current upgrade target. Desired version must be >= this to prevent partial downgrades.
// ActiveVersions is ordered with the most recent version first, so the highest is the first element.
// Returns nil if no versions are present.
func findHighestNodePoolVersion(activeVersions []api.HCPNodePoolActiveVersion) *semver.Version {
	if len(activeVersions) == 0 {
		return nil
	}
	return activeVersions[0].Version
}

// findLowestControlPlaneVersion returns the lowest version from the list of control plane active versions.
func findLowestControlPlaneVersion(activeVersions []api.HCPClusterActiveVersion) *semver.Version {
	if len(activeVersions) == 0 {
		return nil
	}
	return activeVersions[len(activeVersions)-1].Version
}

// validateUpgradePathAvailable checks that an upgrade path exists from current to desired version.
func (c *nodePoolVersionSyncer) validateUpgradePathAvailable(
	ctx context.Context,
	currentVersion, desiredVersion *semver.Version,
	channelGroup string,
	clusterKey controllerutils.HCPClusterKey,
	clusterUUID uuid.UUID,
) error {
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return fmt.Errorf("failed to get Cincinnati URI: %w", err)
	}

	targetMinorString := fmt.Sprintf("%d.%d", desiredVersion.Major, desiredVersion.Minor)
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinorString)

	cincinnatiClient := c.getCincinnatiClient(clusterKey, clusterUUID)
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

// getCincinnatiClient returns a Cincinnati client for the given cluster, using a cache.
// The clusterUUID is the external ID of the parent cluster from Cluster Service.
func (c *nodePoolVersionSyncer) getCincinnatiClient(key controllerutils.HCPClusterKey, clusterUUID uuid.UUID) cincinatti.Client {
	// Fast path: check cache with read lock
	c.cincinnatiClientLock.RLock()
	client, ok := c.clusterToCincinnatiClient.Get(key)
	c.cincinnatiClientLock.RUnlock()

	if ok {
		return client.(cincinatti.Client)
	}

	c.cincinnatiClientLock.Lock()
	defer c.cincinnatiClientLock.Unlock()

	// Double-check after acquiring write lock
	client, ok = c.clusterToCincinnatiClient.Get(key)
	if ok {
		return client.(cincinatti.Client)
	}

	// Create client using the parent cluster's UUID
	client = cincinnati.NewClient(clusterUUID, http.DefaultTransport.(*http.Transport), "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	c.clusterToCincinnatiClient.Add(key, client)
	return client.(cincinatti.Client)
}
