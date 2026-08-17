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
	rolloutLister                RolloutLister
	rolloutWriter                RolloutWriter
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
	ageSource                    VersionAgeSource
	config                       RolloutConfig
}

// NewStatusCollectorSyncer constructs the syncer directly (used by tests).
func NewStatusCollectorSyncer(rolloutLister RolloutLister, rolloutWriter RolloutWriter, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, ageSource VersionAgeSource, config RolloutConfig) *statusCollectorSyncer {
	return &statusCollectorSyncer{
		rolloutLister:                rolloutLister,
		rolloutWriter:                rolloutWriter,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		ageSource:                    ageSource,
		config:                       config,
	}
}

// NewStatusCollectorController wires the syncer into a rollout watching controller.
func NewStatusCollectorController(fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, ageSource VersionAgeSource, config RolloutConfig) controllerutils.Controller {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &statusCollectorSyncer{
		rolloutLister:                rolloutLister,
		rolloutWriter:                NewFleetRolloutWriter(fleetDBClient),
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		ageSource:                    ageSource,
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

// computeRolloutStatusCounts aggregates the progress of the given clusters. It is
// a pure function.
//
//   - Desired[v]: clusters whose resolved desired version is v.
//   - Achieved[v]: clusters whose earliest active version is v.
//   - Mismatched[v]: clusters desiring v that have not achieved v (upgrade in flight).
//   - Successful[v]: achieved clusters stable at v longer than MinVersionReadyDuration.
//   - Failed[v]: mismatched clusters that have desired v longer than MaxUpgradeDuration[minor].
//
// Failed and Successful require a per-cluster transition age from ageSource; when
// the age is unknown (the default until a timestamp source is wired) those counts
// stay empty.
func computeRolloutStatusCounts(spcs []*coreapi.ServiceProviderCluster, config RolloutConfig, ageSource VersionAgeSource) rolloutCounts {
	counts := rolloutCounts{
		Desired:    map[string]int64{},
		Mismatched: map[string]int64{},
		Failed:     map[string]int64{},
		Achieved:   map[string]int64{},
		Successful: map[string]int64{},
	}
	for _, spc := range spcs {
		desired := desiredVersion(spc)
		achieved := earliestActiveVersion(spc.Status.ControlPlaneVersion.ActiveVersions)

		if desired != nil {
			counts.Desired[desired.String()]++
		}
		if achieved != nil {
			counts.Achieved[achieved.String()]++
			if age, known := ageSource.AchievedAge(spc); known && age > config.MinVersionReadyDuration {
				counts.Successful[achieved.String()]++
			}
		}
		if desired != nil && (achieved == nil || !achieved.EQ(*desired)) {
			counts.Mismatched[desired.String()]++
			if maxDur, ok := config.MaxUpgradeDuration[minorString(*desired)]; ok {
				if age, known := ageSource.MismatchAge(spc); known && age > maxDur {
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

	spcs, err := serviceProviderClustersForChannel(ctx, c.serviceProviderClusterLister, c.clusterLister, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}

	counts := computeRolloutStatusCounts(spcs, c.config, c.ageSource)

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
