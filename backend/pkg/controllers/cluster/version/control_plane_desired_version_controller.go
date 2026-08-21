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

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controlplaneversion"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// controlPlaneDesiredVersionControllerName is the Cosmos controller document ID for this syncer.
// It is intentionally kept as "ControlPlaneDesiredVersion" (rather than renamed to match the
// upgrade split) to preserve metrics continuity and the existing controller document identity.
const controlPlaneDesiredVersionControllerName = "ControlPlaneDesiredVersion"

// clusterCreateGracePeriod is how long after a cluster's CreatedAt we
// suppress automatic desired-version recomputation while an active Create
// operation is still in flight. After this window the create is expected
// to have finished, so resuming z-stream selection is safe.
const clusterCreateGracePeriod = 2 * time.Hour

// controlPlaneVersionSyncer manages the control plane desired version for a cluster: it seeds
// the initial desired version when none is set yet and advances it (z-stream/y-stream) thereafter. It
// validates the requested minor-version change and selects the desired z-stream from the tip of the
// customer's desired minor channel.
type controlPlaneVersionSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	activeOperationLister        corelisters.ActiveOperationLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister

	// roundTripper is used by selectControlPlaneVersion to query the OpenShift update service (graph
	// API) for tip selection. Production wiring sets this to the default HTTP transport.
	roundTripper controlplaneversion.RoundTrip

	clusterServiceClient          ocm.ClusterServiceClientSpec
	nodePoolLister                corelisters.NodePoolLister
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister
}

var _ controllerutils.ClusterSyncer = (*controlPlaneVersionSyncer)(nil)

