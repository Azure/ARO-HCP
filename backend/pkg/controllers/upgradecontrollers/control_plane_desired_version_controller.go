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
	"net/http"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	cvocincinnati "github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/hcpversionselection"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// clusterCreateGracePeriod is how long after a cluster's CreatedAt we
// suppress automatic desired-version recomputation while an active Create
// operation is still in flight. After this window the create is expected
// to have finished, so resuming z-stream selection is safe.
const clusterCreateGracePeriod = 2 * time.Hour

// controlPlaneDesiredVersionControllerName is the Cosmos controller document ID for this syncer.
const controlPlaneDesiredVersionControllerName = "ControlPlaneDesiredVersion"

// controlPlaneDesiredVersionSyncer is a Cluster syncer that manages control plane desired version.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneDesiredVersionSyncer struct {
	clock                 utilsclock.PassiveClock
	readDesireLister      dblisters.ReadDesireLister
	resourcesDBClient     database.ResourcesDBClient
	clusterServiceClient  ocm.ClusterServiceClientSpec
	subscriptionLister    listers.SubscriptionLister
	activeOperationLister listers.ActiveOperationLister

	cincinnatiClient cincinnati.Client
}

var _ controllerutils.ClusterSyncer = (*controlPlaneDesiredVersionSyncer)(nil)

// NewControlPlaneDesiredVersionController creates a new controller that manages the desired
// control plane version. It periodically checks each cluster and sets the desired version
// based on the OCPVersion logic documented in the ServiceProviderCluster type.
func NewControlPlaneDesiredVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
	subscriptionLister listers.SubscriptionLister,
) controllerutils.Controller {
	syncer := &controlPlaneDesiredVersionSyncer{
		clock:            clock,
		readDesireLister: readDesireLister,
		cincinnatiClient: cincinnati.NewCachingClient(
			cvocincinnati.NewClient(uuid.Nil, http.DefaultTransport.(*http.Transport).Clone(), "ARO-HCP", cincinnati.NewAlwaysConditionRegistry()),
			clock, 1*time.Hour,
		),
		resourcesDBClient:     resourcesDBClient,
		clusterServiceClient:  clusterServiceClient,
		subscriptionLister:    subscriptionLister,
		activeOperationLister: activeOperationLister,
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

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// here we check to see if we should be determining upgrade versions. We do this by
	// 1. if existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion is empty, then we must run so we can fill it in.
	// 2. if the cluster was created more than two hours ago, then we can run
	// 3. if there is no active operation that is a create, then we can run
	shouldRun, err := c.shouldDetermineDesiredVersion(ctx, existingCluster, existingServiceProviderCluster)
	if err != nil {
		logger.Error(err, "error determining if desired version should be determined")
	} else if !shouldRun {
		return nil
	}

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		logger.Info("error getting cached HostedCluster, continuing without it", "err", err.Error())
	}

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup
	activeVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	subscription, err := c.subscriptionLister.Get(ctx, key.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}
	operation := operation.Operation{
		Type:    operation.Update,
		Options: validation.AFECsToValidationOptions(subscription.GetRegisteredFeatures()),
	}
	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, c.cincinnatiClient, key.GetResourceID(), customerDesiredMinor, channelGroup, activeVersions,
		operation.HasOption(api.FeatureExperimentalReleaseFeatures), hostedCluster)

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

	previousDesiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	// Only advance stored desired when the newly resolved version is greater, so graph changes
	// cannot automatically select a lower z-stream. When rollback support is added, relax this
	// so that only SRE-enforced rollback targets can decrease desired.
	if desiredVersion != nil && (previousDesiredVersion == nil || desiredVersion.GT(*previousDesiredVersion)) {
		logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)
		// on successful resolution of the desired version.
		// update the ServiceProviderCluster first and only afterwards
		// clear the IntentFailed condition
		replacement := existingServiceProviderCluster.DeepCopy()
		replacement.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err := serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
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
// It validates upgrade constraints (downgrade prevention, minor skew, AFEC gating,
// node pool version skew) then delegates version selection to
// hcpversionselection.SelectControlPlaneVersion, which performs transitive
// gateway chain validation through all subsequent minors.
//
// customerDesiredMinor and channelGroup are required. If they are not specified, no version is returned.
// Returns nil if no upgrade is needed.
func (c *controlPlaneDesiredVersionSyncer) desiredControlPlaneZVersion(ctx context.Context, cincinnatiClient cincinnati.Client, clusterResourceID *azcorearm.ResourceID,
	customerDesiredMinor string, channelGroup string, activeVersions []api.HCPClusterActiveVersion, allowExperimentalReleaseFeatures bool, hostedCluster *v1beta1.HostedCluster) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)

	if len(customerDesiredMinor) == 0 {
		logger.Info("No desired minor version specified. Terminating version resolution.")
		return nil, nil
	}
	if len(channelGroup) == 0 {
		logger.Info("No channel group specified. Terminating version resolution.")
		return nil, nil
	}

	cincinnatiURI, err := cincinnati.GetCincinnatiURI(channelGroup)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to get Cincinnati URI for channel %s: %w", channelGroup, err))
	}

	parsedDesired := api.Must(semver.ParseTolerant(customerDesiredMinor))
	desiredMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", parsedDesired.Major, parsedDesired.Minor))

	if len(activeVersions) == 0 {
		logger.Info("Resolving initial desired version", "customerDesiredMinor", customerDesiredMinor, "channelGroup", channelGroup)
		return hcpversionselection.SelectControlPlaneVersion(ctx, channelGroup, desiredMinorVersion, cincinnatiURI, cincinnatiClient, nil)
	}

	actualLatestVersion := activeVersions[0].Version
	actualLatestMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

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

	desiredVersion, err := hcpversionselection.SelectControlPlaneVersion(ctx, channelGroup, desiredMinorVersion, cincinnatiURI, cincinnatiClient, hostedCluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if desiredVersion != nil {
		return desiredVersion, nil
	}

	if desiredMinorVersion.GT(actualLatestMinorVersion) {
		logger.Info("No path to target minor, falling back to current minor",
			"targetMinor", desiredMinorVersion.String(), "currentMinor", actualLatestMinorVersion.String())
		return hcpversionselection.SelectControlPlaneVersion(ctx, channelGroup, actualLatestMinorVersion, cincinnatiURI, cincinnatiClient, hostedCluster)
	}

	return nil, nil
}

