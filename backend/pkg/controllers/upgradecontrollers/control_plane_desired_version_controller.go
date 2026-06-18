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
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/blang/semver/v4"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// controlPlaneDesiredVersionControllerName is the Cosmos controller document ID for this syncer.
const controlPlaneDesiredVersionControllerName = "ControlPlaneDesiredVersion"

// controlPlaneDesiredVersionSyncer is a Cluster syncer that manages control plane desired version.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneDesiredVersionSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	readDesireLister     dblisters.ReadDesireLister
	resourcesDBClient    database.ResourcesDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	subscriptionLister   listers.SubscriptionLister

	cincinnatiClientCache cincinnati.ClientCache
}

var _ controllerutils.ClusterSyncer = (*controlPlaneDesiredVersionSyncer)(nil)

// NewControlPlaneDesiredVersionController creates a new controller that manages the desired
// control plane version. It periodically checks each cluster and sets the desired version
// based on the OCPVersion logic documented in the ServiceProviderCluster type.
func NewControlPlaneDesiredVersionController(
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {
	syncer := &controlPlaneDesiredVersionSyncer{
		cooldownChecker:       controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		readDesireLister:      readDesireLister,
		cincinnatiClientCache: cincinnati.NewClientCache(),
		resourcesDBClient:     resourcesDBClient,
		clusterServiceClient:  clusterServiceClient,
		subscriptionLister:    subscriptionLister,
	}

	controller := controllerutils.NewClusterWatchingController(
		controlPlaneDesiredVersionControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

func (c *controlPlaneDesiredVersionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of the desired control plane version for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. (Active versions are updated by the control plane active version controller.)
//  3. Compute the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  4. If the computed desired version is greater than the previously stored desired version:
//     - Update the DesiredVersion field
//     Only SRE-enforced rollback targets are permitted to decrease desired; automatic graph
//     resolution must not lower a previously selected z-stream.
//  5. Save the updated service provider cluster state
func (c *controlPlaneDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// Currently, this is correct.  We will likely refactor and change this to separate the read of active versions from the determination
		// of the next desired version: we'll need to choose a desired version even if there are no active versions.
		return nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// Resolve the cluster UUID from the cached HostedCluster so we can build the Cincinnati client.
	// Use it as best effort.  If we cannot find use, use an empty value to make progress without a specific value.
	clusterUUID, found, err := maestrohelpers.GetCachedHostedClusterUUIDForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		logger.Info("error getting cluster UUID, continuing with empty", "err", err.Error())
	}
	if !found {
		logger.Info("missing cluster UUID, continuing with empty")
	}
	cincinnatiClient := c.cincinnatiClientCache.GetOrCreateClient(clusterUUID)

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup
	activeVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	previousDesiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	var fromVersions []semver.Version
	for _, activeVersion := range activeVersions {
		fromVersions = append(fromVersions, *activeVersion.Version)
	}
	// Include the previous desired version when it is not yet active so Cincinnati
	// candidate intersection still requires a path from that target. This prevents
	// graph changes from selecting a lower patch without an SRE-initiated rollback.
	if previousDesiredVersion != nil && !slices.ContainsFunc(activeVersions, func(activeVersion api.HCPClusterActiveVersion) bool {
		return activeVersion.Version.EQ(*previousDesiredVersion)
	}) {
		fromVersions = append([]semver.Version{*previousDesiredVersion}, fromVersions...)
	}
	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	operation := operation.Operation{
		Type:    operation.Update,
		Options: validation.AFECsToValidationOptions(subscription.GetRegisteredFeatures()),
	}
	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, cincinnatiClient, key.GetResourceID(), customerDesiredMinor, channelGroup, fromVersions,
		operation.HasOption(api.FeatureExperimentalReleaseFeatures))

	if err != nil {
		// Persist IntentFailed on the controller document for Cincinnati VersionNotFound or any non-Cincinnati resolution error.
		// Other Cincinnati errors are treated as transient graph or transport issues.
		var cincinnatiErr *cvocincinnati.Error
		persistIntentFailed := cincinnati.IsCincinnatiVersionNotFoundError(err) || !errors.As(err, &cincinnatiErr)
		if persistIntentFailed {
			logger.Error(err, "desired version resolution failed, persisting IntentFailed condition")
			controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
			if writeErr := controllerutils.WriteController(ctx, controllerCRUD, controlPlaneDesiredVersionControllerName, key.InitialController,
				func(ctrl *api.Controller) {
					apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
						Type:    api.ControllerConditionTypeIntentFailed,
						Status:  metav1.ConditionTrue,
						Reason:  api.VersionUpgradeNotAcceptedReason,
						Message: utils.ErrorMessageWithoutLineTracking(err),
					})
				}); writeErr != nil {
				return utils.TrackError(writeErr)
			}
			return nil
		}
		return utils.TrackError(err)
	}

	desiredVersionUpdated := false
	// Only advance stored desired when the newly resolved version is greater, so graph changes
	// cannot automatically select a lower z-stream. When rollback support is added, relax this
	// so that only SRE-enforced rollback targets can decrease desired.
	if desiredVersion != nil && (previousDesiredVersion == nil || desiredVersion.GT(*previousDesiredVersion)) {
		logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)
		existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
		desiredVersionUpdated = true
	}

	// on successful resolution of the desired version.
	// update the ServiceProviderCluster first and only afterwards
	// clear the IntentFailed condition
	if desiredVersionUpdated {
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err := serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
	if err = controllerutils.WriteController(ctx, controllerCRUD, controlPlaneDesiredVersionControllerName, key.InitialController,
		func(ctrl *api.Controller) {
			apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionFalse,
				Reason:  api.ControllerConditionReasonAsExpected,
				Message: "",
			})
		}); err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// desiredControlPlaneZVersion determines the desired z-stream version for the control plane.
