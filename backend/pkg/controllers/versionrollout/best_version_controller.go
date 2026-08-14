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

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// BestVersionSelectionControllerName is the single source of the controller name.
const BestVersionSelectionControllerName = "ControlPlaneVersionBestVersionSelection"

// bestVersionSelectionSyncer implements the Control Plane Version Best Version
// Selection controller (design §5.4). For a y-stream channel it computes the best
// exact version from the upgrade graph (offset by zStreamOffset) floored by the
// SRE minimum, and stores it on Spec.BestExactVersion.
type bestVersionSelectionSyncer struct {
	rolloutLister RolloutLister
	rolloutWriter RolloutWriter
	selector      BestVersionSelector
	config        RolloutConfig
}

// NewBestVersionSelectionSyncer constructs the syncer. Wiring it into a fleet
// watching controller (keyed by rollout channel, on an interval) is deferred; see
// the implementation plan.
func NewBestVersionSelectionSyncer(rolloutLister RolloutLister, rolloutWriter RolloutWriter, selector BestVersionSelector, config RolloutConfig) *bestVersionSelectionSyncer {
	return &bestVersionSelectionSyncer{
		rolloutLister: rolloutLister,
		rolloutWriter: rolloutWriter,
		selector:      selector,
		config:        config,
	}
}

// selectBestExactVersion returns the fleet best exact version: the greater of the
// upgrade-graph best (already offset by zStreamOffset) and the SRE channel
// minimum. Either input may be nil. It is a pure function.
func selectBestExactVersion(graphBest, minimum *semver.Version) *semver.Version {
	return maxVersion(graphBest, minimum)
}

// SyncOnce recomputes Spec.BestExactVersion for one rollout channel.
func (c *bestVersionSelectionSyncer) SyncOnce(ctx context.Context, key RolloutKey) error {
	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}

	graphBest, err := c.selector.BestExactVersionForChannel(ctx, key.YStreamChannel, c.config.ZStreamOffset)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to select best version for %q: %w", key.YStreamChannel, err))
	}

	var minimum *semver.Version
	if floor, ok := c.config.MinimumVersions[key.YStreamChannel]; ok {
		minimum = &floor
	}

	best := selectBestExactVersion(graphBest, minimum)
	if best == nil {
		return nil // nothing selectable yet
	}
	if rollout.Spec.BestExactVersion != nil && rollout.Spec.BestExactVersion.EQ(*best) {
		return nil // no change
	}

	replacement := rollout.DeepCopy()
	bestCopy := *best
	replacement.Spec.BestExactVersion = &bestCopy
	if _, err := c.rolloutWriter.Replace(ctx, replacement); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}
	return nil
}
