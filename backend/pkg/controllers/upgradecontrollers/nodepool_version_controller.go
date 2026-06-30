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
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// NodepoolVersionControllerName is the Cosmos controller document ID for this syncer.
const NodepoolVersionControllerName = "NodePoolVersion"

// nodePoolVersionSyncer validates the customer's desired NodePool version and
// stores it on the ServiceProviderNodePool. Active-version tracking moved to
// nodePoolActiveVersionSyncer (sourced from the ReadDesire NodePool mirror) and
// is no longer this controller's responsibility.
type nodePoolVersionSyncer struct {
	cooldownChecker               controllerutil.CooldownChecker
	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
	serviceProviderClusterLister  listers.ServiceProviderClusterLister
	subscriptionLister            listers.SubscriptionLister
	readDesireLister              dblisters.ReadDesireLister
	resourcesDBClient             database.ResourcesDBClient

	cincinnatiClientCache cincinnati.ClientCache
}

var _ controllerutils.NodePoolSyncer = (*nodePoolVersionSyncer)(nil)

// NewNodePoolVersionController creates a new syncer that validates and persists
// the customer's desired NodePool version on the ServiceProviderNodePool.
func NewNodePoolVersionController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	subscriptionLister listers.SubscriptionLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &nodePoolVersionSyncer{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		serviceProviderClusterLister:  serviceProviderClusterLister,
		subscriptionLister:            subscriptionLister,
		readDesireLister:              readDesireLister,
		resourcesDBClient:             resourcesDBClient,
		cincinnatiClientCache:         cincinnati.NewClientCache(),
	}

	resyncDuration := 5 * time.Minute
	controller := controllerutils.NewNodePoolWatchingController(
		NodepoolVersionControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		resyncDuration, // Check for upgrades every 5 minutes
		syncer,
	)

	// we need to trigger on serviceProviderCluster changes because we rely on a version field there to make decisions
	serviceProviderClusterInformer, _ := informers.ServiceProviderClusters()
	err := controller.QueueForInformers(resyncDuration, serviceProviderClusterInformer)
	if err != nil {
		panic(err) // coding error
	}

	return controller
}

// NeedsWork reports whether this controller has anything to do for the given
// NodePool. The work this controller does is persist the customer's desired
// version onto the ServiceProviderNodePool, which is needed when:
//   - the NodePool's customer-visible Properties.Version.ID is set, and
//   - the ServiceProviderNodePool's Spec.NodePoolVersion.DesiredVersion does
//     not already equal that value (otherwise nothing would change).
//
// Both arguments must be non-nil; SyncOnce gates the cache miss before calling
// NeedsWork.
func (c *nodePoolVersionSyncer) NeedsWork(nodePool *api.HCPOpenShiftClusterNodePool, serviceProviderNodePool *api.ServiceProviderNodePool, serviceProviderCluster *api.ServiceProviderCluster) bool {
	if len(nodePool.Properties.Version.ID) == 0 {
		return false
	}
	if len(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) == 0 {
		// we need this information to make validation decisions
		return false
	}
	if serviceProviderNodePool.Spec.NodePoolVersion.DesiredVersion == nil {
		return true
	}
	customerDesiredVersion, err := semver.ParseTolerant(nodePool.Properties.Version.ID)
	if err != nil {
		// Unparseable version; let SyncOnce surface the parse error path —
		// we don't suppress work just because we can't parse it here.
		return true
	}
	if customerDesiredVersion.NE(*serviceProviderNodePool.Spec.NodePoolVersion.DesiredVersion) {
		return true
	}

	return false
}

