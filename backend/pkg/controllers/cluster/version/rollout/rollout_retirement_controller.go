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
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/controllerregistry"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const RolloutRetirementControllerName = "ControlPlaneVersionRolloutRetirement"

type rolloutRetirementSyncer struct {
	rolloutLister fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient fleetcosmosstorage.FleetDBClient
}

// NewControlPlaneVersionRolloutRetirementController reclaims obsolete internal
// version state after a supported minor version has been retired and its clusters
// have drained. Supported channels remain available for future cluster creation,
// including those with zero membership.
//
// Rollouts below the backend support floor qualify for retirement once the status
// collector reports zero referencing clusters.
func NewControlPlaneVersionRolloutRetirementController(fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers) controllerregistry.Runnable {
	_, rollouts := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &rolloutRetirementSyncer{rolloutLister: rollouts, fleetDBClient: fleetDBClient}
	return controllerutils.NewControlPlaneVersionRolloutWatchingController(
		RolloutRetirementControllerName, fleetInformers, 5*time.Minute, syncer)
}

func (c *rolloutRetirementSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("get rollout %q for retirement: %w", key.YStreamChannel, err))
	}
	if reconcileRolloutRetirement(rollout) {
		err := c.fleetDBClient.ControlPlaneVersionRollouts().DeleteWithETag(ctx, key.YStreamChannel, rollout.CosmosETag)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// A newer rollout must be evaluated when its watch event arrives.
			return nil
		}
		if err != nil && !cosmosstorageutils.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("retire rollout %q: %w", key.YStreamChannel, err))
		}
	}
	return nil
}

// reconcileRolloutRetirement retires a target below the backend support floor
// once the status collector reports zero referencing clusters.
func reconcileRolloutRetirement(rollout *fleetapi.ControlPlaneVersionRollout) bool {
	profile := rollout.Spec.Version
	if profile == (coreapi.VersionProfile{}) {
		return false
	}
	if atLeastBackendVersion(profile.ID) {
		return false
	}
	return rollout.Status.ReferencingClusterCount != nil && *rollout.Status.ReferencingClusterCount == 0
}
