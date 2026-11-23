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
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	utilsclock "k8s.io/utils/clock"

	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/kubeapplierapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

// clusterCreateGracePeriod is how long after a cluster's CreatedAt we suppress
// automatic upgrade triggering while an active Create operation is still in
// flight. After this window the create is expected to have finished, so resuming
// upgrade triggering is safe.
const clusterCreateGracePeriod = 2 * time.Hour

const controlPlaneVersionLookupTimeout = 30 * time.Second

// triggerControlPlaneUpgradeSyncer is a Cluster syncer that triggers control plane upgrades
type triggerControlPlaneUpgradeSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	cincinnatiClientCache        cincinnati.ClientCache
	ocpImagesACRHostname         string
	activeOperationLister        corelisters.ActiveOperationLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	readDesireLister             kubeapplierlisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*triggerControlPlaneUpgradeSyncer)(nil)

const TriggerControlPlaneUpgradeControllerName = "TriggerControlPlaneUpgrade"

// NewTriggerControlPlaneUpgradeController creates a new controller that triggers control plane upgrades.
// It accepts a desired minor version after checking the mirrored HostedCluster
// conditions, then writes the selected exact release image to the SPC.
func NewTriggerControlPlaneUpgradeController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	clusterLister corelisters.ClusterLister,
	ocpImagesACRHostname string,
	activeOperationLister corelisters.ActiveOperationLister,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &triggerControlPlaneUpgradeSyncer{
		clock:                        clock,
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		cincinnatiClientCache:        cincinnati.NewClientCache(),
		ocpImagesACRHostname:         ocpImagesACRHostname,
		activeOperationLister:        activeOperationLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		readDesireLister:             readDesireLister,
	}

	controller := controllerutils.NewClusterWatchingController(
		TriggerControlPlaneUpgradeControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(coreapi.OperationStatusResourceType, "*")
	controller.AddInterestingResource(kubeapplierapi.ClusterScopedReadDesireResourceType, kubeapplierapi.ReadDesireNameReadonlyHostedCluster)

	return controller
}

// SyncOnce performs a single reconciliation to trigger a control plane upgrade if needed.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. Check if desiredVersion differs from latest actual version
//  3. If different, evaluate upgrade conditions for a new minor
//  4. Store the accepted minor and selected exact release image together
func (c *triggerControlPlaneUpgradeSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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
	if existingCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// ClusterResources cannot apply a release without a Cluster Service cluster.
		return nil
	}

	existingServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	// here we check to see if we should be triggering an upgrade. We do this by
	// 1. if the cluster was created more than two hours ago, then we can run
	// 2. if there is no active operation that is a create, then we can run
	shouldRun, err := c.shouldTriggerUpgrade(ctx, existingCluster)
	if err != nil {
		logger.Error(err, "error determining if control plane upgrade should be triggered")
	} else if !shouldRun {
		logger.Info("Skipping control plane upgrade trigger", "cluster", existingCluster.Name)
		return nil
	}

	desiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if desiredVersion == nil {
		return nil // No desired version set
	}

	// No active version yet (installation ongoing); skip upgrade trigger.
	if len(existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) == 0 {
		return nil
	}

	activeVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	for _, activeVersion := range activeVersions {
		if activeVersion.Version == nil {
			return utils.TrackError(fmt.Errorf("control plane active version is missing"))
		}
	}

	// The mirror stores active versions newest first.
	actualLatestVersion := activeVersions[0].Version

	// If desired version matches latest actual version, nothing to do
	if desiredVersion.EQ(*actualLatestVersion) {
		return nil
	}

	// The lowest observed version preserves the minor gate while an earlier
	// minor upgrade remains in progress.
	sourceVersion, _ := apihelpers.FindLowestAndHighestClusterVersion(activeVersions)
	return c.acceptDesiredRelease(ctx, key, existingCluster, existingServiceProviderCluster, desiredVersion, sourceVersion)
}

// acceptDesiredRelease atomically stores the accepted minor and the image for
// this exact desired version. A later exact-version assignment clears the image
// so the release writer cannot apply an old payload to a new version.
func (c *triggerControlPlaneUpgradeSyncer) acceptDesiredRelease(ctx context.Context, key controllerutils.HCPClusterKey, cluster *coreapi.Cluster, spc *coreapi.ServiceProviderCluster, desiredVersion, sourceVersion *semver.Version) error {
	logger := utils.LoggerFromContext(ctx)
	minor := fmt.Sprintf("%d.%d", desiredVersion.Major, desiredVersion.Minor)
	alreadyAccepted := spc.Spec.ControlPlaneVersion.AcceptedMinorVersion != nil && *spc.Spec.ControlPlaneVersion.AcceptedMinorVersion == minor
	if alreadyAccepted && spc.Spec.DesiredHostedCluster != nil && spc.Spec.DesiredHostedCluster.Spec.Release.Image != "" {
		return nil
	}
	if err := c.checkUpgradeConditions(ctx, key, desiredVersion, sourceVersion, !alreadyAccepted); err != nil {
		return err
	}
	image, channel, err := c.resolveRelease(ctx, key, cluster.CustomerProperties.Version.ChannelGroup, desiredVersion)
	if err != nil {
		return err
	}
	replacement := spc.DeepCopy()
	replacement.Spec.ControlPlaneVersion.AcceptedMinorVersion = &minor
	if replacement.Spec.DesiredHostedCluster == nil {
		replacement.Spec.DesiredHostedCluster = &hsv1beta1.HostedCluster{}
	}
	replacement.Spec.DesiredHostedCluster.Spec.Release.Image = image
	replacement.Spec.DesiredHostedCluster.Spec.Channel = channel
	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		logger.Info("Acceptance write conflicted; waiting for current ServiceProviderCluster")
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	logger.Info("Accepted control plane release", "minor", minor, "desiredVersion", desiredVersion, "releaseImage", image)
	return nil
}

