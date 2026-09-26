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

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// BestVersionSelectionControllerName is the single source of the controller name.
const BestVersionSelectionControllerName = "ControlPlaneVersionBestVersionSelection"

// bestVersionSelectionSyncer implements the Control Plane Version Best Version
// Selection controller (design §5.4). For a y-stream channel it computes the best
// exact version from the upgrade graph (offset by zStreamOffset) floored by the
// SRE minimum, and stores it on Spec.BestExactVersion.
type bestVersionSelectionSyncer struct {
	rolloutLister fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient fleetcosmosstorage.FleetDBClient
	selector      BestVersionSelector
	config        RolloutConfig
}

// NewBestVersionSelectionController wires the syncer into a rollout watching
// controller that re-selects the best version on the resync interval.
func NewBestVersionSelectionController(fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, selector BestVersionSelector, config RolloutConfig) controllerutils.Controller {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &bestVersionSelectionSyncer{
		rolloutLister: rolloutLister,
		fleetDBClient: fleetDBClient,
		selector:      selector,
		config:        config,
	}
	return controllerutils.NewControlPlaneVersionRolloutWatchingController(
		BestVersionSelectionControllerName, fleetInformers, 5*time.Minute, syncer)
}

// CooldownChecker returns nil: the resync interval drives periodic reselection.
func (c *bestVersionSelectionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

// selectBestExactVersion returns the fleet best exact version: the greater of the
// upgrade-graph best (already offset by zStreamOffset) and the SRE channel
// minimum. Either input may be nil. It is a pure function.
func selectBestExactVersion(graphBest, minimum *semver.Version) *semver.Version {
	return maxVersion(graphBest, minimum)
}

// SyncOnce recomputes Spec.BestExactVersion for one rollout channel.
func (c *bestVersionSelectionSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) (syncErr error) {
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), key).WithValues(utils.LogValues{}.AddControllerName(BestVersionSelectionControllerName)...)
	ctx = utils.ContextWithLogger(ctx, logger)
	logger.Info("Starting version rollout sync")
	defer func() {
		if syncErr != nil {
			logger.Error(syncErr, "Version rollout sync failed")
		} else {
			logger.Info("Finished version rollout sync")
		}
	}()

	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Skipping sync because watched resource was not found")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}

	graphBest, err := c.selector.BestExactVersionForChannel(ctx, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select best version for %q: %w", key.YStreamChannel, err))
	}

	var minimum *semver.Version
	if floor, ok := c.config.MinimumVersions[key.YStreamChannel]; ok {
		minimum = &floor
	}

	best := selectBestExactVersion(graphBest, minimum)
	logger.Info("Selected best version", "graphBest", versionString(graphBest), "minimum", versionString(minimum), "best", versionString(best), "currentBest", versionString(rollout.Spec.BestExactVersion))
	if best == nil {
		logger.Info("Skipping best version update because no version is selectable")
		return nil // nothing selectable yet
	}
	if rollout.Spec.BestExactVersion != nil && rollout.Spec.BestExactVersion.EQ(*best) {
		logger.Info("Best version is unchanged")
		return nil // no change
	}

	replacement := rollout.DeepCopy()
	bestCopy := *best
	replacement.Spec.BestExactVersion = &bestCopy
	if _, err := c.fleetDBClient.ControlPlaneVersionRollouts().Replace(ctx, replacement, rollout, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		utils.LoggerFromContext(ctx).Info("Write conflicted; waiting for informer to provide current resource")
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}
	logger.Info("Persisted best version", "best", versionString(best))
	return nil
}