// SyncOnce validates and persists the customer's desired node pool version on
// the ServiceProviderNodePool in Cosmos DB.
//
//   - Reads the customer's desired version from HCPNodePool.Properties.Version.ID.
//   - Validates it against version change constraints (see validateDesiredNodePoolVersion):
//   - Exists as a known version in Cincinnati.
//   - Upgrade: at most +2 minor versions from current, and cannot exceed lowest control plane version.
//   - Downgrade: at most -2 minor versions from the highest control plane version.
//   - Cross-major changes (either direction) require AFEC FeatureExperimentalReleaseFeatures.
//   - NP version must be in the allowed skew map when CP and NP are on different majors.
//   - If valid, stores it in ServiceProviderNodePool.Spec.NodePoolVersion.DesiredVersion.
//
// Active version tracking lives in nodePoolActiveVersionSyncer, which reads the
// Hypershift NodePool from the per-node-pool ReadDesire kubeContent rather than
// round-tripping through Cluster Service.
func (c *nodePoolVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("#### 1a")

	// Do the super cheap cache check first.
	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	logger.Info("#### 1b")
	if database.IsNotFoundError(err) {
		logger.Info("#### 1c")
		// we'll be re-fired if it is created again
		return nil
	}
	logger.Info("#### 1d")
	if err != nil {
		logger.Info("#### 1e")
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	logger.Info("#### 1f")
	// SPNP must be in cache. If a sibling controller hasn't created it yet,
	// skip this sync; the informer will retrigger us when it lands.
	cachedServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	logger.Info("#### 1g")
	if database.IsNotFoundError(err) {
		logger.Info("#### 1h")
		return nil
	}
	logger.Info("#### 1i")
	if err != nil {
		logger.Info("#### 1j")
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}
	logger.Info("#### 1k")

	// Pull the ServiceProviderCluster and Subscription from cache rather than
	// re-fetching live: validation only reads them, and if either isn't yet
	// observed by the informer we'll be retriggered when it lands.
	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	logger.Info("#### 1q")
	if database.IsNotFoundError(err) {
		logger.Info("#### 1r")
		return nil
	}
	logger.Info("#### 1s")
	if err != nil {
		logger.Info("#### 1t")
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}
	logger.Info("#### 1u")

	if !c.NeedsWork(cachedNodePool, cachedServiceProviderNodePool, cachedServiceProviderCluster) {
		logger.Info("#### 1l")
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		return nil
	}
	logger.Info("#### 1m")

	customerDesiredVersion, err := semver.Parse(cachedNodePool.Properties.Version.ID)
	logger.Info("#### 1n")
	if err != nil {
		logger.Info("#### 1o")
		return utils.TrackError(err)
	}
	logger.Info("#### 1p")

	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	logger.Info("#### 1v")
	if database.IsNotFoundError(err) {
		logger.Info("#### 1w")
		return nil
	}
	logger.Info("#### 1x")
	if err != nil {
		logger.Info("#### 1y")
		return utils.TrackError(fmt.Errorf("failed to get Subscription from cache: %w", err))
	}
	logger.Info("#### 1z")

	// Resolve the cluster UUID from the cached HostedCluster so we can build the Cincinnati client.
	// Use it as best effort.  If we cannot find use, use an empty value to make progress without a specific value.
	clusterUUID, found, err := maestrohelpers.GetCachedHostedClusterUUIDForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	logger.Info("#### 1aa")
	if err != nil {
		logger.Info("#### 1ab")
		logger.Info("error getting cluster UUID, continuing with empty", "err", err.Error())
	}
	logger.Info("#### 1ac")
	if !found {
		logger.Info("#### 1ad")
		logger.Info("missing cluster UUID, continuing with empty")
	}
	logger.Info("#### 1ae")

	op := operation.Operation{
		Options: validation.AFECsToValidationOptions(subscription.GetRegisteredFeatures()),
	}
	logger.Info("#### 1af")

	// Validate the customer's desired version before setting it
	err = c.validateDesiredNodePoolVersion(ctx, &customerDesiredVersion, cachedServiceProviderNodePool, cachedServiceProviderCluster, cachedNodePool.Properties.Version.ChannelGroup, clusterUUID,
		op.HasOption(api.FeatureExperimentalReleaseFeatures))
	logger.Info("#### 1ag")
	if err != nil {
		logger.Info("#### 1ah")
		// Persist IntentFailed on the controller document for Cincinnati VersionNotFound or any non-Cincinnati resolution error.
		// Other Cincinnati errors are treated as transient graph or transport issues.
		var cincinnatiErr *cvocincinnati.Error
		logger.Info("#### 1ai")
		persistIntentFailed := cincinnati.IsCincinnatiVersionNotFoundError(err) || !errors.As(err, &cincinnatiErr)
		logger.Info("#### 1aj")
		if persistIntentFailed {
			logger.Info("#### 1ak")
			logger.Error(err, "desired version resolution failed, persisting IntentFailed condition")
			controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
				NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName)
			logger.Info("#### 1al")
			if writeErr := controllerutils.WriteController(ctx, controllerCRUD, NodepoolVersionControllerName, key.InitialController,
				func(ctrl *api.Controller) {
					apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
						Type:    api.ControllerConditionTypeIntentFailed,
						Status:  metav1.ConditionTrue,
						Reason:  api.VersionUpgradeNotAcceptedReason,
						Message: utils.ErrorMessageWithoutLineTracking(err),
					})
				}); writeErr != nil {
				logger.Info("#### 1am")
				return utils.TrackError(writeErr)
			}
			logger.Info("#### 1an")
			return nil
		}
		logger.Info("#### 1ao")
		return utils.TrackError(err)
	}
	logger.Info("#### 1ap")

	// Update the serviceProviderNodePool DesiredVersion
	replacement := cachedServiceProviderNodePool.DeepCopy()
	logger.Info("#### 1aq")
	replacement.Spec.NodePoolVersion.DesiredVersion = &customerDesiredVersion
	logger.Info("#### 1ar")
	_, err = c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName).Replace(ctx, replacement, nil)
	logger.Info("#### 1as")
	if database.IsPreconditionFailedError(err) {
		logger.Info("#### 1at")
		// the cache will update eventually since we're out of date and we'll enter this controller again. No need to fail.
		return nil
	}
	logger.Info("#### 1au")
	if err != nil {
		logger.Info("#### 1av")
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}
	logger.Info("#### 1aw")
	logger.Info("Updated ServiceProviderNodePool with new desired version", "desiredVersion", customerDesiredVersion.String())
	logger.Info("#### 1ax")

	// Clear IntentFailed condition on successful validation
	controllerCRUD := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).
		NodePools(key.HCPClusterName).Controllers(key.HCPNodePoolName)
	logger.Info("#### 1ay")
	if err = controllerutils.WriteController(ctx, controllerCRUD, NodepoolVersionControllerName, key.InitialController,
		func(ctrl *api.Controller) {
			apimeta.SetStatusCondition(&ctrl.Status.Conditions, metav1.Condition{
				Type:    api.ControllerConditionTypeIntentFailed,
				Status:  metav1.ConditionFalse,
				Reason:  api.ControllerConditionReasonAsExpected,
				Message: "",
			})
		}); err != nil {
		logger.Info("#### 1az")
		return utils.TrackError(err)
	}
	logger.Info("#### 1ba")

	return nil
}

