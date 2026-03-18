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

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/cincinatti"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	versionpkg "github.com/Azure/ARO-HCP/internal/version"
)

// controlPlaneDesiredVersionSyncer is a Cluster syncer that manages control plane desired version.
// It handles automated (managed) z-stream (patch) upgrades and assists with y-stream (minor)
// version upgrades by selecting the appropriate z-stream within the user-desired minor version.
type controlPlaneDesiredVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec

	cincinnatiClientLock      sync.RWMutex
	clusterToCincinnatiClient *lru.Cache
}

var _ controllerutils.ClusterSyncer = (*controlPlaneDesiredVersionSyncer)(nil)

// NewControlPlaneDesiredVersionController creates a new controller that manages the desired
// control plane version. It periodically checks each cluster and sets the desired version
// based on the OCPVersion logic documented in the ServiceProviderCluster type.
// The controller name remains "ControlPlaneVersion" for compatibility.
func NewControlPlaneDesiredVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {

	syncer := &controlPlaneDesiredVersionSyncer{
		cooldownChecker:           controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:              cosmosClient,
		clusterToCincinnatiClient: lru.New(100000),
		clusterServiceClient:      clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"ControlPlaneDesiredVersion",
		cosmosClient,
		informers,
		5*time.Minute, // Check for upgrades every 5 minutes
		syncer,
	)

	return controller
}

