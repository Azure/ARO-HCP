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
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/golang/groupcache/lru"
	"github.com/google/uuid"

	utilsclock "k8s.io/utils/clock"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneUpgradeSyncer is a Cluster syncer that manages control plane version upgrades.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneUpgradeSyncer struct {
	clock                utilsclock.PassiveClock
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	cincinnatiClientLock      sync.RWMutex
	clusterToCincinnatiClient *lru.Cache
}

var _ controllerutils.ClusterSyncer = (*controlPlaneUpgradeSyncer)(nil)

// NewControlPlaneUpgradeController creates a new controller that manages control plane upgrades.
// It periodically checks each cluster and determines if an upgrade is needed based on the
// OCPVersion logic documented in the ServiceProviderCluster type.
func NewControlPlaneUpgradeController(
	cosmosClient database.DBClient,
	ocmConnection *ocmsdk.Connection,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {

	clusterServiceClient := ocm.NewClusterServiceClientWithTracing(
		ocm.NewClusterServiceClient(
			ocmConnection,
			"",
			false,
			false,
		),
		"github.com/Azure/ARO-HCP/backend",
	)

	syncer := &controlPlaneUpgradeSyncer{
		clock:                     utilsclock.RealClock{},
		cosmosClient:              cosmosClient,
		clusterToCincinnatiClient: lru.New(100000),
		clusterServiceClient:      clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ControlPlaneUpgrade",
		cosmosClient,
		subscriptionLister,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// SyncOnce performs a single reconciliation of the control plane upgrade for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Initialize HCPClusterVersion if needed
//  3. Update the active versions array by querying version service to get current cluster version(s)
//  4. Compute the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  5. If the computed desired version differs from the previously stored desired version:
//     - Update the DesiredVersion field
//     - Trigger the upgrade
//  6. Save the updated service provider cluster state
func (c *controlPlaneUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error { // Get the customer's desired cluster configuration
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

	if existingServiceProviderCluster.Version == nil {
		existingServiceProviderCluster.Version = &api.HCPClusterVersion{}
	}

	// TODO bring the cluster uuid into serviceprovidercluster
	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	version, ok := csCluster.GetVersion()
	if !ok {
		return utils.TrackError(fmt.Errorf("cluster version not found in Cluster Service response"))
	}

	actualVersion := version.RawID()

	currentActiveVersions := existingServiceProviderCluster.Version.ActiveVersions
	existingServiceProviderCluster.Version.ActiveVersions = c.prependActiveVersionIfChanged(
		ctx,
		currentActiveVersions,
		actualVersion,
	)

	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if len(currentActiveVersions) == 0 || currentActiveVersions[0].Version != actualVersion {
		existingServiceProviderCluster, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	// Create Cincinnati client with cluster UUID from Cluster Service
	clusterUUID, err := uuid.Parse(csCluster.ExternalID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid cluster UUID from Cluster Service: %w", err))
	}
	cincinnatiClient := c.getCincinnatiClient(key, clusterUUID)

	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, cincinnatiClient, existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	previousDesiredVersion := existingServiceProviderCluster.Version.DesiredVersion
	if len(desiredVersion) == 0 || desiredVersion == previousDesiredVersion {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Selected desired version",
		"desiredVersion", desiredVersion,
		"previousDesiredVersion", existingServiceProviderCluster.Version.DesiredVersion,
	)

	existingServiceProviderCluster.Version.DesiredVersion = desiredVersion

	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// prependActiveVersionIfChanged takes a slice of active versions and returns an updated slice
// with the new version prepended if it differs from the most recent version.
// If the most recent version matches the new version, returns the original slice unchanged.
func (c *controlPlaneUpgradeSyncer) prependActiveVersionIfChanged(ctx context.Context, currentVersions []api.HCPClusterActiveVersion, newVersion string) []api.HCPClusterActiveVersion {
	// Check if the tip (most recent version) is already the new version
	if len(currentVersions) > 0 && currentVersions[0].Version == newVersion {
		return currentVersions
	}

	logger := utils.LoggerFromContext(ctx)
	var previousVersion string
	if len(currentVersions) > 0 {
		previousVersion = currentVersions[0].Version
	}
	logger.Info("Detected cluster version change",
		"previousVersion", previousVersion,
		"newVersion", newVersion,
	)

	// Create and prepend the new active version entry at the tip
	return append(
		[]api.HCPClusterActiveVersion{{
			Version:            newVersion,
			LastTransitionTime: c.clock.Now().UTC().Format(time.RFC3339),
		}},
		currentVersions...,
	)
}

// getCincinnatiClient provides a point for unit testing.  Likely need to provide transport injection for integration testing.
func (c *controlPlaneUpgradeSyncer) getCincinnatiClient(key controllerutils.HCPClusterKey, clusterID uuid.UUID) cincinatti.Client {
	c.cincinnatiClientLock.RLock()
	defer c.cincinnatiClientLock.RUnlock()

	ret, ok := c.clusterToCincinnatiClient.Get(key)
	if ok {
		return ret.(cincinatti.Client)
	}

	ret = cincinnati.NewClient(clusterID, http.DefaultTransport.(*http.Transport), "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	c.clusterToCincinnatiClient.Add(key, ret)
	return ret.(cincinatti.Client)
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
func (c *controlPlaneUpgradeSyncer) desiredControlPlaneZVersion(ctx context.Context, cincinnatiClient cincinatti.Client, customerDesired *api.HCPOpenShiftCluster, serviceProviderCluster *api.ServiceProviderCluster) (string, error) {
	customerDesiredMinor := customerDesired.CustomerProperties.Version.ID
	channelGroup := customerDesired.CustomerProperties.Version.ChannelGroup

	// Get the most recent active version from the service provider cluster
	var actualLatestVersionStr string
	if len(serviceProviderCluster.Version.ActiveVersions) > 0 {
		actualLatestVersionStr = serviceProviderCluster.Version.ActiveVersions[0].Version
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Retrieved cluster version state",
		"actualLatestVersion", actualLatestVersionStr,
		"customerDesiredMinor", customerDesiredMinor,
		"channelGroup", channelGroup,
	)

	if len(actualLatestVersionStr) == 0 {
		logger.Info("Resolving initial desired version for new cluster",
			"customerDesiredMinor", customerDesiredMinor,
			"channelGroup", channelGroup,
		)

		seedVersion, err := semver.ParseTolerant(customerDesiredMinor)
		if err != nil {
			return "", err
		}
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, customerDesiredMinor, seedVersion)
	}

	actualLatestVersion, err := semver.Parse(actualLatestVersionStr)
	if err != nil {
		return "", fmt.Errorf("invalid actual latest version %s: %w", actualLatestVersionStr, err)
	}

	actualMinor := fmt.Sprintf("%d.%d", actualLatestVersion.Major, actualLatestVersion.Minor)

	if actualMinor == customerDesiredMinor { // we need to do a z-stream upgrade (actual minor == desired minor)
		logger.Info("Resolving next z-stream version for managed upgrade",
			"actualLatestVersion", actualLatestVersionStr,
			"customerDesiredMinor", customerDesiredMinor,
			"channelGroup", channelGroup,
		)
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, customerDesiredMinor, actualLatestVersion)
	}

	// If we reach here it is because we need to do a next y-stream upgrade (actual minor != desired minor)
	// Validate that desired minor is exactly one minor ahead (actualMinor + 1)
	// We don't allow downgrades or skipping minor versions
	if !isValidNextYStreamUpgradePath(actualMinor, customerDesiredMinor) {
		return "", fmt.Errorf("invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades or skipping minor versions", actualMinor, customerDesiredMinor)
	}

	return c.resolveNextYStream(ctx, cincinnatiClient, channelGroup, customerDesiredMinor, actualLatestVersion)
}

// resolveNextYStream handles user-initiated Y-stream upgrades to the next minor version.
//
// This method attempts to find the latest version in the target minor. If no direct upgrade
// path exists, it falls back to a Z-stream upgrade in the current minor to help the cluster
// reach a gateway version.
//
// Examples:
//   - Direct upgrade: 4.19.22 → 4.20.15 (direct path available)
//   - Fallback: 4.19.15 → 4.19.22 (no direct path to 4.20, upgrade to gateway first)
func (c *controlPlaneUpgradeSyncer) resolveNextYStream(ctx context.Context, cincinnatiClient cincinatti.Client, channelGroup string, targetMinor string, actualLatestVersion semver.Version) (string, error) {
	actualMinor := fmt.Sprintf("%d.%d", actualLatestVersion.Major, actualLatestVersion.Minor)
	actualLatestVersionStr := actualLatestVersion.String()

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Resolving next Y-stream upgrade",
		"actualMinor", actualMinor,
		"actualLatestVersion", actualLatestVersionStr,
		"channelGroup", channelGroup,
		"targetMinor", targetMinor,
	)

	// Try to find latest version in target minor (Y-stream upgrade)
	latestVersion, err := c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, targetMinor, actualLatestVersion)
	if err != nil {
		return "", err
	}

	// If no upgrade path to target minor, fall back to Z-stream upgrade in current minor
	// This helps the cluster reach a gateway version that may enable the Y-stream upgrade later
	if len(latestVersion) == 0 {
		logger.Info("No upgrade path to target minor, falling back to Z-stream upgrade in actual minor",
			"actualMinor", actualMinor,
			"targetMinor", targetMinor,
		)
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, actualMinor, actualLatestVersion)
	}

	logger.Info("Successfully found Y-stream upgrade path",
		"selectedVersion", latestVersion,
		"targetMinor", targetMinor,
	)
	return latestVersion, nil
}

// findLatestVersionInMinor queries Cincinnati and finds the latest version within the specified target minor.
//
// This method implements the core version selection logic for all upgrade scenarios (both Y-stream and Z-stream).
// It prioritizes versions that have an upgrade path to the next minor version (gateway versions).
//
// Version selection algorithm:
//  1. Query Cincinnati for all available updates from actualLatestVersion in the target minor channel
//  2. Sort candidates by version (descending - latest first)
//  3. Iterate through candidates in the target minor:
//     - For each candidate, check if it has an upgrade path to 4.(y+1) (next minor)
//     - If yes: return this version (latest gateway found)
//     - If no: continue checking older versions
//  4. If no gateway found: return the latest version in target minor
//
// Examples:
//   - Z-stream (4.19.15 → 4.19.z): Find latest 4.19.z with path to 4.20, or latest 4.19.z
//   - Y-stream (4.19.x → 4.20.z): Find latest 4.20.z with path to 4.21, or latest 4.20.z
//
// The controller always tries to select the latest z-stream that maintains an upgrade
// path to the next minor version (gateway), falling back to the latest available version.
func (c *controlPlaneUpgradeSyncer) findLatestVersionInMinor(ctx context.Context, cincinnatiClient cincinatti.Client, channelGroup string, targetMinor string, actualLatestVersion semver.Version) (string, error) {
	logger := utils.LoggerFromContext(ctx)

	logger.Info("Querying Cincinnati for latest version in minor",
		"channelGroup", channelGroup,
		"targetMinor", targetMinor,
		"actualLatestVersion", actualLatestVersion.String(),
	)
	cincinnatiURI, err := cincinatti.GetCincinnatiURI(channelGroup)
	if err != nil {
		return "", fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err)
	}

	// Query Cincinnati for available updates
	// ARO-HCP uses Multi architecture for all clusters
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinor)
	_, candidateReleases, _, err := cincinnatiClient.GetUpdates(
		ctx,
		cincinnatiURI,
		"multi",
		"multi",
		cincinnatiChannel,
		actualLatestVersion,
	)
	if err != nil {
		if cincinatti.IsCincinnatiVersionNotFoundError(err) {
			return "", fmt.Errorf("no updates found for channel %s from version %s", cincinnatiChannel, actualLatestVersion.String())
		}
		return "", fmt.Errorf("failed to query Cincinnati for channel %s from version %s: %w", cincinnatiChannel, actualLatestVersion.String(), err)
	}

	if len(candidateReleases) == 0 {
		return "", nil
	}
	return c.selectBestVersionFromCandidates(ctx, cincinnatiClient, channelGroup, targetMinor, candidateReleases, actualLatestVersion)
}

// selectBestVersionFromCandidates finds the best version to upgrade to from a list of candidate releases.
// It accepts an unsorted list of candidates and prioritizes versions that are gateways to the next minor version.
// For Z-stream upgrades, if the current actual version has an edge to the next minor, the selected version
// must also have that edge to avoid breaking the upgrade path.
func (c *controlPlaneUpgradeSyncer) selectBestVersionFromCandidates(ctx context.Context, cincinnatiClient cincinatti.Client, channelGroup string, targetMinor string, candidates []configv1.Release, actualLatestVersion semver.Version) (string, error) {
	// Sort candidates by version (descending - latest first)
	sortReleasesByVersionDescending(candidates)

	targetMinorVersion, err := semver.ParseTolerant(targetMinor)
	if err != nil {
		return "", fmt.Errorf("invalid desired minor %s: %w", targetMinor, err)
	}

	nextMinor := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor+1)

	logger := utils.LoggerFromContext(ctx)
	var latestOverall string
	for _, update := range candidates {
		ver, err := semver.Parse(update.Version)
		if err != nil { // should never happen
			continue
		}

		// Only consider versions in target minor
		if ver.Major != targetMinorVersion.Major || ver.Minor != targetMinorVersion.Minor {
			logger.V(2).Info("Skipping version not in target minor",
				"version", ver.String(),
				"targetMinor", targetMinor,
			)
			continue
		}

		// Track first (latest) version in target minor
		if len(latestOverall) == 0 {
			latestOverall = ver.String()
		}

		// Check if this version is a gateway to next minor
		isGateway, err := isGatewayToNextMinor(ctx, ver, cincinnatiClient, channelGroup, nextMinor)
		if err != nil {
			return "", err
		}

		if isGateway {
			return ver.String(), nil
		}
	}

	// No gateway to next minor found
	if len(latestOverall) == 0 {
		return "", nil
	}

	// For Z-stream upgrades, check if the current actual version has an edge to the next minor
	// If it does, we must maintain that upgrade path - don't select a version without a gateway
	// For Y-stream upgrades (moving to next minor), since the user asked to move to the next minor and we care
	// about landing there more, we can return the latest version without this check
	actualMinor := fmt.Sprintf("%d.%d", actualLatestVersion.Major, actualLatestVersion.Minor)
	if actualMinor == targetMinor {
		// Z-stream upgrade: check if we would break an existing upgrade path
		actualHasEdgeToNextMinor, err := isGatewayToNextMinor(ctx, actualLatestVersion, cincinnatiClient, channelGroup, nextMinor)
		if err != nil {
			return "", err
		}

		if actualHasEdgeToNextMinor {
			// Current version has edge to next minor, but no gateway found in candidates
			// Cannot upgrade safely - would break the upgrade path
			return "", nil
		}
	}

	return latestOverall, nil
}
