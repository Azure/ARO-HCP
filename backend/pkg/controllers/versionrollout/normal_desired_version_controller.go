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

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NormalClusterDesiredVersionControllerName is the single source of the name.
const NormalClusterDesiredVersionControllerName = "NormalClusterDesiredVersion"

// Rollout status condition types written by this controller.
const (
	ConditionProgressing = "Progressing"
	ConditionDegraded    = "Degraded"
)

// Failure-budget constants from the design (§5.5 step 1): abort rollout if more
// than 2 clusters, or more than 5% of the clusters desiring best, have failed.
const (
	failureBudgetAbsolute = int64(2)
	failureBudgetFraction = 0.05
)

// normalClusterDesiredVersionSyncer implements the Normal Cluster Desired Version
// Assignment controller (design §5.5). For one rollout channel it advances a
// bounded set of eligible clusters toward Spec.BestExactVersion using a canary
// then rolling strategy, guarded by the failure budget.
type normalClusterDesiredVersionSyncer struct {
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	rolloutLister                RolloutLister
	rolloutWriter                RolloutWriter
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
	selector                     ClusterSelector
	config                       RolloutConfig
}

// NewNormalClusterDesiredVersionSyncer constructs the syncer directly (used by tests).
func NewNormalClusterDesiredVersionSyncer(resourcesDBClient corecosmosstorage.ResourcesDBClient, rolloutLister RolloutLister, rolloutWriter RolloutWriter, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, selector ClusterSelector, config RolloutConfig) *normalClusterDesiredVersionSyncer {
	return &normalClusterDesiredVersionSyncer{
		resourcesDBClient:            resourcesDBClient,
		rolloutLister:                rolloutLister,
		rolloutWriter:                rolloutWriter,
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		selector:                     selector,
		config:                       config,
	}
}

// NewNormalClusterDesiredVersionController wires the syncer into a rollout
// watching controller. selector defaults to RandomClusterSelector when nil.
func NewNormalClusterDesiredVersionController(resourcesDBClient corecosmosstorage.ResourcesDBClient, fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, selector ClusterSelector, config RolloutConfig) controllerutils.Controller {
	if selector == nil {
		selector = RandomClusterSelector{}
	}
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &normalClusterDesiredVersionSyncer{
		resourcesDBClient:            resourcesDBClient,
		rolloutLister:                rolloutLister,
		rolloutWriter:                NewFleetRolloutWriter(fleetDBClient),
		serviceProviderClusterLister: serviceProviderClusterLister,
		clusterLister:                clusterLister,
		selector:                     selector,
		config:                       config,
	}
	return controllerutils.NewControlPlaneVersionRolloutWatchingController(
		NormalClusterDesiredVersionControllerName, fleetInformers, 5*time.Minute, syncer)
}

// CooldownChecker returns nil: the resync interval drives periodic rollout steps.
func (c *normalClusterDesiredVersionSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return nil
}

// eligibleClusters returns the clusters that may be advanced to best now: those
// below best (or with no desired version yet) that are either unpinned or pinned
// with a release threshold at/under best. It is a pure function.
func eligibleClusters(spcs []*coreapi.ServiceProviderCluster, best semver.Version) []*coreapi.ServiceProviderCluster {
	var eligible []*coreapi.ServiceProviderCluster
	for _, spc := range spcs {
		desired := desiredVersion(spc)
		below := desired == nil || desired.LT(best)
		if !below {
			continue
		}
		pin := spc.Spec.PinnedVersion
		switch {
		case pin == nil || pin.ExactVersion == nil:
			eligible = append(eligible, spc)
		case pin.UntilExactVersion != nil && pin.UntilExactVersion.LTE(best):
			eligible = append(eligible, spc)
		default:
			// pinned and still holding; not eligible
		}
	}
	return eligible
}

// rolloutOutcome enumerates the decisions of the normal rollout logic.
type rolloutOutcome string

const (
	outcomeNoBest      rolloutOutcome = "NoBestVersion"
	outcomeFailure     rolloutOutcome = "Failure"
	outcomeStable      rolloutOutcome = "Stable"
	outcomeCanary      rolloutOutcome = "Canary"
	outcomeProgressing rolloutOutcome = "Progressing"
	outcomeRolling     rolloutOutcome = "Rolling"
)

// rolloutDecisionResult is the outcome of the pure rollout logic.
type rolloutDecisionResult struct {
	Outcome     rolloutOutcome
	SelectCount int
	Message     string
}