// resolveRelease uses the requested exact version as the Cincinnati current
// release. The first GetUpdates result supplies its multi-architecture payload;
// outgoing edges are irrelevant to the catalog lookup. Nightly releases use
// the CI graph and must be pulled through the service's ACR mirror.
func (c *triggerControlPlaneUpgradeSyncer) resolveRelease(ctx context.Context, key controllerutils.HCPClusterKey, channelGroup string, desiredVersion *semver.Version) (string, string, error) {
	if channelGroup == "" {
		return "", "", utils.TrackError(fmt.Errorf("channel group is missing for control plane version %s", desiredVersion))
	}
	uri, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return "", "", utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for %s: %w", channelGroup, err))
	}
	minor := desiredVersion.Minor
	if channelGroup == "eus" && minor%2 == 1 {
		minor++
	}
	graphChannel := fmt.Sprintf("%s-%d.%d", channelGroup, desiredVersion.Major, minor)
	hostedChannel := graphChannel
	if channelGroup == "nightly" {
		hostedChannel = ""
	}
	clusterUUID, _, err := kubeapplierhelpers.GetCachedHostedClusterUUIDForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		utils.LoggerFromContext(ctx).Info("Cannot read HostedCluster UUID for Cincinnati request", "error", err)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, controlPlaneVersionLookupTimeout)
	defer cancel()
	release, _, _, err := c.cincinnatiClientCache.GetOrCreateClient(clusterUUID).GetUpdates(lookupCtx, uri, "multi", "multi", graphChannel, *desiredVersion)
	if err != nil {
		return "", "", utils.TrackError(fmt.Errorf("failed to resolve control plane release %s in %s: %w", desiredVersion, graphChannel, err))
	}
	if release.Version != desiredVersion.String() {
		return "", "", utils.TrackError(fmt.Errorf("cincinnati returned release %q for control plane version %s", release.Version, desiredVersion))
	}
	if release.Image == "" {
		return "", "", utils.TrackError(fmt.Errorf("release image is missing for control plane version %s", desiredVersion))
	}
	image := release.Image
	if channelGroup == "nightly" && strings.HasPrefix(image, "quay.io/") {
		if c.ocpImagesACRHostname == "" {
			return "", "", utils.TrackError(fmt.Errorf("OCP image ACR hostname is missing for nightly control plane version %s", desiredVersion))
		}
		image = c.ocpImagesACRHostname + strings.TrimPrefix(image, "quay.io")
	}
	return image, hostedChannel, nil
}

// checkUpgradeConditions evaluates the observed HostedCluster conditions
// against the transition being requested, before persisting acceptance.
func (c *triggerControlPlaneUpgradeSyncer) checkUpgradeConditions(ctx context.Context, key controllerutils.HCPClusterKey, desiredVersion, sourceVersion *semver.Version, checkMinor bool) error {
	minorUpgrade, err := isControlPlaneMinorUpgrade(sourceVersion, desiredVersion)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to classify control plane upgrade from %s to %s: %w", sourceVersion, desiredVersion, err))
	}

	// Use cached content even when the latest read failed.
	hostedCluster, _, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to observe HostedCluster for control plane upgrade from %s to %s: %w", sourceVersion, desiredVersion, err))
	}
	if hostedCluster == nil {
		return utils.TrackError(fmt.Errorf("missing HostedCluster observation for control plane upgrade from %s to %s", sourceVersion, desiredVersion))
	}
	decision, err := controlPlaneUpgradeConditions(hostedCluster.Status.Conditions, minorUpgrade && checkMinor)
	if err != nil {
		return utils.TrackError(fmt.Errorf("cannot evaluate control plane upgrade from %s to %s: %w", sourceVersion, desiredVersion, err))
	}
	if !decision.eligible {
		// An expected eligibility wait intentionally uses the existing error path:
		// rate-limited retry and controller Degraded until reconciliation succeeds.
		return utils.TrackError(fmt.Errorf("control plane upgrade from %s to %s blocked by %s: reason=%q, message=%q", sourceVersion, desiredVersion, decision.conditionType, decision.reason, decision.message))
	}
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
// created cluster doesn't have a release update applied while creation is
// still in progress.
func (c *triggerControlPlaneUpgradeSyncer) shouldTriggerUpgrade(ctx context.Context, cluster *coreapi.Cluster) (bool, error) {
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
func (c *triggerControlPlaneUpgradeSyncer) clusterOlderThanGracePeriod(cluster *coreapi.Cluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return c.clock.Since(*cluster.SystemData.CreatedAt) > clusterCreateGracePeriod
}

// clusterHasActiveCreateOperation reports whether there is a non-terminal
// Create operation whose ExternalID is the cluster itself. Operations on
// child resources (node pools, external auths) under the cluster are
// ignored on purpose: they don't gate control-plane upgrade triggering.
func (c *triggerControlPlaneUpgradeSyncer) clusterHasActiveCreateOperation(ctx context.Context, cluster *coreapi.Cluster) (bool, error) {
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
