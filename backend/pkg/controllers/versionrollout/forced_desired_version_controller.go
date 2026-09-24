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

package versionrollout

import (
	"context"
	"fmt"
	"time"

	"github.com/blang/semver/v4"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// ForcedClusterDesiredVersionControllerName is the single source of the
// controller name used for metrics, logging, and controller docs.
const ForcedClusterDesiredVersionControllerName = "ForcedClusterDesiredVersion"

// forcedClusterDesiredVersionSyncer implements the Forced Cluster Desired
// Version Assignment controller (design §5.2). It acts on clusters held at an
// authoritative version: for an SRE-set PinnedVersion it holds the cluster at the
// pinned exact version until the fleet's bestExactVersion for the cluster's
// channel reaches the pin's UntilExactVersion, then adopts best and clears the
// pin. For an unpinned cluster whose ServiceProviderProperties.ExperimentalFeatures
// .ControlPlaneExactVersion is set, that exact version is authoritative and the
// cluster is held at it indefinitely. Otherwise, an experimental Immediate
// z-stream update policy advances to the channel's best version independently of
// progressive rollout gates. All other clusters are left to normal assignment.
type forcedClusterDesiredVersionSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
}

var _ controllerutils.ClusterSyncer = (*forcedClusterDesiredVersionSyncer)(nil)

// NewForcedClusterDesiredVersionController wires the per-cluster forced-version
// syncer into a cluster watching controller.
func NewForcedClusterDesiredVersionController(
	clock utilsclock.PassiveClock,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
	rolloutLister fleetlisters.ControlPlaneVersionRolloutLister,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &forcedClusterDesiredVersionSyncer{
		clock:                        clock,
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		rolloutLister:                rolloutLister,
	}
	return controllerutils.NewClusterWatchingController(
		ForcedClusterDesiredVersionControllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		time.Minute,
		syncer,
	)
}

// SyncOnce applies the forced-assignment decision for one cluster.
func (c *forcedClusterDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) (syncErr error) {
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key).WithValues(utils.LogValues{}.AddControllerName(ForcedClusterDesiredVersionControllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting version rollout sync")
	defer func() {
		if syncErr != nil {
			logger.Error(syncErr, "Version rollout sync failed")
		} else {
			logger.Info("Finished version rollout sync")
		}
	}()

	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Skipping sync because watched resource was not found")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Skipping sync because watched resource was not found")
		return nil // the cluster is gone; the ServiceProviderCluster will be cleaned up
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	pin := serviceProviderCluster.Spec.PinnedVersion
	experimentalFeatures := cluster.ServiceProviderProperties.ExperimentalFeatures
	experimentalExactVersion := experimentalFeatures.ControlPlaneExactVersion
	immediate := experimentalFeatures.ZStreamUpdatePolicy == coreapi.ImmediateZStreamUpdatePolicy

	// Only pins, exact overrides, and Immediate updates bypass normal rollout.
	if pin.ExactVersion == nil && experimentalExactVersion == nil && !immediate {
		logger.Info("Leaving desired version to normal rollout assignment; no pin or experimental override")
		return nil
	}

	// Pins use their pinned minor's channel; Immediate uses the desired minor's
	// channel, just like normal z-stream rollout. Exact overrides need no rollout.
	versionForChannel := pin.ExactVersion
	if versionForChannel == nil && experimentalExactVersion == nil && immediate {
		versionForChannel = serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	}
	var best *semver.Version
	if versionForChannel != nil {
		channelGroup := cluster.CustomerProperties.Version.ChannelGroup
		if channelGroup == "" {
			return utils.TrackError(fmt.Errorf("cluster %s has no channel group", key.HCPClusterName))
		}
		yStreamChannel := yStreamChannel(channelGroup, minorString(*versionForChannel))

		rollout, err := c.rolloutLister.Get(ctx, yStreamChannel)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", yStreamChannel, err))
		}
		if cosmosstorageutils.IsNotFoundError(err) {
			logger.Info("Channel rollout is missing; no best version available", "ystreamChannel", yStreamChannel)
		}
		if err == nil {
			best = rollout.Spec.BestExactVersion
		}
	}

	// Pins take precedence over exact overrides, which take precedence over
	// Immediate. Releasing a pin adopts best even if desired is already there.
	desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	var newDesired *semver.Version
	clearPin := false
	switch {
	case pin.ExactVersion != nil:
		newDesired = pin.ExactVersion
		if best != nil && pin.UntilExactVersion != nil && best.GTE(*pin.UntilExactVersion) {
			newDesired = best
			clearPin = true
		}
	case experimentalExactVersion != nil:
		newDesired = experimentalExactVersion
	case immediate && best != nil && desired != nil && minorString(*best) == minorString(*desired) && best.GT(*desired):
		// Production e2e tests must reliably exercise automatic z-stream upgrades
		// even when the fleet's canaries are incomplete or failed. Immediate bypasses
		// progressive rollout gates, but never downgrades or changes the minor.
		// Initial assignment and requested minor upgrades retain their owners.
		newDesired = best
	}
	changed := newDesired != nil && (clearPin || desired == nil || !desired.EQ(*newDesired))
	logger.Info("Computed forced version decision", "currentDesired", versionString(desired), "pinnedVersion", versionString(pin.ExactVersion), "untilVersion", versionString(pin.UntilExactVersion), "experimentalExactVersion", versionString(experimentalExactVersion), "zStreamUpdatePolicy", experimentalFeatures.ZStreamUpdatePolicy, "best", versionString(best), "changed", changed, "clearPin", clearPin, "newDesired", versionString(newDesired))
	if !changed {
		return nil
	}

	replacement := serviceProviderCluster.DeepCopy()
	setDesiredVersion(replacement, newDesired, metav1.Time{Time: c.clock.Now()})
	if clearPin {
		replacement.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{}
	}

	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		utils.LoggerFromContext(ctx).Info("Write conflicted; waiting for informer to provide current resource")
		// Someone else won the race; the informer will re-enqueue with the fresh etag.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	if clearPin {
		logger.Info("Removed cluster version pin", "oldDesiredVersion", versionString(desired), "newDesiredVersion", versionString(newDesired))
	}
	logger.Info("Persisted forced desired version", "desiredVersion", versionString(newDesired), "clearedPin", clearPin)
	return nil
}