// rolloutDecision decides what the rollout should do this round given the
// rollout's best version + status counts, the total number of clusters in the
// channel, and how many are currently eligible to advance. It is a pure function
// mirroring design §5.5.
func rolloutDecision(rollout *fleetapi.ControlPlaneVersionRollout, totalClusters, eligibleCount int, config RolloutConfig) rolloutDecisionResult {
	best := rollout.Spec.BestExactVersion
	if best == nil {
		return rolloutDecisionResult{Outcome: outcomeNoBest, Message: "no best version selected yet"}
	}
	key := best.String()
	status := rollout.Status
	failed := status.FailedClusterCountByDesiredExactVersion[key]
	desiredAtBest := status.ClusterCountByDesiredExactVersion[key]
	mismatched := status.MismatchedClusterCountByDesiredExactVersion[key]
	achieved := status.ClusterCountByAchievedExactVersion[key]
	successful := status.SuccessfulClusterCountByAchievedExactVersion[key]

	// Step 1: failure budget.
	if failed > failureBudgetAbsolute || float64(failed) > failureBudgetFraction*float64(desiredAtBest) {
		return rolloutDecisionResult{
			Outcome: outcomeFailure,
			Message: fmt.Sprintf("%d clusters failed to reach %s, exceeding the failure budget", failed, key),
		}
	}

	// Step 2: eligibility.
	if eligibleCount == 0 {
		return rolloutDecisionResult{Outcome: outcomeStable, Message: fmt.Sprintf("no clusters eligible to advance to %s", key)}
	}

	total := int64(totalClusters)
	inFlightOrDone := mismatched + achieved

	// Step 3: canary — select until in-flight reaches canary% + 2.
	canaryThreshold := percentOfCeil(config.CanaryPercentage, total) + 2
	if inFlightOrDone < canaryThreshold {
		n := clampSelect(canaryThreshold-inFlightOrDone, eligibleCount)
		return rolloutDecisionResult{Outcome: outcomeCanary, SelectCount: n, Message: fmt.Sprintf("selecting %d canary clusters for %s", n, key)}
	}

	// Step 5: canary gate — wait for canary% successful before rolling.
	if successful < percentOfCeil(config.CanaryPercentage, total) {
		return rolloutDecisionResult{Outcome: outcomeProgressing, Message: fmt.Sprintf("waiting for canaries to become successful at %s", key)}
	}

	// Step 6: rolling — select until in-flight reaches rolling%.
	rollingThreshold := percentOfCeil(config.RollingPercentage, total)
	if inFlightOrDone < rollingThreshold {
		n := clampSelect(rollingThreshold-inFlightOrDone, eligibleCount)
		return rolloutDecisionResult{Outcome: outcomeRolling, SelectCount: n, Message: fmt.Sprintf("selecting %d rolling clusters for %s", n, key)}
	}

	return rolloutDecisionResult{Outcome: outcomeProgressing, Message: fmt.Sprintf("rollout to %s in progress", key)}
}

// clampSelect bounds a desired selection count to [0, eligibleCount].
func clampSelect(need int64, eligibleCount int) int {
	if need <= 0 {
		return 0
	}
	if need > int64(eligibleCount) {
		return eligibleCount
	}
	return int(need)
}

// SyncOnce advances clusters for one rollout channel and records the rollout
// condition.
func (c *normalClusterDesiredVersionSyncer) SyncOnce(ctx context.Context, key controllerutils.ControlPlaneVersionRolloutKey) error {
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

	var eligible []*coreapi.ServiceProviderCluster
	if rollout.Spec.BestExactVersion != nil {
		eligible = eligibleClusters(spcs, *rollout.Spec.BestExactVersion)
	}

	decision := rolloutDecision(rollout, len(spcs), len(eligible), c.config)

	if decision.SelectCount > 0 {
		best := *rollout.Spec.BestExactVersion
		for _, spc := range c.selector.Select(eligible, decision.SelectCount) {
			if err := c.assignDesiredVersion(ctx, spc, best); err != nil {
				return utils.TrackError(err)
			}
		}
	}

	return c.recordCondition(ctx, rollout, decision)
}

// assignDesiredVersion sets a single cluster's desired version to best.
func (c *normalClusterDesiredVersionSyncer) assignDesiredVersion(ctx context.Context, spc *coreapi.ServiceProviderCluster, best semver.Version) error {
	sub, rg, cluster, ok := spcClusterCoords(spc)
	if !ok {
		return fmt.Errorf("ServiceProviderCluster is missing a cluster-scoped resource ID")
	}
	replacement := spc.DeepCopy()
	bestCopy := best
	replacement.Spec.ControlPlaneVersion.DesiredVersion = &bestCopy
	if _, err := c.resourcesDBClient.ServiceProviderClusters(sub, rg, cluster).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster %s/%s/%s: %w", sub, rg, cluster, err)
	}
	return nil
}

// recordCondition writes the rollout's Progressing/Degraded conditions from the
// decision, skipping the write when nothing changed.
func (c *normalClusterDesiredVersionSyncer) recordCondition(ctx context.Context, rollout *fleetapi.ControlPlaneVersionRollout, decision rolloutDecisionResult) error {
	replacement := rollout.DeepCopy()

	progressing := metav1.ConditionFalse
	degraded := metav1.ConditionFalse
	switch decision.Outcome {
	case outcomeCanary, outcomeRolling, outcomeProgressing:
		progressing = metav1.ConditionTrue
	case outcomeFailure:
		degraded = metav1.ConditionTrue
	case outcomeStable, outcomeNoBest:
		// both false
	}

	meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    ConditionProgressing,
		Status:  progressing,
		Reason:  string(decision.Outcome),
		Message: decision.Message,
	})
	meta.SetStatusCondition(&replacement.Status.Conditions, metav1.Condition{
		Type:    ConditionDegraded,
		Status:  degraded,
		Reason:  string(decision.Outcome),
		Message: decision.Message,
	})

	if equality.Semantic.DeepEqual(rollout.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}
	if _, err := c.rolloutWriter.Replace(ctx, replacement, rollout); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", rollout.GetStampIdentifier(), err)
	}
	return nil
}

// spcClusterCoords extracts the subscription, resource group, and cluster name
// from a ServiceProviderCluster's resource ID (it is nested under its cluster).
func spcClusterCoords(spc *coreapi.ServiceProviderCluster) (subscription, resourceGroup, cluster string, ok bool) {
	id := spc.ResourceID
	if id == nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" {
		return "", "", "", false
	}
	return id.SubscriptionID, id.ResourceGroupName, id.Parent.Name, true
}
