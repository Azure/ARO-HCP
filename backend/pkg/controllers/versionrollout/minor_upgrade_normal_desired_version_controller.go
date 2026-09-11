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
	"github.com/Azure/ARO-HCP/internal/utils"
)

const MinorUpgradeNormalClusterDesiredVersionControllerName = "MinorUpgradeNormalClusterDesiredVersion"

// minorUpgradeNormalClusterDesiredVersionSyncer updates a cluster's desired
// version from its requested channel, without progressive rollout gates or writes
// to ControlPlaneVersionRollout conditions.
type minorUpgradeNormalClusterDesiredVersionSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	clusterLister                corelisters.ClusterLister
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	enqueueAfter                 controllerutils.AfterEnqueuer
}

func NewMinorUpgradeNormalClusterDesiredVersionController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, informers coreinformers.BackendInformers, rolloutLister fleetlisters.ControlPlaneVersionRolloutLister) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &minorUpgradeNormalClusterDesiredVersionSyncer{
		clock: clock, resourcesDBClient: resourcesDBClient, clusterLister: clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister, rolloutLister: rolloutLister,
	}
	controller := controllerutils.NewClusterWatchingController(
		MinorUpgradeNormalClusterDesiredVersionControllerName, resourcesDBClient, informers, nil, time.Minute, syncer)
	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic(fmt.Sprintf("%s controller must implement AfterEnqueuer", MinorUpgradeNormalClusterDesiredVersionControllerName))
	}
	return controller
}

func (c *minorUpgradeNormalClusterDesiredVersionSyncer) NeedsWork(cluster *coreapi.HCPOpenShiftCluster, serviceProviderCluster *coreapi.ServiceProviderCluster) bool {
	desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
	if desired == nil {
		return false
	}
	requested, err := semver.ParseTolerant(cluster.CustomerProperties.Version.ID)
	if err != nil {
		return false
	}
	return minorString(requested) != minorString(*desired)
}

func (c *minorUpgradeNormalClusterDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Syncing minor upgrade desired version")
	serviceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Waiting for ServiceProviderCluster")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	cluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Cluster no longer exists")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster: %w", err))
	}
	channel, ok := clusterYStreamChannel(cluster)
	if !ok {
		return utils.TrackError(fmt.Errorf("cannot determine requested channel: channel group %q, requested version %q", cluster.CustomerProperties.Version.ChannelGroup, cluster.CustomerProperties.Version.ID))
	}
	if !c.NeedsWork(cluster, serviceProviderCluster) {
		return nil
	}
	rollout, err := c.rolloutLister.Get(ctx, channel)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Waiting for rollout", "ystreamChannel", channel, "retryAfter", 10*time.Second)
		c.enqueueAfter.EnqueueAfter(key, 10*time.Second)
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get rollout %q: %w", channel, err))
	}
	if rollout.Spec.BestExactVersion == nil {
		logger.Info("Waiting for best version", "ystreamChannel", channel, "retryAfter", 10*time.Second)
		c.enqueueAfter.EnqueueAfter(key, 10*time.Second)
		return nil
	}
	replacement := serviceProviderCluster.DeepCopy()
	best := *rollout.Spec.BestExactVersion
	setDesiredVersion(replacement, &best, metav1.Time{Time: c.clock.Now()})
	if _, err := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update desired version for minor upgrade: %w", err))
	}
	logger.Info("Updated desired version for minor upgrade", "ystreamChannel", channel, "desiredVersion", best.String())
	return nil
}