//
// The desired version selection logic is executed on each controller sync.
// NOTE: Rollback to a previous z-stream is not currently supported (future enhancement).
//
// fromVersions supplies the anchor versions for Cincinnati intersection: active control plane
// versions plus, when not yet active, the previous desired version (prepended first).
//
// It dispatches to one of three resolution methods based on the current cluster state:
// - Case 1: Initial version selection (no from versions)
// - Case 2: Z-stream managed upgrade (customer desired minor == actual minor)
// - Case 3: Next Y-stream user-initiated upgrade (customer desired minor == actual minor + 1)
//
// customerDesiredMinor and channelGroup are required. If they are not specified, no version is returned.
// Returns nil if no upgrade is needed.
func (c *controlPlaneDesiredVersionSyncer) desiredControlPlaneZVersion(ctx context.Context, cincinnatiClient cincinnati.Client, clusterResourceID *azcorearm.ResourceID,
	customerDesiredMinor string, channelGroup string, fromVersions []semver.Version, allowExperimentalReleaseFeatures bool) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)

	if len(customerDesiredMinor) == 0 {
		logger.Info("No desired minor version specified. Terminating version resolution.")
		return nil, nil
	}
	if len(channelGroup) == 0 {
		logger.Info("No channel group specified. Terminating version resolution.")
		return nil, nil
	}

	if len(fromVersions) == 0 {
		logger.Info("Resolving initial desired version", "customerDesiredMinor", customerDesiredMinor, "channelGroup", channelGroup)

		// ParseTolerant handles both "4.19" and "4.19.0" formats
		customerDotZeroRelease := api.Must(semver.ParseTolerant(customerDesiredMinor))

		initialDesiredVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, channelGroup, customerDotZeroRelease, []semver.Version{customerDotZeroRelease}, false)
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

	actualLatestVersion := fromVersions[0]
	actualLatestMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

	// ParseTolerant handles both "4.19", "4.19.0" and full versions like "4.20.15". Normalize to major.minor.0
	// so that same-minor z-stream (e.g. 4.20.0 -> 4.20.15) is not mistaken for a y-stream upgrade.
	parsedDesired := api.Must(semver.ParseTolerant(customerDesiredMinor))
	desiredMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", parsedDesired.Major, parsedDesired.Minor))

	if desiredMinorVersion.LT(actualLatestMinorVersion) {
		return nil, utils.TrackError(fmt.Errorf(
			"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades",
			actualLatestMinorVersion.String(), desiredMinorVersion.String(),
		))
	}

	if desiredMinorVersion.GT(actualLatestMinorVersion) {
		if desiredMinorVersion.Major >= 5 && !allowExperimentalReleaseFeatures {
			return nil, utils.TrackError(errors.New("OpenShift v5 and above is not supported"))
		}
		if err := validation.OpenshiftVersionAtMostOneMinorSkew(actualLatestMinorVersion.String(), desiredMinorVersion.String()); err != nil {
			return nil, utils.TrackError(err)
		}
		clusterNodePools, err := c.listClusterAdmissionNodePools(ctx, clusterResourceID)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		if err := admission.AdmitClusterNodePoolsMinorVersionSkew(ctx, clusterNodePools, desiredMinorVersion); err != nil {
			return nil, utils.TrackError(err)
		}
	}

	if desiredMinorVersion.EQ(actualLatestMinorVersion) {
		return FindBestVersionInMinor(ctx, cincinnatiClient, channelGroup, desiredMinorVersion, fromVersions, false)
	}

	logger.Info("Resolving user-initiated upgrade desired version", "actualMinor", actualLatestMinorVersion.String(), "fromVersions", fromVersions,
		"channelGroup", channelGroup, "targetMinor", desiredMinorVersion.String())

	latestVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, channelGroup, desiredMinorVersion, fromVersions, true)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if latestVersion != nil {
		return latestVersion, nil
	}

	// User-requested control plane minor has no path yet; advance to latest patch on the current minor toward a gateway for a later user-initiated upgrade.
	fallbackVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, channelGroup, actualLatestMinorVersion, fromVersions, false)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if fallbackVersion != nil {
		return fallbackVersion, nil
	}

	return nil, utils.TrackError(fmt.Errorf(
		"no upgrade path found from %s to %s: no reachable versions in target minor and no gateway version in current minor",
		actualLatestVersion.String(), desiredMinorVersion.String(),
	))
}