// NewControlPlaneDesiredVersionController creates a new controller that manages the control plane
// desired version for every cluster: it seeds the initial desired version and drives z-stream and
// y-stream upgrades thereafter, based on the OCPVersion logic documented in the ServiceProviderCluster
// type.
func NewControlPlaneDesiredVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister corelisters.ActiveOperationLister,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	nodePoolLister corelisters.NodePoolLister,
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	syncer := &controlPlaneVersionSyncer{
		clock:                         clock,
		resourcesDBClient:             resourcesDBClient,
		clusterLister:                 clusterLister,
		activeOperationLister:         activeOperationLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		roundTripper:                  http.DefaultTransport.RoundTrip,
		clusterServiceClient:          clusterServiceClient,
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
	}

	// kubeApplierInformers is intentionally omitted: this controller resolves versions from the
	// OpenShift update service and cluster/subscription state only, so it should not be re-enqueued on
	// kube-applier events.
	controller := controllerutils.NewClusterWatchingController(
		controlPlaneDesiredVersionControllerName,
		resourcesDBClient,
		informers,
		nil,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

// NeedsWork reports whether this controller applies to the given cluster. version.id and
// version.channelGroup are both required by static validation, so the only gate here is that the
// cluster has an associated ServiceProviderCluster (created by the backend): this controller seeds
// the initial desired control plane version when none is set yet, and advances it (z-stream/y-stream)
// once one has been seeded.
func (c *controlPlaneVersionSyncer) NeedsWork(_ *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster != nil
}

// SyncOnce performs a single reconciliation of the desired control plane version for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Skip unless NeedsWork (a desired version is already seeded, and customer minor + channel group present)
//  3. Compute the desired z-stream version based on upgrade logic (z-stream/y-stream)
//  4. If the computed desired version is greater than the previously stored desired version:
//     - Update the DesiredVersion field
//     Only SRE-enforced rollback targets are permitted to decrease desired; automatic graph
//     resolution must not lower a previously selected z-stream.
//  5. Save the updated service provider cluster state and clear/raise the IntentFailed condition
func (c *controlPlaneVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
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

	if !c.NeedsWork(existingCluster, cachedServiceProviderCluster) {
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

	if exact := existingCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion; exact != nil {
		// Experimental exact-version pin: use it directly as the desired version and skip the
		// z-stream/y-stream Cincinnati/gateway resolution entirely.
		replacement := cachedServiceProviderCluster.DeepCopy()
		replacement.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(*exact)
		// Skip the write when nothing changed. DesiredVersion is a *semver.Version, so a fresh
		// pointer to the same value (e.g. an already-pinned 4.21.12) must not trigger a Replace;
		// equality.Semantic.DeepEqual compares values, not pointer identity.
		if !equality.Semantic.DeepEqual(cachedServiceProviderCluster, replacement) {
			logger.Info("Using pinned exact control plane version", "exactVersion", exact.String())
			_, replaceErr := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
			if cosmosstorageutils.IsPreconditionFailedError(replaceErr) {
				return nil
			}
			if replaceErr != nil {
				return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", replaceErr))
			}
		}

		return clearIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName)
	}

	previousDesiredVersion := cachedServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup

	// The nightly channel group is not supported for control plane version selection: nightly builds
	// are published to the CI releasestream API rather than the Cincinnati graph API used here.
	if channelGroup == nightlyChannelGroup {
		return utils.TrackError(fmt.Errorf("channel group %q is not supported for control plane version selection", channelGroup))
	}

	activeVersions := cachedServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	if validateErr := c.validateRequestedMinorVersionChange(ctx, key, customerDesiredMinor, activeVersions); validateErr != nil {
		return setIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName, validateErr)
	}

	// Active versions have no impact on which version is selected; resolve the desired version from the
	// tip (with the z-stream offset) of the customer's desired minor channel via the OpenShift update
	// service. ParseTolerant handles both "4.19" and "4.19.0" formats.
	parsedDesired, parseErr := semver.ParseTolerant(customerDesiredMinor)
	if parseErr != nil {
		return utils.TrackError(fmt.Errorf("failed to parse customer desired minor version %q: %w", customerDesiredMinor, parseErr))
	}
	desiredMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", parsedDesired.Major, parsedDesired.Minor))
	desiredVersion, resolveErr := selectControlPlaneVersion(ctx, c.roundTripper, channelGroup, desiredMinorVersion, GetZStreamOffset(channelGroup))

	// Only advance stored desired when the newly resolved version is greater, so graph changes cannot
	// automatically select a lower z-stream. When rollback support is added, relax this so that only
	// SRE-enforced rollback targets can decrease desired.
	switch {
	case resolveErr != nil:
		return setIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName, resolveErr)
	case desiredVersion == nil:
		// No upgrade is needed.
		return clearIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName)
	case previousDesiredVersion != nil && desiredVersion.LE(*previousDesiredVersion):
		// Newly resolved version is not greater than the stored desired; leave it unchanged.
		return clearIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName)
	default:
		logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)
	}

	replacement := cachedServiceProviderCluster.DeepCopy()
	replacement.Spec.ControlPlaneVersion.DesiredVersion = ptr.To(*desiredVersion)
	_, replaceErr := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil)
	if cosmosstorageutils.IsPreconditionFailedError(replaceErr) {
		return nil
	}
	if replaceErr != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", replaceErr))
	}
	return clearIntentFailed(ctx, c.resourcesDBClient, key, controlPlaneDesiredVersionControllerName)
}

