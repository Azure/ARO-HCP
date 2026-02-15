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

	"k8s.io/client-go/tools/cache"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneVersionSyncer is a Cluster syncer that manages control plane version upgrades.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	cincinnatiClientLock      sync.RWMutex
	clusterToCincinnatiClient *lru.Cache
}

var _ controllerutils.ClusterSyncer = (*controlPlaneVersionSyncer)(nil)

// NewControlPlaneVersionController creates a new controller that manages control plane versions.
// It periodically checks each cluster to track active versions and determine the desired version
// based on the OCPVersion logic documented in the ServiceProviderCluster type.
func NewControlPlaneVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	clusterInformer cache.SharedIndexInformer,
) controllerutils.Controller {

	syncer := &controlPlaneVersionSyncer{
		cooldownChecker:           controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:              cosmosClient,
		clusterToCincinnatiClient: lru.New(100000),
		clusterServiceClient:      clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ControlPlaneVersion",
		cosmosClient,
		clusterInformer,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

func (c *controlPlaneVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of the control plane upgrade for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Update the active versions array by querying version service to get current cluster version(s)
//  3. Compute the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  4. If the computed desired version differs from the previously stored desired version:
//     - Update the DesiredVersion field
//  5. Save the updated service provider cluster state
//
// Get the customer's desired cluster configuration
func (c *controlPlaneVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	// TODO bring the cluster uuid into serviceprovidercluster
	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	version, ok := clusterServiceCluster.GetVersion()
	if !ok {
		return utils.TrackError(fmt.Errorf("cluster version not found in Cluster Service response"))
	}

	actualVersion := semver.MustParse(version.RawID())

	oldActiveVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions = c.prependActiveVersionIfChanged(oldActiveVersions, actualVersion)

	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if !slices.Equal(oldActiveVersions, existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) {
		logger := utils.LoggerFromContext(ctx)
		logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)
		existingServiceProviderCluster, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	// Create Cincinnati client with cluster UUID from Cluster Service
	clusterUUID, err := uuid.Parse(clusterServiceCluster.ExternalID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid cluster UUID from Cluster Service: %w", err))
	}
	cincinnatiClient := c.getCincinnatiClient(key, clusterUUID)

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup
	activeVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, cincinnatiClient, customerDesiredMinor, channelGroup, activeVersions)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	// Check if there's a new desired version to set
	if desiredVersion == nil {
		return nil
	}

	// Check if it's the same as the previously set desired version
	previousDesiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if previousDesiredVersion != nil && desiredVersion.EQ(*previousDesiredVersion) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)

	existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion

	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// prependActiveVersionIfChanged takes a slice of active versions and returns an updated slice
// with the new version prepended if it differs from the most recent version.
// If the most recent version matches the new version, returns the original slice unchanged.
// The returned slice is capped to the 2 most recent versions.
func (c *controlPlaneVersionSyncer) prependActiveVersionIfChanged(currentVersions []api.HCPClusterActiveVersion, newVersion semver.Version) []api.HCPClusterActiveVersion {
	// Check if the tip (most recent version) is already the new version
	if len(currentVersions) > 0 && currentVersions[0].Version != nil && currentVersions[0].Version.EQ(newVersion) {
		return currentVersions
	}

	// Create new list with at most 2 versions: new version + most recent old version
	newVersions := []api.HCPClusterActiveVersion{{Version: &newVersion}}
	if len(currentVersions) > 0 {
		newVersions = append(newVersions, currentVersions[0])
	}
	return newVersions
}

// getCincinnatiClient provides a point for unit testing.
// Likely need to provide transport injection for integration testing.
func (c *controlPlaneVersionSyncer) getCincinnatiClient(key controllerutils.HCPClusterKey, clusterID uuid.UUID) cincinatti.Client {
	// Fast path: check cache with read lock
	c.cincinnatiClientLock.RLock()
	client, ok := c.clusterToCincinnatiClient.Get(key)
	c.cincinnatiClientLock.RUnlock()

	if ok {
		return client.(cincinatti.Client)
	}

	// Slow path: cache miss, need to create
	c.cincinnatiClientLock.Lock()
	defer c.cincinnatiClientLock.Unlock()

	// Double-check: another goroutine might have created it while we waited
	client, ok = c.clusterToCincinnatiClient.Get(key)
	if ok {
		return client.(cincinatti.Client)
	}

	// Create and cache
	client = cincinnati.NewClient(clusterID, http.DefaultTransport.(*http.Transport), "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	c.clusterToCincinnatiClient.Add(key, client)
	return client.(cincinatti.Client)
}

// desiredControlPlaneZVersion determines the desired z-stream version for the control plane.
//
// The desired version selection logic is executed on each controller sync.
// NOTE: Rollback to a previous z-stream is not currently supported (future enhancement).
//
// It dispatches to one of three resolution methods based on the current cluster state:
// - Case 1: Initial version selection (no active versions yet)
// - Case 2: Z-stream managed upgrade (customer desired minor == actual minor)
// - Case 3: Next Y-stream user-initiated upgrade (customer desired minor == actual minor + 1)
//
// Returns nil if no upgrade is needed.
func (c *controlPlaneVersionSyncer) desiredControlPlaneZVersion(
	ctx context.Context, cincinnatiClient cincinatti.Client,
	customerDesiredMinor string, channelGroup string,
	activeVersions []api.HCPClusterActiveVersion,
) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)

	if len(activeVersions) == 0 {
		logger.Info("Resolving initial desired version", "customerDesiredMinor", customerDesiredMinor, "channelGroup", channelGroup)

		// ParseTolerant handles both "4.19" and "4.19.0" formats
		customerDotZeroRelease := api.Must(semver.ParseTolerant(customerDesiredMinor))

		initialDesiredVersion, err := c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, customerDotZeroRelease, []semver.Version{customerDotZeroRelease})
		if err != nil {
			return nil, utils.TrackError(err)
		}

		// If no desired version found, fall back to customerDotZeroRelease
		// This happens when either:
		// - there is no latestVersion greater than customerDotZeroRelease
		// - or there is a latestVersion greater than customerDotZeroRelease but it doesn't have an upgrade path to the next minor
		// if the next minor existed
		// In both cases, customerDotZeroRelease is guaranteed to exist (since we didn't get a VersionNotFound error back when querying
		// for it from Cincinnati).  It is safe to use.
		if initialDesiredVersion == nil {
			return &customerDotZeroRelease, nil
		}

		return initialDesiredVersion, nil
	}

	// Extract active versions and determine actual minor (if any)
	// Use the most recent version to determine the minor version
	actualLatestVersion := activeVersions[0].Version

	actualMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

	// ParseTolerant handles both "4.19" and "4.19.0" formats (validated at API level, should never fail)
	desiredMinorVersion := api.Must(semver.ParseTolerant(customerDesiredMinor))

	if desiredMinorVersion.LT(actualMinorVersion) {
		return nil, utils.TrackError(fmt.Errorf(
			"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades",
			actualMinorVersion.String(), customerDesiredMinor,
		))
	}

	if desiredMinorVersion.GT(actualMinorVersion) {
		// TODO: Add support for major version upgrades (e.g., 4.20 → 5.0) when needed
		if desiredMinorVersion.Major != actualMinorVersion.Major {
			return nil, utils.TrackError(fmt.Errorf(
				"invalid next y-stream upgrade path from %s to %s: major version changes are not supported",
				actualMinorVersion.String(), customerDesiredMinor,
			))
		}
		if desiredMinorVersion.Minor != actualMinorVersion.Minor+1 {
			return nil, utils.TrackError(fmt.Errorf(
				"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no skipping minor versions",
				actualMinorVersion.String(), customerDesiredMinor,
			))
		}
	}

	targetMinorVersion := desiredMinorVersion

	activeVersionList := make([]semver.Version, 0, len(activeVersions))
	for _, av := range activeVersions {
		activeVersionList = append(activeVersionList, *av.Version)
	}

	if desiredMinorVersion.Minor == actualMinorVersion.Minor+1 {
		logger.Info("Resolving next Y-stream upgrade", "actualMinor", actualMinorVersion.String(), "activeVersions", activeVersions, "channelGroup", channelGroup,
			"targetMinor", customerDesiredMinor)

		latestVersion, err := c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, desiredMinorVersion, activeVersionList)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		if latestVersion != nil {
			return latestVersion, nil
		}

		// If no upgrade path to target minor, fall back to Z-stream upgrade in current minor
		// This helps the cluster reach a gateway version that may enable the Y-stream upgrade later
		targetMinorVersion = actualMinorVersion
	}

	return c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, targetMinorVersion, activeVersionList)
}

