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

package statuscontrollers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"

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

// customerPropertiesVersionStatusSyncer is a Cluster syncer that updates
// ServiceProviderCluster.Status.CustomerPropertiesStatus.Version by reading the
// observed version from the per-cluster ReadDesire's HostedCluster mirror
// (the kube-applier's view of the management cluster's HostedCluster).
type customerPropertiesVersionStatusSyncer struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient
	readDesireLister  dblisters.ReadDesireLister
}

var _ controllerutils.ClusterSyncer = (*customerPropertiesVersionStatusSyncer)(nil)

// NewCustomerPropertiesVersionStatusController creates a controller that
// populates ServiceProviderCluster.Status.CustomerPropertiesStatus.Version
// from the per-cluster ReadDesire's observed HostedCluster.
func NewCustomerPropertiesVersionStatusController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	readDesireLister dblisters.ReadDesireLister,
) controllerutils.Controller {
	syncer := &customerPropertiesVersionStatusSyncer{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		readDesireLister:  readDesireLister,
	}

	return controllerutils.NewClusterWatchingController(
		"CustomerPropertiesVersionStatus",
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		5*time.Minute,
		syncer,
	)
}

func (c *customerPropertiesVersionStatusSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *customerPropertiesVersionStatusSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// TODO, decide how fast we'll allow repeated syncs.  Maybe limit firing to HostedCluster changes?

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
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
		// ReadDesire absent or kubeContent not yet observed; the next
		// ReadDesire event will retrigger this controller.
		return nil
	}

	observedVersionID := latestAttemptedHostedClusterVersion(hostedCluster)
	if observedVersionID == "" {
		// Nothing observable yet — leave the existing status as-is rather than
		// clearing what may already be a meaningful value.
		return nil
	}

	desiredStatusVersion := api.VersionProfileStatus{
		ID: observedVersionID,
		// ChannelGroup is not observable from the HostedCluster; mirror the
		// customer-supplied value from spec so the status surface is complete.
		ChannelGroup: existingCluster.CustomerProperties.Version.ChannelGroup,
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if equality.Semantic.DeepEqual(existingServiceProviderCluster.Status.CustomerPropertiesStatus.Version, desiredStatusVersion) {
		return nil
	}

	logger.Info("CustomerPropertiesStatus.Version changed",
		"old", existingServiceProviderCluster.Status.CustomerPropertiesStatus.Version,
		"new", desiredStatusVersion)
	existingServiceProviderCluster.Status.CustomerPropertiesStatus.Version = desiredStatusVersion

	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if _, err := serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil); err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	return nil
}

// latestAttemptedHostedClusterVersion returns the most recent version string
// the HostedCluster has attempted to install, regardless of whether the
// attempt completed. History is ordered newest-first, so the first entry
// with a non-empty Version wins (Partial or Completed). It prefers
// status.controlPlaneVersion.history (populated on 4.22+ clusters per
// https://github.com/openshift/enhancements/pull/1950) and falls back to
// status.version.history for older clusters. Returns "" if neither source
// has a usable entry, so the caller can decline to overwrite existing status.
func latestAttemptedHostedClusterVersion(hostedCluster *hsv1beta1.HostedCluster) string {
	for _, entry := range hostedCluster.Status.ControlPlaneVersion.History {
		if entry.Version != "" {
			return entry.Version
		}
	}
	if hostedCluster.Status.Version != nil {
		for _, entry := range hostedCluster.Status.Version.History {
			if entry.Version != "" {
				return entry.Version
			}
		}
	}
	return ""
}