// validateRequestedMinorVersionChange validates that moving the cluster's control plane to the
// customer's desired minor is permitted. It short-circuits (returns nil) when the cluster has no
// active versions yet — there is nothing to validate against. Otherwise it rejects downgrades,
// unsupported OpenShift v5+ jumps, more-than-one minor skew, and node-pool minor-version skew for a
// y-stream upgrade. Active versions do not influence which version is selected; that is done by
// selectControlPlaneVersion.
func (c *controlPlaneVersionSyncer) validateRequestedMinorVersionChange(ctx context.Context, key controllerutils.HCPClusterKey, customerDesiredMinor string, activeVersions []coreapi.HCPClusterActiveVersion) error {
	if len(activeVersions) == 0 {
		return nil
	}

	// Use the most recent active version to determine the current minor.
	actualLatestVersion := activeVersions[0].Version
	actualLatestMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

	// ParseTolerant handles both "4.19", "4.19.0" and full versions like "4.20.15". Normalize to
	// major.minor.0 so a same-minor z-stream is not mistaken for a y-stream upgrade.
	parsedDesired, err := semver.ParseTolerant(customerDesiredMinor)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to parse customer desired minor version %q: %w", customerDesiredMinor, err))
	}
	desiredMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", parsedDesired.Major, parsedDesired.Minor))

	if desiredMinorVersion.LT(actualLatestMinorVersion) {
		return utils.TrackError(fmt.Errorf(
			"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades",
			actualLatestMinorVersion.String(), desiredMinorVersion.String(),
		))
	}

	if desiredMinorVersion.GT(actualLatestMinorVersion) {
		if err := validation.OpenshiftVersionAtMostOneMinorSkew(actualLatestMinorVersion.String(), desiredMinorVersion.String()); err != nil {
			return utils.TrackError(err)
		}
		clusterNodePools, err := c.listClusterAdmissionNodePools(ctx, key.GetResourceID())
		if err != nil {
			return utils.TrackError(err)
		}
		if err := admission.AdmitClusterNodePoolsMinorVersionSkew(ctx, clusterNodePools, desiredMinorVersion); err != nil {
			return utils.TrackError(err)
		}
	}

	return nil
}

