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

package rollout

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/blang/semver/v4"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/metadataapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/fleetapihelpers"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const CincinnatiSeedingControllerName = "ControlPlaneVersionCincinnatiSeeding"

type cincinnatiSeedingController struct {
	*controllerutil.GenericWatchingController[controllerutils.ControlPlaneVersionRolloutKey]
	discover func(context.Context) error
}

type cincinnatiSeedingSyncer struct {
	fleetDBClient fleetcosmosstorage.FleetDBClient
	rolloutLister fleetlisters.ControlPlaneVersionRolloutLister
	queue         controllerutil.Enqueuer
	discovery     cincinnati.VersionProfileSource
}

// NewControlPlaneVersionCincinnatiSeedingController seeds prospective rollouts
// from Cincinnati while preserving existing rollout state.
func NewControlPlaneVersionCincinnatiSeedingController(
	fleetDBClient fleetcosmosstorage.FleetDBClient,
	fleetInformers fleetinformers.FleetInformers,
	discovery cincinnati.VersionProfileSource,
) controllerregistry.Runnable {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &cincinnatiSeedingSyncer{
		fleetDBClient: fleetDBClient,
		rolloutLister: rolloutLister,
		discovery:     discovery,
	}
	controller := &cincinnatiSeedingController{
		GenericWatchingController: controllerutils.NewControlPlaneVersionRolloutWatchingController(CincinnatiSeedingControllerName, fleetInformers, 5*time.Minute, syncer),
		discover:                  syncer.discover,
	}
	syncer.queue = controller
	return controller
}

func (c *cincinnatiSeedingController) Run(ctx context.Context, workers int) {
	defer utilruntime.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	var discovery sync.WaitGroup
	defer discovery.Wait()
	defer cancel()
	if !c.WaitForCacheSync(ctx) {
		utilruntime.HandleErrorWithContext(ctx, fmt.Errorf("%s cache sync failed", CincinnatiSeedingControllerName), "Unable to start rollout discovery")
		c.GenericWatchingController.Run(ctx, workers)
		return
	}
	discoveryCtx := utils.ContextWithControllerName(ctx, CincinnatiSeedingControllerName)
	discoveryCtx = utils.ContextWithLogger(discoveryCtx, utils.LoggerFromContext(ctx).WithValues(utils.LogValues{}.AddControllerName(CincinnatiSeedingControllerName)...))
	discovery.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer discovery.Done()
		wait.JitterUntilWithContext(discoveryCtx, func(ctx context.Context) {
			if err := c.discover(ctx); err != nil {
				utilruntime.HandleErrorWithContext(ctx, err, "Rollout discovery failed; retrying on next interval")
			}
		}, 5*time.Minute, 0.1, true)
	}()
	c.GenericWatchingController.Run(ctx, workers)
}

func (c *cincinnatiSeedingSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	profile, err := fleetapihelpers.RolloutVersionFromName(key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}
	// Apply the current backend version floor to queued events.
	if !atLeastBackendVersion(profile.ID) {
		return nil
	}
	_, err = c.rolloutLister.Get(ctx, key.YStreamChannel)
	if err == nil {
		return nil
	}
	if !cosmosstorageutils.IsNotFoundError(err) {
		return utils.TrackError(err)
	}
	desired, err := newControlPlaneVersionRollout(key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}
	crud := c.fleetDBClient.ControlPlaneVersionRollouts()
	if _, err := crud.Create(ctx, desired, nil); err != nil && !cosmosstorageutils.IsConflictError(err) {
		return utils.TrackError(fmt.Errorf("create rollout %s: %w", key.YStreamChannel, err))
	}
	return nil
}

func discoverChannels(profiles []coreapi.VersionProfile) ([]controllerutils.ControlPlaneVersionRolloutKey, error) {
	eligible := sets.New[coreapi.VersionProfile]()
	for _, profile := range profiles {
		if !metadataapi.AllowedChannelGroupsWithExperimentalFlag.Has(profile.ChannelGroup) {
			return nil, utils.TrackError(fmt.Errorf("unsupported channel group %q", profile.ChannelGroup))
		}
		version, err := semver.ParseTolerant(profile.ID)
		if err != nil {
			return nil, utils.TrackError(fmt.Errorf("invalid version %q: %w", profile.ID, err))
		}
		profile.ID = fmt.Sprintf("%d.%d", version.Major, version.Minor)
		if atLeastBackendVersion(profile.ID) {
			eligible.Insert(profile)
		}
	}
	profiles = eligible.UnsortedList()
	slices.SortFunc(profiles, func(a, b coreapi.VersionProfile) int {
		if order := cmp.Compare(a.ChannelGroup, b.ChannelGroup); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	var keys []controllerutils.ControlPlaneVersionRolloutKey
	for _, profile := range profiles {
		keys = append(keys, controllerutils.ControlPlaneVersionRolloutKey{YStreamChannel: profile.ChannelGroup + "-" + profile.ID})
	}
	return keys, nil
}

func (c *cincinnatiSeedingSyncer) discover(ctx context.Context) error {
	profiles, err := c.discovery.VersionProfiles(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	keys, err := discoverChannels(profiles)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, key := range keys {
		c.queue.Enqueue(key)
	}
	return nil
}
