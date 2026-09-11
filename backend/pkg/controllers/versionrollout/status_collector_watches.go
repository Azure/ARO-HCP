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

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// statusInputs contains only ServiceProviderCluster fields consumed by membership and status counts.
// Other active-version entries and their metadata do not affect either calculation.
type statusInputs struct {
	Desired      *semver.Version
	DesiredSince *metav1.Time
	Active       *semver.Version
	ActiveSince  metav1.Time
}

func statusInputsFor(serviceProviderCluster *coreapi.ServiceProviderCluster) statusInputs {
	inputs := statusInputs{Desired: serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion, DesiredSince: serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime}
	if active := earliestActiveVersionEntry(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions); active != nil {
		inputs.Active = active.Version
		inputs.ActiveSince = active.LastTransitionTime
	}
	return inputs
}

// enqueueStatusChannels fans out across existing channel groups because a ServiceProviderCluster
// does not carry its cluster's channel group. Unknown membership means all
// rollouts could be affected. Rollout adds and periodic resync cover cache races.
func (c *statusCollectorSyncer) enqueueStatusChannels(queue controllerutils.AfterEnqueuer, minors ...string) {
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(StatusCollectorControllerName)...)
	affected := map[string]bool{}
	for _, minor := range minors {
		affected[minor] = true
	}
	rollouts, err := c.rolloutLister.List(context.Background())
	if err != nil {
		logger.Error(err, "Cannot list affected status channels; rollout resync will retry")
		return
	}
	for _, rollout := range rollouts {
		if rollout.ResourceID == nil {
			continue
		}
		channel := rollout.ResourceID.Name
		_, minor, ok := parseYStreamChannel(channel)
		if ok && (affected[""] || affected[minor]) {
			logger.Info("Enqueuing status collection after input change", "ystreamChannel", channel)
			queue.EnqueueAfter(controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: channel}, 0)
		}
	}
}

func (c *statusCollectorSyncer) watchStatusInputs(clusters, serviceProviderClusters controllerutils.Notifier, queue controllerutils.AfterEnqueuer) error {
	logger := utils.DefaultLogger().WithValues(utils.LogValues{}.AddControllerName(StatusCollectorControllerName)...)
	options := cache.HandlerOptions{Logger: &logger, ResyncPeriod: ptr.To(time.Duration(0))}
	serviceProviderClusterMinor := func(serviceProviderCluster *coreapi.ServiceProviderCluster) string {
		// Without an active or desired version, the requested-version fallback is
		// unknown from this event. Conservatively enqueue every existing rollout.
		minor, _ := clusterMinor(serviceProviderCluster, nil)
		return minor
	}
	onServiceProviderCluster := func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		if serviceProviderCluster, ok := obj.(*coreapi.ServiceProviderCluster); ok {
			c.enqueueStatusChannels(queue, serviceProviderClusterMinor(serviceProviderCluster))
		}
	}
	// Cluster membership can use the ServiceProviderCluster's active/desired minor instead of the
	// requested minor, so cluster events conservatively affect all rollout minors.
	onCluster := func(obj any) {
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = tombstone.Obj
		}
		if _, ok := obj.(*coreapi.HCPOpenShiftCluster); ok {
			c.enqueueStatusChannels(queue, "")
		}
	}
	_, serviceProviderClusterErr := serviceProviderClusters.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc: onServiceProviderCluster, DeleteFunc: onServiceProviderCluster,
		UpdateFunc: func(oldObj, newObj any) {
			oldServiceProviderCluster, oldOK := oldObj.(*coreapi.ServiceProviderCluster)
			newServiceProviderCluster, newOK := newObj.(*coreapi.ServiceProviderCluster)
			if oldOK && newOK && !equality.Semantic.DeepEqual(statusInputsFor(oldServiceProviderCluster), statusInputsFor(newServiceProviderCluster)) {
				c.enqueueStatusChannels(queue, serviceProviderClusterMinor(oldServiceProviderCluster), serviceProviderClusterMinor(newServiceProviderCluster))
			}
		},
	}, options)
	_, clusterErr := clusters.AddEventHandlerWithOptions(cache.ResourceEventHandlerFuncs{
		AddFunc: onCluster, DeleteFunc: onCluster,
		UpdateFunc: func(oldObj, newObj any) {
			oldCluster, oldOK := oldObj.(*coreapi.HCPOpenShiftCluster)
			newCluster, newOK := newObj.(*coreapi.HCPOpenShiftCluster)
			if !oldOK || !newOK {
				return
			}
			oldMinor, _ := clusterMinor(&coreapi.ServiceProviderCluster{}, oldCluster)
			newMinor, _ := clusterMinor(&coreapi.ServiceProviderCluster{}, newCluster)
			if oldMinor != newMinor || oldCluster.CustomerProperties.Version.ChannelGroup != newCluster.CustomerProperties.Version.ChannelGroup {
				c.enqueueStatusChannels(queue, "")
			}
		},
	}, options)
	return errors.Join(serviceProviderClusterErr, clusterErr)
}
