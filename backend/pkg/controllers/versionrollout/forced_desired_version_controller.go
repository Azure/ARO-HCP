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
// cluster is held at it indefinitely. All other clusters are left to normal
// rollout assignment.
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
func (c *forcedClusterDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // the cluster is gone; the ServiceProviderCluster will be cleaned up
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	pin := serviceProviderCluster.Spec.PinnedVersion
	experimentalExactVersion := cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion

	// This controller only acts on clusters held at an authoritative version: an
	// SRE PinnedVersion or the experimental ControlPlaneExactVersion. Otherwise
	// normal rollout assignment owns the cluster.
	if pin.ExactVersion == nil && experimentalExactVersion == nil {
		return nil
	}

	// The fleet best version is only needed to decide when an SRE pin releases;
	// the experimental exact-version override holds indefinitely and never reads it.
	var best *semver.Version
	if pin.ExactVersion != nil {
		// Resolve the ControlPlaneVersionRollout that governs this pinned cluster
		// (its y-stream channel = <channelGroup>-<pinned minor>) to read bestExactVersion.
		channelGroup := cluster.CustomerProperties.Version.ChannelGroup
		if channelGroup == "" {
			return utils.TrackError(fmt.Errorf("cluster %s has no channel group", key.HCPClusterName))
		}
		yStreamChannel := yStreamChannel(channelGroup, minorString(*pin.ExactVersion))

		rollout, err := c.rolloutLister.Get(ctx, yStreamChannel)
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", yStreamChannel, err))
		}
		if err == nil {
			best = rollout.Spec.BestExactVersion
		}
	}

	// Pins take precedence over exact overrides. Releasing a pin adopts best
	// even if desired is already there.
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
	}
	changed := newDesired != nil && (clearPin || desired == nil || !desired.EQ(*newDesired))
	if !changed {
		return nil
	}

	replacement := serviceProviderCluster.DeepCopy()
	setDesiredVersion(replacement, newDesired, metav1.Time{Time: c.clock.Now()})
	if clearPin {
		replacement.Spec.PinnedVersion = coreapi.ServiceProviderClusterPinnedVersion{}
	}

	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		// Someone else won the race; the informer will re-enqueue with the fresh etag.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}
	if clearPin {
		utils.LoggerFromContext(ctx).Info("Removed cluster version pin", "oldDesiredVersion", versionString(desired), "newDesiredVersion", versionString(newDesired))
	}
	return nil
}
