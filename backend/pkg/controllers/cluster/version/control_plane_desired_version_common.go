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

package version

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterCreateGracePeriod is how long after a cluster's CreatedAt we
// suppress automatic desired-version recomputation while an active Create
// operation is still in flight. After this window the create is expected
// to have finished, so resuming z-stream selection is safe.
const clusterCreateGracePeriod = 2 * time.Hour

// nightlyChannelGroup is the OpenShift channel group whose builds are published to the CI
// releasestream API rather than the Cincinnati graph API. controlplaneversion.SelectControlPlaneVersion
// resolves versions via the graph API (api.openshift.com) and therefore cannot serve nightly; the
// nightly channel guard in upgradeDesiredControlPlaneZVersion routes nightly to the internal/cincinnati
// gateway logic instead.
const nightlyChannelGroup = "nightly"

// defaultControlPlaneVersionOffset is the offset from the tip of a channel used when selecting a
// desired control plane version. Offset 0 selects the most recent (tip) release in the channel.
const defaultControlPlaneVersionOffset uint = 0

// desiredVersionSyncerCommon holds the state and behaviour shared by the two
// control plane desired-version syncers: controlPlaneInitialVersionSyncer
// (Case 1: seed the initial desired version before any active versions exist)
// and controlPlaneUpgradeVersionSyncer (Cases 2+3: z-stream and y-stream
// upgrades). Both embed this type so the create-race gate, the Cincinnati
// client construction, and the desired-version persistence policy stay in one
// place.
type desiredVersionSyncerCommon struct {
	clock                        utilsclock.PassiveClock
	readDesireLister             kubeapplierlisters.ReadDesireLister
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	activeOperationLister        corelisters.ActiveOperationLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister

	cincinnatiClientCache cincinnati.ClientCache
	graphClient           cincinnati.GraphClient
}

// cincinnatiClientForCluster resolves the cluster UUID from the cached
// HostedCluster (best effort) and returns a Cincinnati client keyed on it. If
// the UUID cannot be found an empty value is used so the controller can still
// make progress.
func (c *desiredVersionSyncerCommon) cincinnatiClientForCluster(ctx context.Context, key controllerutils.HCPClusterKey) cincinnati.Client {
	logger := utils.LoggerFromContext(ctx)

	clusterUUID, found, err := kubeapplierhelpers.GetCachedHostedClusterUUIDForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		logger.Info("error getting cluster UUID, continuing with empty", "err", err.Error())
	}
	if !found {
		logger.Info("missing cluster UUID, continuing with empty")
	}
	return c.cincinnatiClientCache.GetOrCreateClient(clusterUUID)
}

// reportDesiredVersionResolution applies the shared persistence policy after a
// syncer resolves (or fails to resolve) a desired control plane version:
//
//   - On error: persist IntentFailed on the controller document for Cincinnati
//     VersionNotFound or any non-Cincinnati resolution error, then return nil.
//     Other (transient) Cincinnati errors are returned so the sync is retried.
//   - On success: advance the ServiceProviderCluster DesiredVersion only when
//     the newly resolved version is greater than the previously stored desired
//     (graph changes must not automatically select a lower z-stream), then clear
//     the IntentFailed condition.
//
// controllerName is the caller's Cosmos controller document ID so the initial
// and upgrade syncers write to their own controller documents.
func (c *desiredVersionSyncerCommon) reportDesiredVersionResolution(ctx context.Context, key controllerutils.HCPClusterKey, controllerName string, cachedServiceProviderCluster *coreapi.ServiceProviderCluster, desiredVersion *semver.Version, resolveErr error) error {
	logger := utils.LoggerFromContext(ctx)

	if resolveErr != nil {
		// Persist IntentFailed on the controller document for Cincinnati VersionNotFound or any non-Cincinnati resolution error.
		// Other Cincinnati errors are treated as transient graph or transport issues.
		var cincinnatiErr *cvocincinnati.Error
		persistIntentFailed := cincinnati.IsCincinnatiVersionNotFoundError(resolveErr) || !errors.As(resolveErr, &cincinnatiErr)
		if persistIntentFailed {
			logger.Error(resolveErr, "desired version resolution failed, persisting IntentFailed condition")
			controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
			if writeErr := controllerutils.WriteController(ctx, controllerCRUD, controllerName, key.InitialController,
				func(ctrl *coreapi.Controller) {
					apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
						Type:    coreapi.ControllerConditionTypeIntentFailed,
						Status:  metav1.ConditionTrue,
						Reason:  coreapi.VersionUpgradeNotAcceptedReason,
						Message: utils.ErrorMessageWithoutLineTracking(resolveErr),
					})
				}); writeErr != nil {
				return utils.TrackError(writeErr)
			}
			return nil
		}
		return utils.TrackError(resolveErr)
	}

	previousDesiredVersion := cachedServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	// Only advance stored desired when the newly resolved version is greater, so graph changes
	// cannot automatically select a lower z-stream. When rollback support is added, relax this
	// so that only SRE-enforced rollback targets can decrease desired.
	if desiredVersion != nil && (previousDesiredVersion == nil || desiredVersion.GT(*previousDesiredVersion)) {
		logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)
		// on successful resolution of the desired version.
		// update the ServiceProviderCluster first and only afterwards
		// clear the IntentFailed condition
		replacement := cachedServiceProviderCluster.DeepCopy()
		replacement.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err := serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
	if err := controllerutils.WriteController(ctx, controllerCRUD, controllerName, key.InitialController,
		func(ctrl *coreapi.Controller) {
			apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
				Type:    coreapi.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionFalse,
				Reason:  coreapi.ControllerConditionReasonAsExpected,
				Message: "",
			})
		}); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// shouldDetermineDesiredVersion decides whether a syncer should compute a