// listClusterAdmissionNodePools prefetches every node pool that is not in the process of being deleted
// under clusterResourceID paired with its service-provider record, in the same shape that
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
		// When performing version skew validation, we do not include node pools
		// that are in the process of being deleted.
		if nodePool.ServiceProviderProperties.DeletionTimestamp != nil {
			continue
		}
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

// shouldDetermineDesiredVersion decides whether the syncer should compute a
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
func (c *controlPlaneDesiredVersionSyncer) shouldDetermineDesiredVersion(ctx context.Context, cluster *api.HCPOpenShiftCluster, spc *api.ServiceProviderCluster) (bool, error) {
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
func (c *controlPlaneDesiredVersionSyncer) clusterOlderThanGracePeriod(cluster *api.HCPOpenShiftCluster) bool {
	if cluster.SystemData == nil || cluster.SystemData.CreatedAt == nil {
		return true
	}
	return c.clock.Since(*cluster.SystemData.CreatedAt) > clusterCreateGracePeriod
}

// hasActiveClusterCreateOperation reports whether there is a non-terminal
// Create operation whose ExternalID is the cluster itself. Operations on
// child resources (node pools, external auths) under the cluster are
// ignored on purpose: they don't gate control-plane upgrade selection.
func (c *controlPlaneDesiredVersionSyncer) clusterHasActiveCreateOperation(ctx context.Context, cluster *api.HCPOpenShiftCluster) (bool, error) {
	logger := utils.LoggerFromContext(ctx)
	if len(cluster.ServiceProviderProperties.ActiveOperationID) == 0 {
		logger.Info("Cluster has no active create operation", "cluster", cluster.Name)
		return false, nil
	}
	operation, err := c.activeOperationLister.Get(ctx, cluster.ResourceID.SubscriptionID, cluster.ServiceProviderProperties.ActiveOperationID)
	if err != nil {
		return false, fmt.Errorf("failed to get operations %q for cluster: %w", cluster.ServiceProviderProperties.ActiveOperationID, err)
	}
	if operation.Request != database.OperationRequestCreate {
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
