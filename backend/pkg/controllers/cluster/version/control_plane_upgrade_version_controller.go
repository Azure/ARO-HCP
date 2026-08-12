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
	"net/http"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/operation"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// controlPlaneDesiredVersionControllerName is the Cosmos controller document ID for this syncer.
// It is intentionally kept as "ControlPlaneDesiredVersion" (rather than renamed to match the
// upgrade split) to preserve metrics continuity and the existing controller document identity.
const controlPlaneDesiredVersionControllerName = "ControlPlaneDesiredVersion"

// controlPlaneUpgradeVersionSyncer manages control plane desired version for clusters that already
// report active versions. It handles automated (managed) z-stream (patch) upgrades and assists with
// y-stream (minor) version upgrades by selecting the appropriate z-stream within the user-desired
// minor version. Initial version selection (no active versions yet) is handled by
// controlPlaneInitialVersionSyncer.
type controlPlaneUpgradeVersionSyncer struct {
	desiredVersionSyncerCommon

	clusterServiceClient          ocm.ClusterServiceClientSpec
	subscriptionLister            corelisters.SubscriptionLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister

	// roundTripper is used by controlplaneversion.SelectControlPlaneVersion to query the Cincinnati
	// graph API for graph-based tip selection on non-nightly channels. Production wiring sets this to
	// the default HTTP transport. When nil (e.g. unit tests that drive the internal/cincinnati mock)
	// z-stream selection falls back to the gateway-based FindBestVersionInMinor logic.
	roundTripper controlplaneversion.RoundTrip
}

var _ controllerutils.ClusterSyncer = (*controlPlaneUpgradeVersionSyncer)(nil)

// NewControlPlaneDesiredVersionController creates a new controller that manages control plane
// z-stream and y-stream upgrades for clusters that already report active versions. It periodically
// checks each cluster and advances the desired version based on the OCPVersion logic documented in
// the ServiceProviderCluster type.
//
// The constructor name and signature are unchanged from the pre-split combined controller so
// existing wiring keeps working; the initial-version selection was extracted into
// NewControlPlaneInitialVersionController.
func NewControlPlaneDesiredVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister corelisters.ActiveOperationLister,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
	subscriptionLister corelisters.SubscriptionLister,
) controllerutils.Controller {
	syncer := &controlPlaneUpgradeVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			clock:                        clock,
			readDesireLister:             readDesireLister,
			resourcesDBClient:            resourcesDBClient,
			activeOperationLister:        activeOperationLister,
			serviceProviderClusterLister: serviceProviderClusterLister,
			cincinnatiClientCache:        cincinnati.NewClientCache(),
			graphClient:                  cincinnati.NewGraphClient(),
		},
		roundTripper:                  http.DefaultTransport.RoundTrip,
		clusterServiceClient:          clusterServiceClient,
		subscriptionLister:            subscriptionLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
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

// NeedsWork reports whether this controller applies to the given cluster. The upgrade controller
// only acts once the cluster reports at least one active version; before that,
// controlPlaneInitialVersionSyncer seeds the initial desired version.
func (c *controlPlaneUpgradeVersionSyncer) NeedsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster != nil && len(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) > 0
}

