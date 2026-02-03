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
	"github.com/google/uuid"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"

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
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
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
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
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

	csCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Create Cincinnati client with cluster UUID from Cluster Service
	clusterID, err := uuid.Parse(csCluster.ExternalID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid cluster UUID from Cluster Service: %w", err))
	}
	cincinnatiClient := cincinatti.NewCincinnatiClient(clusterID)

	err = c.updateActualVersionInStatus(csCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update actual version in status: %w", err))
	}

	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, cincinnatiClient, existingCluster, existingServiceProviderCluster)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Selected desired version",
		"desiredVersion", desiredVersion,
		"previousDesiredVersion", existingServiceProviderCluster.Version.DesiredVersion,
	)

	previousDesiredVersion := existingServiceProviderCluster.Version.DesiredVersion
	if len(desiredVersion) > 0 && desiredVersion != previousDesiredVersion {
		existingServiceProviderCluster.Version.DesiredVersion = desiredVersion

		// TODO: Make API call to version service to trigger the upgrade
		// The version service API is idempotent:
		// - If desiredVersion == current cluster version: NOOP
		// - Otherwise: Initiate the upgrade to desiredVersion

		// For now, just log that we would trigger an upgrade
		logger.Info("Would trigger control plane upgrade",
			"cluster", key.HCPClusterName,
			"desiredVersion", desiredVersion,
			"previousDesiredVersion", previousDesiredVersion)
	}

	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// updateActualVersionInStatus extracts the actual version from the Cluster Service cluster