// desired control plane version on this pass. It returns true when ANY of:
//
//  1. ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion is unset
//     — we have nothing seeded yet, so we must run to fill it in.
//  2. The cluster's ARM CreatedAt is older than clusterCreateGracePeriod —
//     past that window the create flow is expected to be done and resuming
//     z-stream selection cannot race the initial DesiredVersion write.
//  3. There is no active Create operation for the cluster itself — without a
//     create in flight there is nothing to race with, so we can run.
//
// Otherwise (DesiredVersion already set, cluster still young, Create in
// flight) we skip so a freshly created cluster doesn't have its initial
// desired version overwritten while creation is still in progress.
func (c *desiredVersionSyncerCommon) shouldDetermineDesiredVersion(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster) (bool, error) {
	if spc.Spec.ControlPlaneVersion.DesiredVersion == nil {
		return true, nil
	}
	if c.clusterOlderThanGracePeriod(cluster) {
		return true, nil
	}
	hasCreate, err := c.clusterHasActiveCreateOperation(ctx, cluster)
	if err != nil {
		return true, err
	}
	return !hasCreate, nil
}

// clusterOlderThanGracePeriod returns true when the cluster's ARM CreatedAt
// is more than clusterCreateGracePeriod in the past. A missing CreatedAt is
// treated as "old enough" so a malformed document does not pin the controller
// in skip-forever mode.
func (c *desiredVersionSyncerCommon) clusterOlderThanGracePeriod(cluster *coreapi.HCPOpenShiftCluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return c.clock.Since(*cluster.SystemData.CreatedAt) > clusterCreateGracePeriod
}