func (c *nodePoolVersionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// validateDesiredNodePoolVersion checks that the desired node pool version is a valid change.
// It validates:
//   - The desired version exists in Cincinnati
//   - Upgrade: at most +2 minor versions from current, and cannot exceed lowest control plane version
//   - Downgrade: at most -2 minor versions from the highest control plane version
//   - Cross-major changes (either direction) require AFEC FeatureExperimentalReleaseFeatures
//   - NP version must be in the allowed skew map when CP and NP are on different majors
//
// Cincinnati upgrade-edge validation is intentionally skipped — HCP nodepools use the Replace
// strategy (destroy + recreate), so only version existence matters.
// See https://hypershift.pages.dev/reference/nodepool-rollouts/#upgrade-types
//
// Returns nil if the desired version is valid, or an error describing why it's invalid.
func (c *nodePoolVersionSyncer) validateDesiredNodePoolVersion(ctx context.Context, desiredVersion *semver.Version, spNodePool *api.ServiceProviderNodePool, spCluster *api.ServiceProviderCluster,
	channelGroup string, clusterUUID uuid.UUID, allowExperimentalReleaseFeatures bool) error {
	if desiredVersion == nil {
		return fmt.Errorf("customerDesiredVersion is nil, cannot evaluate upgrade")
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Validating desired nodepool version", "desiredVersion", desiredVersion.String(), "channelGroup", channelGroup)

	// Get all active versions from ServiceProviderNodePool
	nodePoolActiveVersions := spNodePool.Status.NodePoolVersion.ActiveVersions
	lowestCPVersion, highestCPVersion := apihelpers.FindLowestAndHighestClusterVersion(spCluster.Status.ControlPlaneVersion.ActiveVersions)

	if err := validation.ValidateNodePoolVersionChange(*desiredVersion, nodePoolActiveVersions, lowestCPVersion, highestCPVersion, allowExperimentalReleaseFeatures); err != nil {
		return err
	}

	// Validate the desired version exists in Cincinnati (not that an edge exists from the current version).
	if err := c.validateVersionExistsInCincinnati(ctx, desiredVersion, channelGroup, clusterUUID); err != nil {
		return err
	}

	return nil
}

// validateVersionExistsInCincinnati checks that the desired version exists as a node in the
// Cincinnati update graph.
func (c *nodePoolVersionSyncer) validateVersionExistsInCincinnati(
	ctx context.Context,
	version *semver.Version,
	channelGroup string,
	clusterUUID uuid.UUID,
) error {
	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return fmt.Errorf("failed to get Cincinnati URI: %w", err)
	}

	cincinnatiChannel := fmt.Sprintf("%s-%d.%d", channelGroup, version.Major, version.Minor)
	cincinnatiClient := c.cincinnatiClientCache.GetOrCreateClient(clusterUUID)

	// GetUpdates returns VersionNotFound if the version doesn't exist in the channel.
	_, _, _, err = cincinnatiClient.GetUpdates(ctx, cincinnatiURI, "multi", "multi", cincinnatiChannel, *version)
	if err != nil {
		if cincinnati.IsCincinnatiVersionNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("version %s not found in Cincinnati channel %s", version, cincinnatiChannel))
		}
		return utils.TrackError(fmt.Errorf("failed to query Cincinnati for version existence: %w", err))
	}

	return nil
}
