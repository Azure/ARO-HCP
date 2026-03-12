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
	"slices"
	"time"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneActiveVersionSyncer is a Cluster syncer that updates the control plane active
// versions in ServiceProviderCluster status by querying the version from Cluster Service.
type controlPlaneActiveVersionSyncer struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

var _ controllerutils.ClusterSyncer = (*controlPlaneActiveVersionSyncer)(nil)

// NewControlPlaneActiveVersionController creates a new controller that updates
// Status.ControlPlaneVersion.ActiveVersions from the Cluster Service version.
func NewControlPlaneActiveVersionController(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &controlPlaneActiveVersionSyncer{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	return controllerutils.NewClusterWatchingController(
		"ControlPlaneActiveVersions",
		cosmosClient,
		informers,
		5*time.Minute,
		syncer,
	)
}

func (c *controlPlaneActiveVersionSyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce updates ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
// from the current version reported by Cluster Service.
func (c *controlPlaneActiveVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	clusterServiceCluster, err := c.clusterServiceClient.GetCluster(ctx, existingCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from Cluster Service: %w", err))
	}

	version, ok := clusterServiceCluster.GetVersion()
	if !ok {
		return utils.TrackError(fmt.Errorf("cluster version not found in Cluster Service response"))
	}

	actualVersion := semver.MustParse(version.RawID())

	oldActiveVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions = c.prependActiveVersionIfChanged(oldActiveVersions, actualVersion)

	if slices.Equal(oldActiveVersions, existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions) {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)
	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// prependActiveVersionIfChanged takes a slice of active versions and returns an updated slice
// with the new version prepended if it differs from the most recent version.
// If the most recent version matches the new version, returns the original slice unchanged.
// The returned slice is capped to the 2 most recent versions.
func (c *controlPlaneActiveVersionSyncer) prependActiveVersionIfChanged(currentVersions []api.HCPClusterActiveVersion, newVersion semver.Version) []api.HCPClusterActiveVersion {
	if len(currentVersions) > 0 && currentVersions[0].Version != nil && currentVersions[0].Version.EQ(newVersion) {
		return currentVersions
	}

	newVersions := []api.HCPClusterActiveVersion{{Version: &newVersion}}
	if len(currentVersions) > 0 {
		newVersions = append(newVersions, currentVersions[0])
	}
	return newVersions
}