// clusterHasActiveCreateOperation reports whether there is a non-terminal
// Create operation whose ExternalID is the cluster itself. Operations on
// child resources (node pools, external auths) under the cluster are
// ignored on purpose: they don't gate control-plane upgrade selection.
func (c *desiredVersionSyncerCommon) clusterHasActiveCreateOperation(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)
	if len(cluster.ServiceProviderProperties.ActiveOperationID) == 0 {
		logger.Info("Cluster has no active create operation", "cluster", cluster.Name)
		return false, nil
	}
	operation, err := c.activeOperationLister.Get(ctx, cluster.ResourceID.SubscriptionID, cluster.ServiceProviderProperties.ActiveOperationID)
	if err != nil {
		return false, fmt.Errorf("failed to get operations %q for cluster: %w", cluster.ServiceProviderProperties.ActiveOperationID, err)
	}
	if operation.Request != cosmosstorageutils.OperationRequestCreate {
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

// FindAllUpgradeTargetVersionsInMinor queries Cincinnati and finds the latest version within the specified target minor.
//
// This method implements the core version selection logic for all upgrade scenarios (both Y-stream and Z-stream).
// It prioritizes versions that have an upgrade path to the next minor version (gateway versions).
//
// Version selection algorithm:
//  1. Query Cincinnati for all available updates from EACH active version in the target minor channel
//  2. Filter candidates: only include versions within the target minor
//  3. Intersect candidate sets: only keep versions reachable from ALL active versions
//  4. Sort candidates by version (descending - latest first)
//
// Examples:
//   - Z-stream (4.19.15 → 4.19.z): Find latest 4.19.z with path to 4.20, or latest 4.19.z
//   - Y-stream (4.19.x → 4.20.z): Find latest 4.20.z with path to 4.21, or latest 4.20.z
//
// When multiple active versions are provided, this method ensures that the selected version
// is reachable from ALL active versions by intersecting the upgrade paths.
//
// Returns nil if no suitable version is found.
func FindAllUpgradeTargetVersionsInMinor(
	ctx context.Context,
	cincinnatiClient cincinnati.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	activeVersions []semver.Version,
) ([]semver.Version, error) {
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
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

	return commonCandidates, nil
}

// FindBestVersionInMinor queries Cincinnati and finds the latest version within the specified target minor.
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
//  8. If no gateway found and preferLatestOverGateway (y-stream): return the latest candidate
//  9. If no gateway found and !preferLatestOverGateway (z-stream): return nil
//
// Examples:
//   - Z-stream (4.19.15 → 4.19.z): Find latest 4.19.z with path to 4.20, or nil if none
//   - Y-stream (4.19.x → 4.20.z): Find latest 4.20.z with path to 4.21, or latest 4.20.z
//
// When multiple active versions are provided, this method ensures that the selected version
// is reachable from ALL active versions by intersecting the upgrade paths.
//
// Returns nil if no suitable version is found.
func FindBestVersionInMinor(
	ctx context.Context,
	cincinnatiClient cincinnati.Client,
	graphClient cincinnati.GraphClient,
	channelGroup string,
	targetMinorVersion semver.Version,
	activeVersions []semver.Version,
	preferLatestOverGateway bool,
) (*semver.Version, error) {
	commonCandidates, err := FindAllUpgradeTargetVersionsInMinor(ctx, cincinnatiClient, channelGroup, targetMinorVersion, activeVersions)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return selectBestVersionFromCandidates(ctx, cincinnatiClient, graphClient, channelGroup, targetMinorVersion, commonCandidates, preferLatestOverGateway)
}

// selectBestVersionFromCandidates finds the best version to upgrade to from a list of candidate versions.
// It accepts a list of candidates (already filtered within the target minor) and prioritizes versions
// that are gateways to the next minor version.
//
// When preferLatestOverGateway is true (y-stream upgrades), the latest candidate is returned even if
// no gateway to the next minor exists. When false (z-stream upgrades), nil is returned if no gateway
// exists, preserving upgradeability to the next minor.
//
// Algorithm:
//  1. Sort candidates by version (descending - latest first)
//  2. Check if the next minor channel exists in Cincinnati
//  3. If next minor doesn't exist: return the latest candidate
//  4. If next minor exists: iterate through candidates to find a gateway version to the next minor
//  5. If no gateway found and preferLatestOverGateway: return the latest candidate
//  6. If no gateway found and !preferLatestOverGateway: return nil
func selectBestVersionFromCandidates(
	ctx context.Context,
	cincinnatiClient cincinnati.Client,
	graphClient cincinnati.GraphClient,
	channelGroup string,
	targetMinorVersion semver.Version,
	candidates []semver.Version,
	preferLatestOverGateway bool,
) (*semver.Version, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort candidates by version (descending - latest first)
	slices.SortFunc(candidates, func(a, b semver.Version) int {
		return b.Compare(a)
	})

	// Check if next minor channel exists before checking for gateways.
	// Query the public graph API directly (not GetUpdates from a candidate version).
	nextMinorVersion := metadataapi.NextMinorReleaseLine(targetMinorVersion)
	nextMinor := fmt.Sprintf("%d.%d", nextMinorVersion.Major, nextMinorVersion.Minor)
	nextMinorExists, err := graphClient.ChannelExists(ctx, channelGroup, nextMinor)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to check if channel %s-%s exists: %w", channelGroup, nextMinor, err))
	}

	if !nextMinorExists {
		// If the next minor doesn't exist, return the latest version in the target minor
		return &candidates[0], nil
	}

	// Prefer a candidate that is a gateway to the next minor
	for _, candidate := range candidates {
		isGateway, err := isGatewayToNextMinor(ctx, candidate, cincinnatiClient, channelGroup, nextMinor)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		if isGateway {
			return &candidate, nil
		}
	}

	if preferLatestOverGateway {
		return &candidates[0], nil
	}

	// TODO: If the next minor exists but none of the candidates have a gateway to it,
	// and none of the active versions have a gateway to it either, prefer the latest
	// candidate instead of returning nil — there is no existing next-minor path to preserve.
	return nil, nil
}