// findLatestVersionInMinor queries Cincinnati and finds the latest version within the specified target minor.
//
// This method implements the core version selection logic for all upgrade scenarios (both Y-stream and Z-stream).
// It prioritizes versions that have an upgrade path to the next minor version (gateway versions).
//
// Version selection algorithm:
//  1. Query Cincinnati for all available updates from EACH active version in the target minor channel
//  2. Filter candidates: only include versions within the target minor
//  3. Intersect candidate sets: only keep versions reachable from ALL active versions
//  4. Sort candidates by version (descending - latest first)
//  5. Check if next minor (4.(y+1)) channel exists in Cincinnati
//  6. If next minor doesn't exist: return the latest candidate
//  7. If next minor exists: iterate through candidates to find a gateway version to the next minor
//     - For each candidate, check if it has an upgrade path to the next minor
//     - If yes: return this version (latest gateway found)
//     - If no: continue checking older versions
//  8. If no gateway found: return nil
//
// Examples:
//   - Z-stream (4.19.15 → 4.19.z): Find latest 4.19.z with path to 4.20, or latest 4.19.z
//   - Y-stream (4.19.x → 4.20.z): Find latest 4.20.z with path to 4.21, or latest 4.20.z
//
// When multiple active versions are provided, this method ensures that the selected version
// is reachable from ALL active versions by intersecting the upgrade paths.
//
// Returns nil if no suitable version is found.
func (c *controlPlaneVersionSyncer) findLatestVersionInMinor(
	ctx context.Context,
	cincinnatiClient cincinatti.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	activeVersions []semver.Version,
) (*semver.Version, error) {
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err))
	}

	targetMinorString := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor)
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinorString)

	// For active versions, intersect their upgrade candidates
	candidatesByVersion := map[string]struct {
		version semver.Version
		count   int
	}{}

	for _, activeVersion := range activeVersions {
		_, candidateReleases, _, err := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, activeVersion)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		for _, candidate := range candidateReleases {
			candidateTargetVersion := semver.MustParse(candidate.Version)

			// Filter: only include versions in target minor
			if candidateTargetVersion.Major != targetMinorVersion.Major || candidateTargetVersion.Minor != targetMinorVersion.Minor {
				continue
			}

			candidateEntry := candidatesByVersion[candidateTargetVersion.String()]
			candidateEntry.version = candidateTargetVersion
			candidateEntry.count++
			candidatesByVersion[candidateTargetVersion.String()] = candidateEntry
		}
	}

	// Extract only candidates that appeared for ALL active versions (intersection)
	commonCandidates := []semver.Version{}
	for _, candidateEntry := range candidatesByVersion {
		if candidateEntry.count == len(activeVersions) {
			commonCandidates = append(commonCandidates, candidateEntry.version)
		}
	}

	// Use the most recent active version for additional validation logic
	return c.selectBestVersionFromCandidates(ctx, cincinnatiClient, channelGroup, targetMinorVersion, commonCandidates)
}

