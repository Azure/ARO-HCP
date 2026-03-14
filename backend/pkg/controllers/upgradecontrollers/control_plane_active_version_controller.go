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
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"
	hsv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// controlPlaneActiveVersionSyncer is a Cluster syncer that updates the control plane active
// versions in ServiceProviderCluster status by reading the version from the management cluster
// content (HostedCluster status persisted from Maestro readonly bundles).
type controlPlaneActiveVersionSyncer struct {
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient
}

var _ controllerutils.ClusterSyncer = (*controlPlaneActiveVersionSyncer)(nil)

// NewControlPlaneActiveVersionController creates a new controller that updates
// Status.ControlPlaneVersion.ActiveVersions from the management cluster content
// (HostedCluster status stored in ManagementClusterContent).
func NewControlPlaneActiveVersionController(
	cosmosClient database.DBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &controlPlaneActiveVersionSyncer{
		cooldownChecker: controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:    cosmosClient,
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
// from the management cluster content (HostedCluster status). Each active version
// includes Version and State (Completed or Partial) and is persisted on replace.
func (c *controlPlaneActiveVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	_, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	managementClusterContentClient := c.cosmosClient.ManagementClusterContents(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	managementClusterContent, err := managementClusterContentClient.Get(ctx, string(api.MaestroBundleInternalNameReadonlyHypershiftHostedCluster))
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ManagementClusterContent: %w", err))
	}

	if managementClusterContent.Status.KubeContent == nil {
		return nil
	}

	newActiveVersions, err := c.getControlPlaneActiveVersionsFromManagementClusterContent(ctx, managementClusterContent.Status.KubeContent.Items)
	if err != nil {
		return utils.TrackError(err)
	}

	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}
	oldActiveVersions := existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
	if slices.Equal(oldActiveVersions, newActiveVersions) {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Active versions changed", "oldActiveVersions", oldActiveVersions, "newActiveVersions", newActiveVersions)
	existingServiceProviderCluster.Status.ControlPlaneVersion.ActiveVersions = newActiveVersions
	serviceProviderClustersCosmosClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

// getControlPlaneActiveVersionsFromManagementClusterContent reads the HostedCluster content and returns
// the active versions (each with Version and State). Returns nil if no content or no version.
func (c *controlPlaneActiveVersionSyncer) getControlPlaneActiveVersionsFromManagementClusterContent(ctx context.Context, items []runtime.RawExtension) ([]api.HCPClusterActiveVersion, error) {
	hostedCluster, err := c.findHostedClusterInKubeContent(items)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if hostedCluster == nil {
		return nil, utils.TrackError(fmt.Errorf("no HostedCluster found in KubeContent"))
	}
	versions, err := c.getHostedClusterActiveVersions(ctx, hostedCluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	return versions, nil
}

// getHostedClusterActiveVersions derives active versions from HostedCluster status.version.history (newest first).
// Entries with empty or unparseable Version are skipped; State is taken from history (configv1.UpdateState).
// If the latest entry is Completed, return a single version (steady state); otherwise return all versions
// until the last successfully completed one. Each returned entry includes Version and State.
//
// TODO: Once Hypershift exposes HostedCluster.Status.controlPlaneVersion (ControlPlaneVersionStatus) from
// https://github.com/openshift/enhancements/pull/1950, derive active versions from that instead of status.version.history.
func (c *controlPlaneActiveVersionSyncer) getHostedClusterActiveVersions(ctx context.Context, hostedCluster *hsv1beta1.HostedCluster) ([]api.HCPClusterActiveVersion, error) {
	if hostedCluster == nil || hostedCluster.Status.Version == nil {
		return nil, nil
	}
	logger := utils.LoggerFromContext(ctx)
	var activeVersions []api.HCPClusterActiveVersion
	for _, historyEntry := range hostedCluster.Status.Version.History {
		parsedVersion, err := semver.Parse(historyEntry.Version)
		if err != nil {
			logger.Info("Skipping HostedCluster version history entry with unparseable version", "history", historyEntry)
			continue
		}
		activeVersions = append(activeVersions, api.HCPClusterActiveVersion{Version: &parsedVersion, State: historyEntry.State})
		if historyEntry.State == configv1.CompletedUpdate {
			return activeVersions, nil
		}
	}
	return activeVersions, nil
}

// findHostedClusterInKubeContent returns the HostedCluster from KubeContent items by matching
// APIVersion and Kind, then parsing into the typed HostedCluster. Returns nil, nil if none found.
func (c *controlPlaneActiveVersionSyncer) findHostedClusterInKubeContent(items []runtime.RawExtension) (*hsv1beta1.HostedCluster, error) {
	for i := range items {
		hc, err := c.tryParseHostedCluster(&items[i])
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("item %d: %w", i, err))
		}
		if hc != nil {
			return hc, nil
		}
	}
	return nil, nil
}

// tryParseHostedCluster decodes a RawExtension directly into *HostedCluster, then returns it only if Kind and APIVersion match.
// Returns (nil, nil) when the item is not a HostedCluster; (nil, error) on decode failure; (hc, nil) on success.
func (c *controlPlaneActiveVersionSyncer) tryParseHostedCluster(ext *runtime.RawExtension) (*hsv1beta1.HostedCluster, error) {
	if ext == nil {
		return nil, utils.TrackError(fmt.Errorf("nil RawExtension"))
	}
	var raw []byte
	if len(ext.Raw) > 0 {
		raw = ext.Raw
	} else if ext.Object != nil {
		var err error
		raw, err = json.Marshal(ext.Object)
		if err != nil {
			return nil, utils.TrackError(err)
		}
	} else {
		return nil, utils.TrackError(fmt.Errorf("RawExtension has no Object or Raw"))
	}
	hc := &hsv1beta1.HostedCluster{}
	if err := json.Unmarshal(raw, hc); err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to decode HostedCluster: %w", err))
	}
	if hc.APIVersion != "hypershift.openshift.io/v1beta1" || hc.Kind != "HostedCluster" {
		return nil, nil
	}
	return hc, nil
}
