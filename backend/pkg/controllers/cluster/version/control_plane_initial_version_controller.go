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
	"time"

	"github.com/blang/semver/v4"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneInitialVersionControllerName is the Cosmos controller document ID for this syncer.
const controlPlaneInitialVersionControllerName = "ControlPlaneInitialVersion"

// controlPlaneInitialVersionSyncer seeds the initial control plane desired
// version for a cluster before any active versions exist (Case 1: initial
// install). Once the cluster reports active versions, ongoing z-stream and
// y-stream selection becomes controlPlaneUpgradeVersionSyncer's
// responsibility; this syncer stops acting (see NeedsWork).
type controlPlaneInitialVersionSyncer struct {
	desiredVersionSyncerCommon
}

var _ controllerutils.ClusterSyncer = (*controlPlaneInitialVersionSyncer)(nil)

// NewControlPlaneInitialVersionController creates a controller that selects the
// initial desired control plane version for a freshly created cluster (before
// any active versions are reported). It periodically checks each cluster and
// seeds ServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion.
func NewControlPlaneInitialVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	activeOperationLister corelisters.ActiveOperationLister,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &controlPlaneInitialVersionSyncer{
		desiredVersionSyncerCommon: desiredVersionSyncerCommon{
			clock:                        clock,
			readDesireLister:             readDesireLister,
			resourcesDBClient:            resourcesDBClient,
			activeOperationLister:        activeOperationLister,
			serviceProviderClusterLister: serviceProviderClusterLister,
			cincinnatiClientCache:        cincinnati.NewClientCache(),
			graphClient:                  cincinnati.NewGraphClient(),
		},
	}

	return controllerutils.NewClusterWatchingController(
		controlPlaneInitialVersionControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute, // Check for the initial version every 5 minutes
		syncer,
	)
}

// NeedsWork reports whether this controller applies to the given cluster. The
// initial version controller only acts while there are no active versions yet
// (the initial-install window). Once the cluster reports an active version,
// controlPlaneUpgradeVersionSyncer takes over.
func (c *controlPlaneInitialVersionSyncer) NeedsWork(serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	return serviceProviderCluster != nil && len(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) == 0
}

// SyncOnce seeds the initial desired control plane version for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state.
//  2. Skip unless NeedsWork (no active versions yet).
//  3. Skip while a create is in flight and a desired version is already seeded
//     (shouldDetermineDesiredVersion), so the initial version isn't overwritten
//     while creation is still in progress.
//  4. Resolve the initial desired version for the customer's minor.
//  5. Persist it (monotonically) and clear/raise the IntentFailed condition.
func (c *controlPlaneInitialVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	// Only the initial-install window (no active versions) is our concern; the
	// upgrade controller handles clusters that already report active versions.
	if !c.NeedsWork(cachedServiceProviderCluster) {
		return nil
	}

	// here we check to see if we should be determining the initial version. We do this by
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
	// plane version, use it directly as the desired version and skip
	// Cincinnati/gateway resolution entirely.
	if exact := existingCluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion; exact != nil {
		logger.Info("Using pinned exact control plane version", "exactVersion", exact.String())
		desiredVersion := *exact
		return c.reportDesiredVersionResolution(ctx, key, controlPlaneInitialVersionControllerName, cachedServiceProviderCluster, &desiredVersion, nil)
	}

	cincinnatiClient := c.cincinnatiClientForCluster(ctx, key)

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup

	desiredVersion, resolveErr := c.initialDesiredControlPlaneVersion(ctx, cincinnatiClient, customerDesiredMinor, channelGroup)
	return c.reportDesiredVersionResolution(ctx, key, controlPlaneInitialVersionControllerName, cachedServiceProviderCluster, desiredVersion, resolveErr)
}

// initialDesiredControlPlaneVersion resolves the initial desired z-stream version for a cluster that
// has no active versions yet (Case 1: initial version selection).
//
// customerDesiredMinor and channelGroup are required. If they are not specified, no version is returned.
// Returns nil if no version could be resolved.
func (c *controlPlaneInitialVersionSyncer) initialDesiredControlPlaneVersion(ctx context.Context, cincinnatiClient cincinnati.Client, customerDesiredMinor string, channelGroup string) (*semver.Version, error) {
	logger := utils.LoggerFromContext(ctx)

	if len(customerDesiredMinor) == 0 {
		logger.Info("No desired minor version specified. Terminating version resolution.")
		return nil, nil
	}
	if len(channelGroup) == 0 {
		logger.Info("No channel group specified. Terminating version resolution.")
		return nil, nil
	}

	logger.Info("Resolving initial desired version", "customerDesiredMinor", customerDesiredMinor, "channelGroup", channelGroup)

	// ParseTolerant handles both "4.19" and "4.19.0" formats
	customerDotZeroRelease := metadataapi.Must(semver.ParseTolerant(customerDesiredMinor))

	initialDesiredVersion, err := FindBestVersionInMinor(ctx, cincinnatiClient, c.graphClient, channelGroup, customerDotZeroRelease, []semver.Version{customerDotZeroRelease}, false)
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