// listClusterAdmissionNodePools prefetches every node pool under clusterResourceID
// paired with its service-provider record, in the same shape that
// frontend.newClusterAdmissionContext builds for cluster admission. The upgrade
// controller passes the result to admission.AdmitClusterNodePoolsMinorVersionSkew
// directly so that admission code stays free of any DB dependency.
func (c *controlPlaneDesiredVersionSyncer) listClusterAdmissionNodePools(ctx context.Context, clusterResourceID *azcorearm.ResourceID) ([]admission.ClusterAdmissionNodePool, error) {
	nodePoolIterator, err := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).NodePools(clusterResourceID.Name).List(ctx, nil)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	var clusterNodePools []admission.ClusterAdmissionNodePool
	for _, nodePool := range nodePoolIterator.Items(ctx) {
		spNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, nodePool.ID)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		clusterNodePools = append(clusterNodePools, admission.ClusterAdmissionNodePool{
			NodePool:                nodePool,
			ServiceProviderNodePool: spNodePool,
		})
	}
	if err := nodePoolIterator.GetError(); err != nil {
		return nil, utils.TrackError(err)
	}
	return clusterNodePools, nil
}

// FindAllUpgradeTargetVersionsInMinor queries Cincinnati and finds the latest version within the specified target minor.
//
// This method implements the core version selection logic for all upgrade scenarios (both Y-stream and Z-stream).
// It prioritizes versions that have an upgrade path to the next minor version (gateway versions).
//
// Version selection algorithm:
//  1. Query Cincinnati for all available updates from EACH fromVersion in the target minor channel
//  2. Filter candidates: only include versions within the target minor
//  3. Intersect candidate sets: only keep versions reachable from ALL fromVersions
//  4. Sort candidates by version (descending - latest first)
//
// Examples:
//   - Z-stream (4.19.15 → 4.19.z): Find latest 4.19.z with path to 4.20, or latest 4.19.z
//   - Y-stream (4.19.x → 4.20.z): Find latest 4.20.z with path to 4.21, or latest 4.20.z
//
// When multiple from versions are provided, this method ensures that the selected version
// is reachable from ALL from versions by intersecting the upgrade paths.
//
// Returns nil if no suitable version is found.
func FindAllUpgradeTargetVersionsInMinor(
	ctx context.Context,
	cincinnatiClient cincinnati.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	fromVersions []semver.Version,
) ([]semver.Version, error) {
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err))
	}

	targetMinorString := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor)
	cincinnatiChannel := fmt.Sprintf("%s-%s", channelGroup, targetMinorString)

	// Intersect upgrade candidates across all fromVersions.
	candidatesByVersion := map[string]struct {
		version semver.Version
		count   int
	}{}

	for _, fromVersion := range fromVersions {
		_, candidateReleases, _, err := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, fromVersion)
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

	// Extract only candidates that appeared for ALL fromVersions (intersection).
	commonCandidates := []semver.Version{}
	for _, candidateEntry := range candidatesByVersion {
		if candidateEntry.count == len(fromVersions) {
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
//  1. Query Cincinnati for all available updates from EACH fromVersion in the target minor channel
//  2. Filter candidates: only include versions within the target minor
//  3. Intersect candidate sets: only keep versions reachable from ALL fromVersions
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
// When multiple from versions are provided, this method ensures that the selected version
// is reachable from ALL from versions by intersecting the upgrade paths.
//
// Returns nil if no suitable version is found.
func FindBestVersionInMinor(
	ctx context.Context,
	cincinnatiClient cincinnati.Client,
	channelGroup string,
	targetMinorVersion semver.Version,
	fromVersions []semver.Version,
	preferLatestOverGateway bool,
) (*semver.Version, error) {
	commonCandidates, err := FindAllUpgradeTargetVersionsInMinor(ctx, cincinnatiClient, channelGroup, targetMinorVersion, fromVersions)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return selectBestVersionFromCandidates(ctx, cincinnatiClient, channelGroup, targetMinorVersion, commonCandidates, preferLatestOverGateway)
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

	// Check if next minor channel exists before checking for gateways
	nextMinor := fmt.Sprintf("%d.%d", targetMinorVersion.Major, targetMinorVersion.Minor+1)
	// Here we are sure that the latest candidate version is in the graph,
	// we just want to check if next minor exists
	// If we get VersionNotFound error, it means that the next minor doesn't exist
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err))
	}

	_, _, _, nextMinorExistsErr := cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", fmt.Sprintf("%s-%s", channelGroup, nextMinor), candidates[0])

	if nextMinorExistsErr != nil && !cincinnati.IsCincinnatiVersionNotFoundError(nextMinorExistsErr) {
		return nil, utils.TrackError(nextMinorExistsErr)
	}

	nextMinorExists := nextMinorExistsErr == nil

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
	return nil, nil
}
