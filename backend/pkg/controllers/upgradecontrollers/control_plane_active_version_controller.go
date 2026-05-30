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
	"time"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestrohelpers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	dblisters "github.com/Azure/ARO-HCP/internal/database/listers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneActiveVersionSyncer is a Cluster syncer that updates the control plane active
// versions in ServiceProviderCluster status by reading the version from the per-cluster
// ReadDesire kubeContent (the kube-applier's mirror of the management cluster's HostedCluster).
type controlPlaneActiveVersionSyncer struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*controlPlaneActiveVersionSyncer)(nil)

// NewControlPlaneActiveVersionController creates a new controller that updates
// Status.ControlPlaneVersion.ActiveVersions from the per-cluster ReadDesire's
// observed HostedCluster.
func NewControlPlaneActiveVersionController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &controlPlaneActiveVersionSyncer{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		"ControlPlaneActiveVersions",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *controlPlaneActiveVersionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce updates ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
// from the per-cluster ReadDesire's observed HostedCluster. Each active version
// includes Version and State (Completed or Partial) and is persisted on replace.
func (c *controlPlaneActiveVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	_, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	hostedCluster, err := maestrohelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HostedCluster from ReadDesire: %w", err))
	}
	if hostedCluster == nil {
		// ReadDesire absent or kubeContent not yet observed; retrigger
		// once the kube-applier writes status.
		return nil
	}

	newActiveVersions, err := c.getHostedClusterActiveVersions(ctx, hostedCluster)
	if err != nil {
		return utils.TrackError(err)
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}
	// Use NeedsUpdate (semantic equality) instead of slices.Equal: HCPClusterActiveVersion holds
	// *semver.Version, and Go's `==` (which slices.Equal relies on) compares those pointers, not
	// the represented version. Two independent reads/parses of the same version produce different
	// pointer addresses, which previously caused a Replace on every reconciliation cycle even
	// when the active versions were semantically identical.
	oldActiveVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	if !controllerutil.NeedsUpdate(oldActiveVersions, newActiveVersions) {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", newActiveVersions)
	existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions = newActiveVersions
	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// getHostedClusterActiveVersions derives active versions from HostedCluster version history (newest first).
// Entries with empty or unparseable Version are skipped; State is taken from history (configv1.UpdateState).
// If the latest entry is Completed, return a single version (steady state); otherwise return all versions
// until the last successfully completed one. Each returned entry includes Version and State.
//
// History source: prefer status.controlPlaneVersion.history when non-empty; otherwise fall back to
// status.version.history. ControlPlaneVersionStatus is populated on 4.22+ clusters
// (https://github.com/openshift/enhancements/pull/1950), so we use it where available. Clusters below 4.22
// still rely on status.version.history until Hypershift backports controlPlaneVersion; once that lands,
// the same field will be used automatically when history is present.
func (c *controlPlaneActiveVersionSyncer) getHostedClusterActiveVersions(ctx context.Context, hostedCluster *hsv1beta1.HostedCluster) ([]api.HCPClusterActiveVersion, error) {
	logger := utils.LoggerFromContext(ctx)
	var activeVersions []api.HCPClusterActiveVersion
	// Prefer controlPlaneVersion.history when set.
	// This is available on 4.22+ clusters,  older clusters once Hypershift backports it.
	if len(hostedCluster.Status.ControlPlaneVersion.History) > 0 {
		for _, historyEntry := range hostedCluster.Status.ControlPlaneVersion.History {
			parsedVersion, err := semver.Parse(historyEntry.Version)
			if err != nil {
				logger.Error(err, "Skipping HostedCluster controlPlaneVersion history entry with unparseable version", "history", historyEntry)
				continue
			}
			activeVersions = append(activeVersions, api.HCPClusterActiveVersion{Version: &parsedVersion, State: historyEntry.State})
			if historyEntry.State == configv1.CompletedUpdate {
				return activeVersions, nil
			}
		}
		return activeVersions, nil
	}
	if hostedCluster.Status.Version == nil {
		return activeVersions, nil
	}
	// Pre-4.22 clusters: fall back to status.version.history.
	for _, historyEntry := range hostedCluster.Status.Version.History {
		parsedVersion, err := semver.Parse(historyEntry.Version)
		if err != nil {
			logger.Error(err, "Skipping HostedCluster version history entry with unparseable version", "history", historyEntry)
			continue
		}
		activeVersions = append(activeVersions, api.HCPClusterActiveVersion{Version: &parsedVersion, State: historyEntry.State})
		if historyEntry.State == configv1.CompletedUpdate {
			return activeVersions, nil
		}
	}
	return activeVersions, nil
}
