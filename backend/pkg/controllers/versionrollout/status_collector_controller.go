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

	"k8s.io/apimachinery/pkg/api/equality"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// StatusCollectorControllerName is the single source of the controller name.
const StatusCollectorControllerName = "ControlPlaneVersionStatusCollector"

// statusCollectorSyncer implements the Control Plane Version Status Collector
// controller (design §5.3). For one rollout channel it aggregates per-cluster
// desired/achieved progress into the rollout Status count maps.
type statusCollectorSyncer struct {
	clock                        utilsclock.PassiveClock
	rolloutLister                RolloutLister
	rolloutWriter                RolloutWriter
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
	config                       RolloutConfig
}

// NewStatusCollectorSyncer constructs the syncer directly (used by tests).
func NewStatusCollectorSyncer(clock utilsclock.PassiveClock, rolloutLister RolloutLister, rolloutWriter RolloutWriter, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, config RolloutConfig) *statusCollectorSyncer {
	return &statusCollectorSyncer{
		clock:                        clock,
		rolloutLister:                rolloutLister,
		rolloutWriter:                rolloutWriter,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		config:                       config,
	}
}

// NewStatusCollectorController wires the syncer into a rollout watching controller.
func NewStatusCollectorController(fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, clock utilsclock.PassiveClock, config RolloutConfig) controllerutils.Controller {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &statusCollectorSyncer{
		clock:                        clock,
		rolloutLister:                rolloutLister,
		rolloutWriter:                NewFleetRolloutWriter(fleetDBClient),
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		config:                       config,
	}
	return controllerutils.NewControlPlaneVersionRolloutWatchingController(
		StatusCollectorControllerName, fleetInformers, 5*time.Minute, syncer)
}

// CooldownChecker returns nil: the resync interval drives periodic recomputation.
func (c *statusCollectorSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

// rolloutCounts holds the aggregated per-exact-version cluster counts. Every map
// is keyed by exact-version string.
type rolloutCounts struct {
	Desired    map[string]int64
	Mismatched map[string]int64
	Failed     map[string]int64
	Achieved   map[string]int64
	Successful map[string]int64
}

// computeRolloutStatusCounts aggregates the progress of the given clusters as of
// now. It is a pure function.
//
//   - Desired[v]: clusters whose resolved desired version is v.
//   - Achieved[v]: clusters whose earliest active version is v.
//   - Mismatched[v]: clusters desiring v that have not achieved v (upgrade in flight).
//   - Successful[v]: achieved clusters that have held v (HCPClusterActiveVersion.LastTransitionTime)
//     longer than MinVersionReadyDuration.
//   - Failed[v]: mismatched clusters whose DesiredVersionLastTransitionTime is older
//     than MaxUpgradeDuration[minor].
func computeRolloutStatusCounts(serviceProviderClusters []*coreapi.ServiceProviderCluster, config RolloutConfig, now time.Time) rolloutCounts {
	counts := rolloutCounts{
		Desired:    map[string]int64{},
		Mismatched: map[string]int64{},
		Failed:     map[string]int64{},
		Achieved:   map[string]int64{},
		Successful: map[string]int64{},
	}
	for _, serviceProviderCluster := range serviceProviderClusters {
		desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
		achievedEntry := earliestActiveVersionEntry(serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions)

		if desired != nil {
			counts.Desired[desired.String()]++
		}
		if achievedEntry != nil {
			achieved := achievedEntry.Version
			counts.Achieved[achieved.String()]++
			if !achievedEntry.LastTransitionTime.IsZero() && now.Sub(achievedEntry.LastTransitionTime.Time) > config.MinVersionReadyDuration {
				counts.Successful[achieved.String()]++
			}
		}
		if desired != nil && (achievedEntry == nil || !achievedEntry.Version.EQ(*desired)) {
			counts.Mismatched[desired.String()]++
			if maxDur, ok := config.MaxUpgradeDuration[minorString(*desired)]; ok {
				desiredSince := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime
				if desiredSince != nil && !desiredSince.IsZero() && now.Sub(desiredSince.Time) > maxDur {
					counts.Failed[desired.String()]++
				}
			}
		}
	}
	return counts
}

// SyncOnce recomputes the status counts for one rollout channel.
func (c *statusCollectorSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}

	serviceProviderClusters, err := serviceProviderClustersForChannel(ctx, c.serviceProviderClusterLister, c.clusterLister, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}

	counts := computeRolloutStatusCounts(serviceProviderClusters, c.config, c.clock.Now())

	replacement := rollout.DeepCopy()
	replacement.Status.ClusterCountByDesiredExactVersion = nilIfEmpty(counts.Desired)
	replacement.Status.MismatchedClusterCountByDesiredExactVersion = nilIfEmpty(counts.Mismatched)
	replacement.Status.FailedClusterCountByDesiredExactVersion = nilIfEmpty(counts.Failed)
	replacement.Status.ClusterCountByAchievedExactVersion = nilIfEmpty(counts.Achieved)
	replacement.Status.SuccessfulClusterCountByAchievedExactVersion = nilIfEmpty(counts.Successful)

	if equality.Semantic.DeepEqual(rollout, replacement) {
		return nil
	}

	if _, err := c.rolloutWriter.Replace(ctx, replacement, rollout); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}
	return nil
}

// nilIfEmpty returns nil for an empty map so status comparison does not churn on
// nil-vs-empty differences.
func nilIfEmpty(m map[string]int64) map[string]int64 {
	if len(m) == 0 {
		return nil
	}
	return m
}