// and updates version.active_versions with the retrieved actual version.
func (c *controlPlaneUpgradeSyncer) updateActualVersionInStatus(csCluster *arohcpv1alpha1.Cluster, serviceProviderCluster *api.ServiceProviderCluster) error {
	version, ok := csCluster.GetVersion()
	if !ok {
		return fmt.Errorf("cluster version not found in Cluster Service response")
	}

	actualVersion := version.RawID()

	if serviceProviderCluster.Version.ActiveVersions == nil {
		serviceProviderCluster.Version.ActiveVersions = []api.HCPClusterActiveVersion{}
	}

	// Check if the tip (most recent version) is already the actual version
	// If so, no need to update (avoids prepending the same version repeatedly)
	if len(serviceProviderCluster.Version.ActiveVersions) > 0 &&
		serviceProviderCluster.Version.ActiveVersions[0].Version == actualVersion {
		return nil
	}

	// Create and prepend the new active version entry at the tip
	newActiveVersion := api.HCPClusterActiveVersion{
		Version:            actualVersion,
		LastTransitionTime: time.Now().UTC().Format(time.RFC3339),
	}

	serviceProviderCluster.Version.ActiveVersions = append(
		[]api.HCPClusterActiveVersion{newActiveVersion},
		serviceProviderCluster.Version.ActiveVersions...,
	)

	return nil
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
	targetCincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, customerDesiredMinor)

	actualLatestVersionStr := c.getLatestActiveVersion(serviceProviderCluster)

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Retrieved cluster version state",
		"actualLatestVersion", actualLatestVersionStr,
		"customerDesiredMinor", customerDesiredMinor,
		"channelGroup", channelGroup,
	)

	// NOTE: This should rarely happen - indicates installation version selection issue.
	if len(actualLatestVersionStr) == 0 {
		logger.Info("Resolving initial desired version for new cluster",
			"customerDesiredMinor", customerDesiredMinor,
			"channelGroup", channelGroup,
		)
		// Use seed version x.y.0 to discover available versions
		// TODO: Verify seeding approach for all versions (for sure it doesn't work for nightly).
		seedVersionString := customerDesiredMinor + ".0"
		seedVersion, err := semver.Parse(seedVersionString)
		if err != nil {
			return "", fmt.Errorf("invalid seed version %s: %w", seedVersionString, err)
		}
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, targetCincinnatiChannel, seedVersion)
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
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, targetCincinnatiChannel, actualLatestVersion)
	}

	// If we reach here it is because we need to do a next y-stream upgrade (actual minor != desired minor)
	// Validate that desired minor is exactly one minor ahead (actualMinor + 1)
	// We don't allow downgrades or skipping minor versions
	if !isValidNextYStreamUpgradePath(actualMinor, customerDesiredMinor) {
		return "", fmt.Errorf("invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades or skipping minor versions", actualMinor, customerDesiredMinor)
	}

	return c.resolveNextYStream(ctx, cincinnatiClient, targetCincinnatiChannel, actualLatestVersion)
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
func (c *controlPlaneUpgradeSyncer) resolveNextYStream(ctx context.Context, cincinnatiClient cincinatti.Client, targetCincinnatiChannel string, actualLatestVersion semver.Version) (string, error) {
	channelGroup, customerDesiredMinor, err := cincinatti.ParseCincinnatiChannel(targetCincinnatiChannel)
	if err != nil {
		return "", err
	}

	actualMinor := fmt.Sprintf("%d.%d", actualLatestVersion.Major, actualLatestVersion.Minor)
	actualLatestVersionStr := actualLatestVersion.String()

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Resolving next Y-stream upgrade",
		"actualMinor", actualMinor,
		"actualLatestVersion", actualLatestVersionStr,
		"targetCincinnatiChannel", targetCincinnatiChannel,
		"customerDesiredMinor", customerDesiredMinor,
		"channelGroup", channelGroup,
	)

	// Try to find latest version in target minor (Y-stream upgrade)
	latestVersion, err := c.findLatestVersionInMinor(ctx, cincinnatiClient, targetCincinnatiChannel, actualLatestVersion)
	if err != nil {
		return "", err
	}

	// If no upgrade path to target minor, fall back to Z-stream upgrade in current minor
	// This helps the cluster reach a gateway version that may enable the Y-stream upgrade later
	if latestVersion == "" {
		logger.Info("No upgrade path to target minor, falling back to Z-stream upgrade in actual minor",
			"actualMinor", actualMinor,
			"customerDesiredMinor", customerDesiredMinor,
		)
		fallbackCincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, actualMinor)
		return c.findLatestVersionInMinor(ctx, cincinnatiClient, fallbackCincinnatiChannel, actualLatestVersion)
	}

	logger.Info("Successfully found Y-stream upgrade path",
		"selectedVersion", latestVersion,
		"targetMinor", customerDesiredMinor,
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
func (c *controlPlaneUpgradeSyncer) findLatestVersionInMinor(ctx context.Context, cincinnatiClient cincinatti.Client, cincinnatiChannel string, actualLatestVersion semver.Version) (string, error) {
	logger := utils.LoggerFromContext(ctx)

	logger.Info("Querying Cincinnati for latest version in minor",
		"cincinnatiChannel", cincinnatiChannel,
		"actualLatestVersion", actualLatestVersion.String(),
	)

	candidateReleases, err := getAndCombineUpdates(ctx, cincinnatiClient, cincinnatiChannel, actualLatestVersion)
	if err != nil {
		if cincinatti.IsCincinnatiVersionNotFoundError(err) {
			return "", fmt.Errorf("no updates found for channel %s from version %s", cincinnatiChannel, actualLatestVersion.String())
		}
		return "", fmt.Errorf("failed to query Cincinnati for channel %s from version %s: %w", cincinnatiChannel, actualLatestVersion.String(), err)
	}

	if len(candidateReleases) == 0 {
		return "", nil
	}

	logger.V(2).Info("Starting version selection",
		"candidateCount", len(candidateReleases),
		"cincinnatiChannel", cincinnatiChannel,
		"actualLatestVersion", actualLatestVersion.String(),
	)

	sortReleasesByVersionDescending(candidateReleases)

	return c.selectBestVersionFromCandidates(ctx, cincinnatiClient, cincinnatiChannel, candidateReleases, actualLatestVersion)
}

// selectBestVersionFromCandidates finds the best version to upgrade to from a list of candidate releases.
// It prioritizes versions that are gateways to the next minor version, falling back to the latest version overall.
func (c *controlPlaneUpgradeSyncer) selectBestVersionFromCandidates(ctx context.Context, cincinnatiClient cincinatti.Client, cincinnatiChannel string, candidates []configv1.Release, actualLatestVersion semver.Version) (string, error) {
	channelGroup, targetMinor, err := cincinatti.ParseCincinnatiChannel(cincinnatiChannel)
	if err != nil {
		return "", err
	}

	targetMinorVersion, err := semver.Parse(targetMinor + ".0")
	if err != nil {
		return "", fmt.Errorf("invalid desired minor %s: %w", targetMinor, err)
	}

	nextMinor := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor+1)
	nextMinorCincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, nextMinor)

	logger := utils.LoggerFromContext(ctx)
	var latestOverall string
	for _, update := range candidates {
		ver, err := semver.Parse(update.Version)
		if err != nil { // should never happen
			continue
		}

		// Only consider versions in target minor
		if !isVersionInTargetMinor(ver, targetMinorVersion) {
			logger.V(2).Info("Skipping version not in target minor",
				"version", ver.String(),
				"targetMinor", targetMinor,
			)
			continue
		}

		// Track first (latest) version in target minor
		if latestOverall == "" {
			latestOverall = ver.String()
		}

		// Check if this version is a gateway to next minor
		isGateway, err := isGatewayToNextMinor(ctx, ver, cincinnatiClient, nextMinorCincinnatiChannel)
		if err != nil {
			return "", err
		}

		if isGateway {
			logger.Info("Selected latest version with gateway to next minor",
				"selectedVersion", ver.String(),
				"targetMinor", targetMinor,
				"nextMinor", nextMinor,
			)
			return ver.String(), nil
		}
	}

	// No gateway to next minor found, return latest version in target minor (if any)
	if latestOverall == "" {
		// No versions found in target minor - no upgrade path exists from actualLatestVersion
		logger.Info("No upgrade path found to target minor",
			"actualLatestVersion", actualLatestVersion.String(),
			"targetMinor", targetMinor,
		)
		return "", nil
	}

	logger.Info("No gateway found to next minor, selected latest version",
		"selectedVersion", latestOverall,
		"targetMinor", targetMinor,
		"nextMinor", nextMinor,
	)
	return latestOverall, nil
}

// getLatestActiveVersion retrieves the most recent active version from the service provider cluster.
// Returns an empty string if no active version is recorded yet (e.g., for newly created clusters).
// TODO: Confirm if we need to consider all the versions transitions from the beginning?
func (c *controlPlaneUpgradeSyncer) getLatestActiveVersion(serviceProviderCluster *api.ServiceProviderCluster) string {
	if len(serviceProviderCluster.Version.ActiveVersions) > 0 {
		return serviceProviderCluster.Version.ActiveVersions[0].Version
	}
	return ""
}