// selectBestVersionFromCandidates finds the best version to upgrade to from a list of candidate versions.
// It accepts a list of candidates (already filtered within the target minor) and prioritizes versions
// that are gateways to the next minor version.
//
// Algorithm:
//  1. Sort candidates by version (descending - latest first)
//  2. Check if the next minor channel exists in Cincinnati
//  3. If next minor doesn't exist: return the latest candidate
//  4. If next minor exists: iterate through candidates to find a gateway version to the next minor
//  5. If no gateway found: return nil
//
// Returns nil if no suitable version is found.
func (c *controlPlaneVersionSyncer) selectBestVersionFromCandidates(
	ctx context.Context,
	cincinnatiClient cincinatti.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	candidates []semver.Version,
) (*semver.Version, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort candidates by version (descending - latest first)
	slices.SortFunc(candidates, func(a, b semver.Version) int {
		return b.Compare(a)
	})

	// Check if next minor channel exists before checking for gateways
	nextMinor := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor+1)
	// Here we are sure that the latest candidate version is in the graph,
	// we just want to check if next minor exists
	// If we get VersionNotFound error, it means that the next minor doesn't exist
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err))
	}

	_, _, _, nextMinorExistsErr := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", fmt.Sprintf("%s-%s", channelGroup, nextMinor), candidates[0])

	if nextMinorExistsErr != nil && !cincinatti.IsCincinnatiVersionNotFoundError(nextMinorExistsErr) {
		return nil, utils.TrackError(nextMinorExistsErr)
	}

	nextMinorExists := nextMinorExistsErr == nil

	if !nextMinorExists {
		// If the next minor doesn't exist, return the latest version in the target minor
		return &candidates[0], nil
	}

	// otherwise return the candidate that is a gateway t next minor
	for _, candidate := range candidates {
		isGateway, err := isGatewayToNextMinor(ctx, candidate, cincinnatiClient, channelGroup, nextMinor)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		if isGateway {
			return &candidate, nil
		}
	}

	return nil, nil
}