// SyncOnce performs a single reconciliation of the desired control plane version for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Skip unless NeedsWork (cluster has active versions)
//  3. (Active versions are updated by the control plane active version controller.)
//  4. Compute the desired z-stream version based on upgrade logic (z-stream/y-stream)
//  5. If the computed desired version is greater than the previously stored desired version:
//     - Update the DesiredVersion field
//     Only SRE-enforced rollback targets are permitted to decrease desired; automatic graph
//     resolution must not lower a previously selected z-stream.
//  6. Save the updated service provider cluster state
func (c *controlPlaneUpgradeVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// Only clusters that already report active versions are our concern; the
	// initial controller seeds the desired version before that.
	if !c.NeedsWork(cachedServiceProviderCluster) {
		return nil
	}

	// here we check to see if we should be determining upgrade versions. We do this by
	// 1. if existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion is empty, then we must run so we can fill it in.
	// 2. if the cluster was created more than two hours ago, then we can run
	// 3. if there is no active operation that is a create, then we can run
	shouldRun, err := c.shouldDetermineDesiredVersion(ctx, existingCluster, cachedServiceProviderCluster)
	if err != nil {
		logger.Error(err, "error determining if desired version should be determined")
	} else if !shouldRun {
		return nil
	}

	// Experimental exact-version pin: when the customer pinned an exact control
	// plane version, use it directly as the desired version and skip the
	// z-stream/y-stream Cincinnati/gateway resolution entirely.
	if exact := existingCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion; exact != nil {
		logger.Info("Using pinned exact control plane version", "exactVersion", exact.String())
		desiredVersion := *exact
		return c.reportDesiredVersionResolution(ctx, key, controlPlaneDesiredVersionControllerName, cachedServiceProviderCluster, &desiredVersion, nil)
	}

	cincinnatiClient := c.cincinnatiClientForCluster(ctx, key)

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup
	activeVersions := cachedServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	op := operation.Operation{
		Type:    operation.Update,
		Options: validation.AFECsToValidationOptions(subscription.GetRegisteredFeatures()),
	}
	desiredVersion, resolveErr := c.upgradeDesiredControlPlaneZVersion(ctx, cincinnatiClient, key.GetResourceID(), customerDesiredMinor, channelGroup, activeVersions,
		op.HasOption(metadataapi.FeatureExperimentalReleaseFeatures))

	return c.reportDesiredVersionResolution(ctx, key, controlPlaneDesiredVersionControllerName, cachedServiceProviderCluster, desiredVersion, resolveErr)
}

// upgradeDesiredControlPlaneZVersion determines the desired z-stream version for the control plane
// of a cluster that already reports active versions.
//
// The desired version selection logic is executed on each controller sync.
// NOTE: Rollback to a previous z-stream is not currently supported (future enhancement).
//
// It dispatches to one of two resolution methods based on the current cluster state:
// - Case 2: Z-stream managed upgrade (customer desired minor == actual minor)
// - Case 3: Next Y-stream user-initiated upgrade (customer desired minor == actual minor + 1)
//
// customerDesiredMinor and channelGroup are required. If they are not specified, no version is returned.
// Returns nil if no upgrade is needed. Initial version selection (no active versions) is handled by
// controlPlaneInitialVersionSyncer.
func (c *controlPlaneUpgradeVersionSyncer) upgradeDesiredControlPlaneZVersion(ctx context.Context, cincinnatiClient cincinnati.Client, clusterResourceID *azcorearm.ResourceID,
	customerDesiredMinor string, channelGroup string, activeVersions []coreapi.HCPClusterActiveVersion, allowExperimentalReleaseFeatures bool) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)

	if len(customerDesiredMinor) == 0 {
		logger.Info("No desired minor version specified. Terminating version resolution.")
		return nil, nil
	}
	if len(channelGroup) == 0 {
		logger.Info("No channel group specified. Terminating version resolution.")
		return nil, nil
	}

	if len(activeVersions) == 0 {
		// Initial version selection is controlPlaneInitialVersionSyncer's job.
		return nil, nil
	}

	// Extract active versions and determine actual minor (if any)
	// Use the most recent version to determine the minor version
	actualLatestVersion := activeVersions[0].Version

	actualLatestMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

	// ParseTolerant handles both "4.19", "4.19.0" and full versions like "4.20.15". Normalize to major.minor.0
	// so that same-minor z-stream (e.g. 4.20.0 -> 4.20.15) is not mistaken for a y-stream upgrade.
	parsedDesired := metadataapi.Must(semver.ParseTolerant(customerDesiredMinor))
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
		clusterNodePools, ready, err := c.listClusterAdmissionNodePools(ctx, clusterResourceID)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		if !ready {
			// Skip until every node pool has its ServiceProviderNodePool;
			// without complete version data we can't validate minor skew.
			return nil, nil
		}
		if err := admission.AdmitClusterNodePoolsMinorVersionSkew(ctx, clusterNodePools, desiredMinorVersion); err != nil {
			return nil, utils.TrackError(err)
		}
	}

	activeVersionList := make([]semver.Version, 0, len(activeVersions))
	for _, av := range activeVersions {
		activeVersionList = append(activeVersionList, *av.Version)
	}

	if desiredMinorVersion.EQ(actualLatestMinorVersion) {
		// Nightly channel guard: controlplaneversion.SelectControlPlaneVersion resolves versions via
		// the Cincinnati graph API (api.openshift.com), which does not serve the "nightly" channel
		// group (nightly builds are published to the CI releasestream API). For nightly, skip
		// graph-based tip selection and fall back to the internal/cincinnati gateway logic, which
		// understands nightly. The same fallback is used when no roundTripper is configured.
		if c.roundTripper != nil && channelGroup != nightlyChannelGroup {
			return c.selectControlPlaneVersion(ctx, channelGroup, desiredMinorVersion, defaultControlPlaneVersionOffset)
		}
		return FindBestVersionInMinor(ctx, cincinnatiClient, c.graphClient, channelGroup, desiredMinorVersion, activeVersionList, false)
	}

	logger.Info("Resolving user-initiated upgrade desired version", "actualMinor", actualLatestMinorVersion.String(), "activeVersions", activeVersions,
		"channelGroup", channelGroup, "targetMinor", desiredMinorVersion.String())

	latestVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, c.graphClient, channelGroup, desiredMinorVersion, activeVersionList, true)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if latestVersion != nil {
		return latestVersion, nil
	}

	// User-requested control plane minor has no path yet; advance to latest patch on the current minor toward a gateway for a later user-initiated upgrade.
	fallbackVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, c.graphClient, channelGroup, actualLatestMinorVersion, activeVersionList, false)
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