// shouldDetermineDesiredVersion decides whether the upgrade syncer should compute a desired control
// plane version on this pass. It returns true when ANY of:
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
func (c *controlPlaneVersionSyncer) shouldDetermineDesiredVersion(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster, spc *coreapi.ServiceProviderCluster) (bool, error) {
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
func (c *controlPlaneVersionSyncer) clusterOlderThanGracePeriod(cluster *coreapi.HCPOpenShiftCluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return c.clock.Since(*cluster.SystemData.CreatedAt) > clusterCreateGracePeriod
}

// clusterHasActiveCreateOperation reports whether there is a non-terminal
// Create operation whose ExternalID is the cluster itself. Operations on
// child resources (node pools, external auths) under the cluster are
// ignored on purpose: they don't gate control-plane upgrade selection.
func (c *controlPlaneVersionSyncer) clusterHasActiveCreateOperation(ctx context.Context, cluster *coreapi.HCPOpenShiftCluster) (bool, error) {
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

// listClusterAdmissionNodePools prefetches every node pool that is not in the process of being deleted
// under clusterResourceID paired with its service-provider record, in the same shape that
// frontend.newClusterAdmissionContext builds for cluster admission. The upgrade
// controller passes the result to admission.AdmitClusterNodePoolsMinorVersionSkew
// directly so that admission code stays free of any DB dependency.
//
// A node pool missing its ServiceProviderNodePool is returned as an error: we
// cannot validate minor skew without complete node-pool version data.
// CreateServiceProviderNodePool populates the missing record; ClusterWatchingController
// does not watch the ServiceProviderNodePool informer (a child-document arrival
// doesn't naturally route to its parent cluster), so the retry happens on the
// controller's periodic resync or on the next Cluster / ServiceProviderCluster
// event for this cluster.
func (c *controlPlaneVersionSyncer) listClusterAdmissionNodePools(ctx context.Context, clusterResourceID *azcorearm.ResourceID) ([]admission.ClusterAdmissionNodePool, error) {
	nodePools, err := c.nodePoolLister.ListForCluster(ctx, clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	var clusterNodePools []admission.ClusterAdmissionNodePool
	for _, nodePool := range nodePools {
		// When performing version skew validation, we do not include node pools
		// that are in the process of being deleted.
		if nodePool.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
		serviceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName, clusterResourceID.Name, nodePool.ID.Name)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		clusterNodePools = append(clusterNodePools, admission.ClusterAdmissionNodePool{
			NodePool:                nodePool,
			ServiceProviderNodePool: serviceProviderNodePool,
		})
	}
	return clusterNodePools, nil
}

// nightlyChannelGroup is the OpenShift channel group whose builds are published to the CI
// releasestream API rather than the Cincinnati graph API. selectControlPlaneVersion resolves versions
// via the graph API (api.openshift.com) and therefore cannot serve nightly; SyncOnce rejects it.
const nightlyChannelGroup = "nightly"

// stableChannelGroup is the OpenShift channel group of generally-available stable releases.
const stableChannelGroup = "stable"

// defaultControlPlaneVersionOffset is the offset from the tip of a channel used when selecting a
// desired control plane version. Offset 0 selects the most recent (tip) release in the channel.
const defaultControlPlaneVersionOffset uint = 0

// GetZStreamOffset returns the offset from the tip of a channel to use when selecting a z-stream
// control plane version (offset 0 = the most recent release). The stable channel group uses the
// penultimate release (offset 1) to avoid selecting a brand-new stable z-stream immediately; all
// other channel groups use the tip.
func GetZStreamOffset(channel string) uint {
	if channel == stableChannelGroup {
		return 1
	}
	return defaultControlPlaneVersionOffset
}

// selectControlPlaneVersion resolves the desired control plane version for a minor by querying the
// OpenShift update service (Cincinnati graph API) for that minor's channel and selecting the release
// at the given offset from the tip (offset 0 = most recent release). It bridges
// controlplaneversion.SelectControlPlaneVersion, which returns a configv1.Release, into the
// *semver.Version used throughout this controller.
//
// This must not be called for the nightly channel group: the graph API does not serve nightly
// builds. See the nightly channel guard in SyncOnce.
func selectControlPlaneVersion(ctx context.Context, roundTripper controlplaneversion.RoundTrip, channelGroup string, minorVersion semver.Version, offset uint) (*semver.Version, error) {
	channel := fmt.Sprintf("%s-%d.%d", channelGroup, minorVersion.Major, minorVersion.Minor)
	release, err := controlplaneversion.SelectControlPlaneVersion(ctx, roundTripper, nil, channel, offset)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	selected, err := semver.ParseTolerant(release.Version)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to parse selected control plane version %q for channel %s: %w", release.Version, channel, err))
	}
	return &selected, nil
}

// setIntentFailed records a failed desired control plane version resolution on the controller
// document's IntentFailed condition. resolveErr must be non-nil.
//
//   - For Cincinnati VersionNotFound or any non-Cincinnati resolution error, persist IntentFailed
//     on the controller document and return nil.
//   - Other (transient) Cincinnati errors are returned so the sync is retried.
func setIntentFailed(ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient, key controllerutils.HCPClusterKey, controllerName string, resolveErr error) error {
	logger := utils.LoggerFromContext(ctx)

	// Transient Cincinnati graph or transport errors are returned so the sync is retried; Cincinnati
	// VersionNotFound and any non-Cincinnati resolution error are persisted as IntentFailed.
	var cincinnatiErr *cvocincinnati.Error
	persistIntentFailed := cincinnati.IsCincinnatiVersionNotFoundError(resolveErr) || !errors.As(resolveErr, &cincinnatiErr)
	if !persistIntentFailed {
		return utils.TrackError(resolveErr)
	}

	logger.Error(resolveErr, "desired version resolution failed, persisting IntentFailed condition")
	controllerCRUD := resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
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

// clearIntentFailed sets the controller document's IntentFailed condition to False. It is called
// once a desired control plane version has been resolved and, when applicable, the resolved
// DesiredVersion has been persisted onto the ServiceProviderCluster.
func clearIntentFailed(ctx context.Context, resourcesDBClient corecosmosstorage.ResourcesDBClient, key controllerutils.HCPClusterKey, controllerName string) error {
	controllerCRUD := resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Controllers(key.HCPClusterName)
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
