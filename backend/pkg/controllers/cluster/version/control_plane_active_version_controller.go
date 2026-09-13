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
	"slices"
	"time"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/kubeapplierhelpers"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/kubeapplierlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneActiveVersionSyncer is a Cluster syncer that updates the control plane active
// versions in ServiceProviderCluster status by reading the version from the per-cluster
// ReadDesire kubeContent (the kube-applier's mirror of the management cluster's HostedCluster).
type controlPlaneActiveVersionSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	readDesireLister             kubeapplierlisters.ReadDesireLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*controlPlaneActiveVersionSyncer)(nil)

// NewControlPlaneActiveVersionController creates a new controller that updates
// Status.ControlPlaneVersion.ActiveVersions from the per-cluster ReadDesire's
// observed HostedCluster.
func NewControlPlaneActiveVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister kubeapplierlisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &controlPlaneActiveVersionSyncer{
		clock:                        clock,
		resourcesDBClient:            resourcesDBClient,
		readDesireLister:             readDesireLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
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

// SyncOnce updates ServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
// from the per-cluster ReadDesire's observed HostedCluster. Each active version
// includes Version and State (Completed or Partial) and is persisted on replace.
func (c *controlPlaneActiveVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	hostedCluster, err := kubeapplierhelpers.GetCachedHostedClusterForCluster(ctx, c.readDesireLister, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
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

	// Mirror the observed HostedCluster's status.version.desired.channels onto
	// the ServiceProviderCluster. Cluster admission consumes this list to decide
	// whether a requested version.id has a reachable update channel. Doing the
	// mirroring here keeps the frontend (and therefore admission) free of any
	// management-cluster access; see internal/admission/CLAUDE.md.
	newDesiredChannels := getHostedClusterDesiredVersionChannels(hostedCluster)

	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	// Use NeedsUpdate (semantic equality) instead of slices.Equal: HCPClusterActiveVersion holds
	// *semver.Version, and Go's `==` (which slices.Equal relies on) compares those pointers, not
	// the represented version. Two independent reads/parses of the same version produce different
	// pointer addresses, which previously caused a Replace on every reconciliation cycle even
	// when the active versions were semantically identical.
	//
	// DesiredVersionChannels is a plain []string, so slices.Equal compares it by value.
	oldActiveVersions := cachedServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	oldDesiredChannels := cachedServiceProviderCluster.Status.DesiredVersionChannels
	// Stamp LastTransitionTime: preserve the timestamp for a version whose state is
	// unchanged, and set now for a newly-observed (version,state). This keeps the
	// list stable across syncs (no churn) while recording when each version entered
	// its current state, which the fleet rollout uses to judge upgrade readiness.
	newActiveVersions = mergeActiveVersionLastTransitionTimes(oldActiveVersions, newActiveVersions, metav1.Time{Time: c.clock.Now()})
	if !controllerutil.NeedsUpdate(oldActiveVersions, newActiveVersions) && slices.Equal(oldDesiredChannels, newDesiredChannels) {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Active versions or desired channels changed",
		"oldActiveVersions", oldActiveVersions, "newActiveVersions", newActiveVersions,
		"oldDesiredChannels", oldDesiredChannels, "newDesiredChannels", newDesiredChannels)
	replacement := cachedServiceProviderCluster.DeepCopy()
	replacement.Status.ControlPlaneVersion.ActiveVersions = newActiveVersions
	replacement.Status.DesiredVersionChannels = newDesiredChannels
	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
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
func (c *controlPlaneActiveVersionSyncer) getHostedClusterActiveVersions(ctx context.Context, hostedCluster *hsv1beta1.HostedCluster) ([]coreapi.HCPClusterActiveVersion, error) {
	logger := utils.LoggerFromContext(ctx)
	var activeVersions []coreapi.HCPClusterActiveVersion
	// Prefer controlPlaneVersion.history when set.
	// This is available on 4.22+ clusters,  older clusters once Hypershift backports it.
	if len(hostedCluster.Status.ControlPlaneVersion.History) > 0 {
		for _, historyEntry := range hostedCluster.Status.ControlPlaneVersion.History {
			parsedVersion, err := semver.Parse(historyEntry.Version)
			if err != nil {
				logger.Error(err, "Skipping HostedCluster controlPlaneVersion history entry with unparseable version", "history", historyEntry)
				continue
			}
			activeVersions = append(activeVersions, coreapi.HCPClusterActiveVersion{Version: &parsedVersion, State: historyEntry.State})
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
		activeVersions = append(activeVersions, coreapi.HCPClusterActiveVersion{Version: &parsedVersion, State: historyEntry.State})
		if historyEntry.State == configv1.CompletedUpdate {
			return activeVersions, nil
		}
	}
	return activeVersions, nil
}

// mergeActiveVersionLastTransitionTimes stamps LastTransitionTime on each new
// active version: if the same (version, state) was already present it keeps the
// existing timestamp, otherwise it uses now. This records when each version last
// entered its current state without churning the list on every sync.
func mergeActiveVersionLastTransitionTimes(oldVersions, newVersions []coreapi.HCPClusterActiveVersion, now metav1.Time) []coreapi.HCPClusterActiveVersion {
	for i := range newVersions {
		newVersions[i].LastTransitionTime = now
		for _, old := range oldVersions {
			if old.Version != nil && newVersions[i].Version != nil &&
				old.Version.EQ(*newVersions[i].Version) && old.State == newVersions[i].State {
				newVersions[i].LastTransitionTime = old.LastTransitionTime
				break
			}
		}
	}
	return newVersions
}

// getHostedClusterDesiredVersionChannels returns the observed
// status.version.desired.channels of the HostedCluster, or nil when the version
// status has not been reported yet (Status.Version is nil on freshly created
// clusters before the control plane reports a desired release).
//
// A channel only appears in status.version.desired.channels when the cluster's
// current desired release has a valid upgrade edge to a release served by that
// channel. Mirroring this list onto ServiceProviderCluster.Status therefore lets
// DB-free cluster admission reject a version.id change whose target channel is
// not reachable, without the frontend ever reaching the management cluster.
func getHostedClusterDesiredVersionChannels(hostedCluster *hsv1beta1.HostedCluster) []string {
	if hostedCluster.Status.Version == nil {
		return nil
	}
	return hostedCluster.Status.Version.Desired.Channels
}
