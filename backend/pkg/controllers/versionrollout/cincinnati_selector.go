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
	"slices"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"

	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/internal/cincinnati"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// cincinnatiBestVersionSelector implements BestVersionSelector on top of the
// Cincinnati upgrade graph. It enumerates the z-streams reachable within a
// channel's minor and picks the one zStreamOffset behind the latest.
type cincinnatiBestVersionSelector struct {
	clientCache cincinnati.ClientCache
}

var _ BestVersionSelector = (*cincinnatiBestVersionSelector)(nil)

// NewCincinnatiBestVersionSelector returns a BestVersionSelector backed by the
// real Cincinnati clients.
func NewCincinnatiBestVersionSelector() BestVersionSelector {
	return &cincinnatiBestVersionSelector{
		clientCache: cincinnati.NewClientCache(),
	}
}

// BestExactVersionForChannel enumerates the reachable z-streams in the channel's
// minor and returns the one zStreamOffset behind the latest. It returns
// (nil, nil) when the graph has no versions for the channel yet.
func (s *cincinnatiBestVersionSelector) BestExactVersionForChannel(ctx context.Context, ystreamChannel string, zStreamOffset int) (*semver.Version, error) {
	channelGroup, minor, ok := parseYStreamChannel(ystreamChannel)
	if !ok {
		return nil, fmt.Errorf("invalid y-stream channel %q", ystreamChannel)
	}
	targetMinor, err := semver.ParseTolerant(minor)
	if err != nil {
		return nil, fmt.Errorf("invalid minor %q in channel %q: %w", minor, ystreamChannel, err)
	}

	// The rollout is fleet-scoped, so there is no single cluster UUID; the UUID
	// is only the Cincinnati client identity, so a fixed nil UUID is fine.
	client := s.clientCache.GetOrCreateClient(uuid.Nil)

	// Seed enumeration from the minor's .0 release, matching the initial-version
	// path in the per-cluster desired-version controller.
	candidates, err := clusterversion.FindAllUpgradeTargetVersionsInMinor(ctx, client, channelGroup, targetMinor, []semver.Version{targetMinor})
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to enumerate versions for channel %q: %w", ystreamChannel, err))
	}
	return selectVersionWithOffset(candidates, zStreamOffset), nil
}

// selectVersionWithOffset sorts candidates newest-first and returns the version
// offset positions behind the latest (clamped to the oldest available). Returns
// nil when there are no candidates. It is a pure function.
func selectVersionWithOffset(candidates []semver.Version, offset int) *semver.Version {
	if len(candidates) == 0 {
		return nil
	}
	sorted := make([]semver.Version, len(candidates))
	copy(sorted, candidates)
	slices.SortFunc(sorted, func(a, b semver.Version) int { return b.Compare(a) })

	idx := offset
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	best := sorted[idx]
	return &best
}