func (c *controlPlaneDesiredVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce performs a single reconciliation of the desired control plane version for a given cluster.
//
// High-level flow:
//  1. Fetch the customer's desired cluster configuration and service provider state
//  2. (Active versions are updated by the control plane active version controller.)
//  3. Compute the desired z-stream version based on upgrade logic (initial/z-stream/y-stream)
//  4. If the computed desired version differs from the previously stored desired version:
//     - Update the DesiredVersion field
//  5. Save the updated service provider cluster state
func (c *controlPlaneDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	// TODO bring the cluster uuid into serviceprovidercluster
	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	// Create Cincinnati client with cluster UUID from Cluster Service
	clusterUUID, err := uuid.Parse(clusterServiceCluster.ExternalID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("invalid cluster UUID from Cluster Service: %w", err))
	}
	cincinnatiClient := c.getCincinnatiClient(key, clusterUUID)

	customerDesiredMinor := existingCluster.CustomerProperties.Version.ID
	channelGroup := existingCluster.CustomerProperties.Version.ChannelGroup
	activeVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	desiredVersion, err := c.desiredControlPlaneZVersion(ctx, cincinnatiClient, customerDesiredMinor, channelGroup, activeVersions)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to determine desired control plane version: %w", err))
	}

	// Check if there's a new desired version to set
	if desiredVersion == nil {
		return nil
	}

	// Check if it's the same as the previously set desired version
	previousDesiredVersion := existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if previousDesiredVersion != nil && desiredVersion.EQ(*previousDesiredVersion) {
		return nil
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info("Selected desired version", "desiredVersion", desiredVersion, "previousDesiredVersion", previousDesiredVersion)
	existingServiceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion = desiredVersion
	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// getCincinnatiClient provides a point for unit testing.
// Likely need to provide transport injection for integration testing.
func (c *controlPlaneDesiredVersionSyncer) getCincinnatiClient(key controllerutils.HCPClusterKey, clusterID uuid.UUID) cincinatti.Client {
	// Fast path: check cache with read lock
	c.cincinnatiClientLock.RLock()
	client, ok := c.clusterToCincinnatiClient.Get(key)
	c.cincinnatiClientLock.RUnlock()

	if ok {
		return client.(cincinatti.Client)
	}

	// Slow path: cache miss, need to create
	c.cincinnatiClientLock.Lock()
	defer c.cincinnatiClientLock.Unlock()

	// Double-check: another goroutine might have created it while we waited
	client, ok = c.clusterToCincinnatiClient.Get(key)
	if ok {
		return client.(cincinatti.Client)
	}

	// Create and cache
	client = cincinnati.NewClient(clusterID, http.DefaultTransport.(*http.Transport), "ARO-HCP", cincinatti.NewAlwaysConditionRegistry())
	c.clusterToCincinnatiClient.Add(key, client)
	return client.(cincinatti.Client)
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
//
// customerDesiredMinor and channelGroup are required. If they are not specified, no version is returned.
// Returns nil if no upgrade is needed.
func (c *controlPlaneDesiredVersionSyncer) desiredControlPlaneZVersion(
	ctx context.Context, cincinnatiClient cincinatti.Client,
	customerDesiredMinor string, channelGroup string,
	activeVersions []api.HCPClusterActiveVersion,
) (*semver.Version, error) {
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
		initialVersion, err := versionpkg.ResolveInitialVersion(ctx, cincinnatiClient, channelGroup, customerDesiredMinor)
		if err != nil {
			return nil, utils.TrackError(err)
		}
		return &initialVersion, nil
	}

	// Extract active versions and determine actual minor (if any)
	// Use the most recent version to determine the minor version
	actualLatestVersion := activeVersions[0].Version

	actualMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", actualLatestVersion.Major, actualLatestVersion.Minor))

	// ParseTolerant handles both "4.19", "4.19.0" and full versions like "4.20.15". Normalize to major.minor.0
	// so that same-minor z-stream (e.g. 4.20.0 -> 4.20.15) is not mistaken for a y-stream upgrade.
	parsedDesired := api.Must(semver.ParseTolerant(customerDesiredMinor))
	desiredMinorVersion := semver.MustParse(fmt.Sprintf("%d.%d.0", parsedDesired.Major, parsedDesired.Minor))

	if desiredMinorVersion.LT(actualMinorVersion) {
		return nil, utils.TrackError(fmt.Errorf(
			"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no downgrades",
			actualMinorVersion.String(), desiredMinorVersion.String(),
		))
	}

	if desiredMinorVersion.GT(actualMinorVersion) {
		// TODO: Add support for major version upgrades (e.g., 4.20 → 5.0) when needed
		if desiredMinorVersion.Major != actualMinorVersion.Major {
			return nil, utils.TrackError(fmt.Errorf(
				"invalid next y-stream upgrade path from %s to %s: major version changes are not supported",
				actualMinorVersion.String(), desiredMinorVersion.String(),
			))
		}
		if desiredMinorVersion.Minor != actualMinorVersion.Minor+1 {
			return nil, utils.TrackError(fmt.Errorf(
				"invalid next y-stream upgrade path from %s to %s: only upgrades to the next minor version are allowed, no skipping minor versions",
				actualMinorVersion.String(), desiredMinorVersion.String(),
			))
		}
	}

	targetMinorVersion := desiredMinorVersion

	activeVersionList := make([]semver.Version, 0, len(activeVersions))
	for _, av := range activeVersions {
		activeVersionList = append(activeVersionList, *av.Version)
	}

	if desiredMinorVersion.Minor == actualMinorVersion.Minor+1 {
		logger.Info("Resolving next Y-stream upgrade", "actualMinor", actualMinorVersion.String(), "activeVersions", activeVersions, "channelGroup", channelGroup,
			"targetMinor", desiredMinorVersion.String())

		latestVersion, err := versionpkg.FindLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, desiredMinorVersion, activeVersionList)
		if err != nil {
			return nil, utils.TrackError(err)
		}

		if latestVersion != nil {
			return latestVersion, nil
		}

		// If no upgrade path to target minor, fall back to Z-stream upgrade in current minor
		// This helps the cluster reach a gateway version that may enable the Y-stream upgrade later
		targetMinorVersion = actualMinorVersion
	}

	return versionpkg.FindLatestVersionInMinor(ctx, cincinnatiClient, channelGroup, targetMinorVersion, activeVersionList)
}

