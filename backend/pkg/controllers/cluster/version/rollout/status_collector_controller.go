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

	"k8s.io/apimachinery/pkg/api/equality"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/coreapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/api/fleetapi"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/arohcp-cosmos-client/storage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// StatusCollectorControllerName is the single source of the controller name.
const StatusCollectorControllerName = "ControlPlaneVersionStatusCollector"

// statusCollectorSyncer implements the Control Plane Version Status Collector
// controller (docs/controllers/fleet-control-plane-version-rollout-implementation-plan.md, §5.3). For one rollout channel it aggregates per-cluster
// desired/achieved progress into the rollout Status count maps.
type statusCollectorSyncer struct {
	clock                        utilsclock.PassiveClock
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient                fleetcosmosstorage.FleetDBClient
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
	config                       RolloutConfig
}

// NewStatusCollectorController wires the syncer into a rollout watching controller.
func NewStatusCollectorController(fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, informers coreinformers.BackendInformers, clock utilsclock.PassiveClock, config RolloutConfig) controllerutils.Controller {
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	clusterInformer, clusterLister := informers.Clusters()
	serviceProviderClusterInformer, serviceProviderClusterLister := informers.ServiceProviderClusters()
	syncer := &statusCollectorSyncer{
		clock:                        clock,
		rolloutLister:                rolloutLister,
		fleetDBClient:                fleetDBClient,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		config:                       config,
	}
	controller := controllerutils.NewControlPlaneVersionRolloutWatchingController(
		StatusCollectorControllerName, fleetInformers, 5*time.Minute, syncer)
	controller.AddInterestingResource(fleetapi.ControlPlaneVersionRolloutResourceType, "*")
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)

	if err := syncer.watchStatusInputs(clusterInformer, serviceProviderClusterInformer, controller); err != nil {
		panic(err) // coding error
	}
	return controller
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
//   - Achieved[v]: the upgrade has successfully settled on v, or a downgrade
//     has successfully settled on v. If we are downgrading to a version that
//     was settled previously but the active downgrade has not completed yet
//     (e.g. desired=4.20.34 with [Partial 4.20.36, Completed 4.20.34]), that
//     old completed entry is not achieved until the version shows up as
//     completed again with no Partial ahead of it.
//   - Mismatched[v]: clusters desiring v that have not achieved v (upgrade or
//     downgrade still in flight).
//   - Successful[v]: achieved clusters that have held v (ServiceProviderClusterActiveVersion.LastTransitionTime)
//     longer than MinVersionReadyDuration.
//   - Failed[v]: mismatched clusters whose DesiredVersionLastTransitionTime is older
//     than the minor's max upgrade duration (per-minor override or the 2h default).
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
		activeVersions := serviceProviderCluster.Status.ControlPlaneVersion.ActiveVersions
		achievedEntry := earliestActiveVersionEntry(activeVersions)
		// Downgrade to a previously settled version that has not completed yet
		// (desired matches the old completed base, but a Partial is still ahead).
		rollbackInFlight := desired != nil && achievedEntry != nil && desired.EQ(*achievedEntry.Version) && hasPartialActiveVersion(activeVersions)

		if desired != nil {
			counts.Desired[desired.String()]++
		}
		// Count achieved versions. Achieved means the upgrade has successfully
		// settled, or a downgrade has successfully settled — if we are
		// downgrading to a version that was settled previously but the active
		// downgrade has not completed yet, wait until that version shows up as
		// completed again.
		if achievedEntry != nil && !rollbackInFlight {
			achieved := achievedEntry.Version
			counts.Achieved[achieved.String()]++
			if !achievedEntry.LastTransitionTime.IsZero() && now.Sub(achievedEntry.LastTransitionTime.Time) > config.MinVersionReadyDuration {
				counts.Successful[achieved.String()]++
			}
		}
		// Count in-flight upgrades. In-flight means desired is set but not yet
		// achieved — either because an upgrade is still running, or because a
		// downgrade to a previously settled version has not shown up as
		// completed again.
		if desired != nil && (achievedEntry == nil || !achievedEntry.Version.EQ(*desired) || rollbackInFlight) {
			counts.Mismatched[desired.String()]++
			maxDur := config.MaxUpgradeDurationForMinor(minorString(*desired))
			desiredSince := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersionLastTransitionTime
			if desiredSince != nil && !desiredSince.IsZero() && now.Sub(desiredSince.Time) > maxDur {
				counts.Failed[desired.String()]++
			}
		}
	}
	return counts
}

// SyncOnce recomputes the status counts for one rollout channel.
func (c *statusCollectorSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
	logger := utils.LoggerFromContext(ctx)

	rollout, err := c.rolloutLister.Get(ctx, key.YStreamChannel)
	if cosmosstorageutils.IsNotFoundError(err) {
		logger.Info("Skipping sync because watched resource was not found")
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}

	clusters, err := c.clusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list Clusters: %w", err))
	}
	serviceProviderClusters, err := c.serviceProviderClusterLister.List(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list ServiceProviderClusters: %w", err))
	}
	refs, err := collectRolloutReferences(clusters, serviceProviderClusters)
	if err != nil {
		return utils.TrackError(err)
	}
	matchedServiceProviderClusters, err := filterServiceProviderClustersForChannel(ctx, clusters, serviceProviderClusters, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}
	counts := computeRolloutStatusCounts(matchedServiceProviderClusters, c.config, c.clock.Now())
	logger.Info("Computed rollout status", "clusterCount", len(matchedServiceProviderClusters), "desired", counts.Desired, "mismatched", counts.Mismatched, "failed", counts.Failed, "achieved", counts.Achieved, "successful", counts.Successful)

	replacement := rollout.DeepCopy()
	replacement.Status.ReferencingClusterCount = ptr.To(refs[rollout.Spec.Version])
	replacement.Status.ClusterCountByDesiredExactVersion = nilIfEmpty(counts.Desired)
	replacement.Status.MismatchedClusterCountByDesiredExactVersion = nilIfEmpty(counts.Mismatched)
	replacement.Status.FailedClusterCountByDesiredExactVersion = nilIfEmpty(counts.Failed)
	replacement.Status.ClusterCountByAchievedExactVersion = nilIfEmpty(counts.Achieved)
	replacement.Status.SuccessfulClusterCountByAchievedExactVersion = nilIfEmpty(counts.Successful)

	if equality.Semantic.DeepEqual(rollout, replacement) {
		logger.Info("Rollout status is unchanged")
		return nil
	}

	if _, err := c.fleetDBClient.ControlPlaneVersionRollouts().Replace(ctx, replacement, rollout, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		logger.Info("Write conflicted; waiting for informer to provide current resource")
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", key.YStreamChannel, err))
	}
	logger.Info("Persisted rollout status")
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
