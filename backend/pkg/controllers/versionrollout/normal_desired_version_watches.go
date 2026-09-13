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
	"errors"
	"time"

	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// watchVersionCandidates wakes normal assignment for new clusters and channel
// changes without making every status update trigger a channel-wide reconcile.
func (c *normalClusterDesiredVersionSyncer) watchVersionCandidates(clusters, serviceProviderClusters controllerutils.Notifier, queue controllerutils.AfterEnqueuer) error {
	clusterHandler, serviceProviderClusterHandler := c.versionCandidateHandlers(queue)
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(NormalClusterDesiredVersionControllerName)...)
	options := cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(time.Duration(0))}
	_, clusterErr := clusters.AddEventHandlerWithOptions(clusterHandler, options)
	_, serviceProviderClusterErr := serviceProviderClusters.AddEventHandlerWithOptions(serviceProviderClusterHandler, options)
	return errors.Join(clusterErr, serviceProviderClusterErr)
}

func (c *normalClusterDesiredVersionSyncer) versionCandidateHandlers(queue controllerutils.AfterEnqueuer) (cache.ResourceEventHandlerFuncs, cache.ResourceEventHandlerFuncs) {
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(NormalClusterDesiredVersionControllerName)...)
	enqueue := func(cluster *coreapi.HCPOpenShiftCluster, reason string) {
		channel, ok := clusterYStreamChannel(cluster)
		if !ok {
			logger.Info("Cannot enqueue normal assignment: candidate channel is unknown", "resourceID", cluster.ID, "requestedVersion", cluster.CustomerProperties.Version.ID, "channelGroup", cluster.CustomerProperties.Version.ChannelGroup)
			return
		}
		logger.Info("Enqueuing normal assignment for candidate channel", "resourceID", cluster.ID, "ystreamChannel", channel, "reason", reason)
		queue.EnqueueAfter(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}, 0)
	}
	onClusterAdd := func(obj any) {
		if cluster, ok := obj.(*coreapi.HCPOpenShiftCluster); ok {
			enqueue(cluster, "cluster added")
		}
	}
	onServiceProviderCluster := func(obj any) {
		spc, ok := obj.(*coreapi.ServiceProviderCluster)
		if !ok || spc.Spec.ControlPlaneVersion.DesiredVersion != nil {
			return
		}
		subscription, resourceGroup, name, ok := serviceProviderClusterCoords(spc)
		if !ok {
			logger.Info("Cannot enqueue normal assignment: ServiceProviderCluster has no cluster resource ID", "resourceID", spc.ResourceID)
			return
		}
		cluster, err := c.clusterLister.Get(context.Background(), subscription, resourceGroup, name)
		if err != nil {
			logger.Error(err, "Cannot resolve candidate channel for ServiceProviderCluster; cluster events or rollout resync will retry", "resourceID", spc.ResourceID)
			return
		}
		enqueue(cluster, "ServiceProviderCluster has no desired version")
	}
	return cache.ResourceEventHandlerFuncs{
			AddFunc: onClusterAdd,
			UpdateFunc: func(oldObj, newObj any) {
				oldCluster, oldOK := oldObj.(*coreapi.HCPOpenShiftCluster)
				newCluster, newOK := newObj.(*coreapi.HCPOpenShiftCluster)
				if !oldOK || !newOK {
					return
				}
				oldChannel, oldValid := clusterYStreamChannel(oldCluster)
				newChannel, newValid := clusterYStreamChannel(newCluster)
				if newValid && (!oldValid || oldChannel != newChannel) {
					enqueue(newCluster, "cluster candidate channel changed")
				}
			},
		}, cache.ResourceEventHandlerFuncs{
			AddFunc:    onServiceProviderCluster,
			UpdateFunc: func(_, newObj any) { onServiceProviderCluster(newObj) },
		}
}