// selectControlPlaneVersion resolves the desired control plane version for a minor by querying the
// OpenShift update service (Cincinnati graph API) for that minor's channel and selecting the release
// at the given offset from the tip (offset 0 = most recent release). It bridges
// controlplaneversion.SelectControlPlaneVersion, which returns a configv1.Release, into the
// *semver.Version used throughout this controller.
//
// This must not be called for the nightly channel group: the graph API does not serve nightly
// builds. See the nightly channel guard in upgradeDesiredControlPlaneZVersion.
func (c *controlPlaneUpgradeVersionSyncer) selectControlPlaneVersion(ctx context.Context, channelGroup string, minorVersion semver.Version, offset uint) (*semver.Version, error) {
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, minorVersion.Major, minorVersion.Minor)
	release, err := controlplaneversion.SelectControlPlaneVersion(ctx, c.roundTripper, nil, channel, offset)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	selected, err := semver.ParseTolerant(release.Version)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse selected control plane version %q for channel %s: %w", release.Version, channel, err))
	}
	return &selected, nil
}

// listClusterAdmissionNodePools prefetches every node pool that is not in the process of being deleted
// under clusterResourceID paired with its service-provider record, in the same shape that
// frontend.newClusterAdmissionContext builds for cluster admission. The upgrade
// controller passes the result to admission.AdmitClusterNodePoolsMinorVersionSkew
// directly so that admission code stays free of any DB dependency.
//
// Returns (_, false, nil) when any node pool is missing its
// ServiceProviderNodePool. CreateServiceProviderNodePool will populate it.
// ClusterWatchingController does not watch the ServiceProviderNodePool
// informer (a child-document arrival doesn't naturally route to its parent
// cluster), so the retry happens on the controller's periodic resync or on
// the next Cluster / ServiceProviderCluster event for this cluster.
// Skipping admission avoids using stale or missing node-pool version data
// for skew validation.
func (c *controlPlaneUpgradeVersionSyncer) listClusterAdmissionNodePools(ctx context.Context, clusterResourceID *azcorearm.ResourceID) ([]admission.ClusterAdmissionNodePool, bool, error) {
	nodePoolIterator, err := c.resourcesDBClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).NodePools(clusterResourceID.Name).List(ctx, nil)
	if err != nil {
		return nil, false, utils.TrackError(err)
	}
	var clusterNodePools []admission.ClusterAdmissionNodePool
	for _, nodePool := range nodePoolIterator.Items(ctx) {
		// When performing version skew validation, we do not include node pools
		// that are in the process of being deleted.
		if nodePool.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
		serviceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name, nodePool.ID.Name)
		if cosmosstorageutils.IsNotFoundError(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, utils.TrackError(err)
		}
		clusterNodePools = append(clusterNodePools, admission.ClusterAdmissionNodePool{
			NodePool:                nodePool,
			ServiceProviderNodePool: serviceProviderNodePool,
		})
	}
	if err := nodePoolIterator.GetError(); err != nil {
		return nil, false, utils.TrackError(err)
	}
	return clusterNodePools, true, nil
}
