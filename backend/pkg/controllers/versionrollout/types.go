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

// Package versionrollout implements the fleet control plane version rollout
// described in docs/controllers/fleet-control-plane-version-rollout.md and the
// implementation plan alongside it. It contains four controllers:
//
//   - Best Version Selection (per rollout): computes Spec.BestExactVersion for a
//     y-stream channel from the upgrade graph and SRE minimum-version floor.
//   - Status Collector (per rollout): aggregates per-cluster progress into the
//     rollout Status count maps.
//   - Normal Cluster Desired Version Assignment (per rollout): advances eligible
//     clusters toward Spec.BestExactVersion using a canary then rolling strategy,
//     bounded by a failure budget.
//   - Forced Cluster Desired Version Assignment (per cluster): holds an
//     SRE-pinned cluster at its pinned exact version until the fleet best version
//     reaches the pin's release threshold.
//
// The decision logic of every controller is factored into pure functions so it
// can be unit-tested without Cosmos or informers.
package versionrollout

import (
	"context"
	"math/rand"

	"github.com/blang/semver/v4"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
)

// BestVersionSelector returns the upgrade-graph-selected best exact version for a
// y-stream channel, already offset by the channel group's z-stream offset. It
// abstracts the OpenShift update service query so the Best Version Selection
// controller can be tested with a fake. Returning (nil, nil) means the graph has
// no suitable version yet.
type BestVersionSelector interface {
	BestExactVersionForChannel(ctx context.Context, yStreamChannel string) (*semver.Version, error)
}

// ClusterSelector chooses which of the eligible clusters to advance in a canary
// or rolling step. Production uses RandomClusterSelector; tests inject a
// deterministic selector.
type ClusterSelector interface {
	Select(candidates []*coreapi.ServiceProviderCluster, n int) []*coreapi.ServiceProviderCluster
}

// RandomClusterSelector picks n candidates uniformly at random. "For now, this
// will be random. In the future, we can make criteria." (design doc).
type RandomClusterSelector struct{}

// Select returns up to n randomly chosen candidates.
func (RandomClusterSelector) Select(candidates []*coreapi.ServiceProviderCluster, n int) []*coreapi.ServiceProviderCluster {
	if n <= 0 || len(candidates) == 0 {
		return nil
	}
	if n >= len(candidates) {
		out := make([]*coreapi.ServiceProviderCluster, len(candidates))
		copy(out, candidates)
		return out
	}
	perm := rand.Perm(len(candidates))
	out := make([]*coreapi.ServiceProviderCluster, 0, n)
	for _, idx := range perm[:n] {
		out = append(out, candidates[idx])
	}
	return out
}
