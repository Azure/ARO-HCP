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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/database/listers/fleetlisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// NormalClusterDesiredVersionControllerName is the single source of the name.
const NormalClusterDesiredVersionControllerName = "NormalClusterDesiredVersion"

// Rollout status condition types written by this controller.
const (
	ConditionProgressing = "Progressing"
	ConditionDegraded    = "Degraded"
)

// Failure-budget constants from the design (§5.5 step 1): abort the rollout when
// the number of failed clusters exceeds the budget. The budget is the larger of
// failureBudgetAbsolute and failureBudgetFraction of the clusters desiring best;
// the absolute value is a floor so small channels tolerate a couple of failures
// before the fraction would.
const (
	failureBudgetAbsolute = int64(2)
	failureBudgetFraction = 0.05
)

// normalClusterDesiredVersionSyncer implements the Normal Cluster Desired Version
// Assignment controller (design §5.5). For one rollout channel it advances a
// bounded set of eligible clusters toward Spec.BestExactVersion using a canary
// then rolling strategy, guarded by the failure budget.
type normalClusterDesiredVersionSyncer struct {
	clock                        utilsclock.PassiveClock
	resourcesDBClient            corecosmosstorage.ResourcesDBClient
	rolloutLister                fleetlisters.ControlPlaneVersionRolloutLister
	fleetDBClient                fleetcosmosstorage.FleetDBClient
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister
	clusterLister                corelisters.ClusterLister
	selector                     ClusterSelector
	config                       RolloutConfig
}

// NewNormalClusterDesiredVersionController wires the syncer into a rollout
// watching controller. selector defaults to RandomClusterSelector when nil.
func NewNormalClusterDesiredVersionController(clock utilsclock.PassiveClock, resourcesDBClient corecosmosstorage.ResourcesDBClient, fleetDBClient fleetcosmosstorage.FleetDBClient, fleetInformers fleetinformers.FleetInformers, serviceProviderClusterLister corelisters.ServiceProviderClusterLister, clusterLister corelisters.ClusterLister, selector ClusterSelector, config RolloutConfig) controllerutils.Controller {
	if selector == nil {
		selector = RandomClusterSelector{}
	}
	_, rolloutLister := fleetInformers.ControlPlaneVersionRollouts()
	syncer := &normalClusterDesiredVersionSyncer{
		clock:                        clock,
		resourcesDBClient:            resourcesDBClient,
		rolloutLister:                rolloutLister,
		fleetDBClient:                fleetDBClient,
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
// with a release threshold at/under best. Clusters whose backing
// HCPOpenShiftCluster carries an experimental ControlPlaneExactVersion (their
// lowercased cluster resource ID is in clustersWithExactVersion) are owned by the
// forced assignment controller and are never advanced here. It is a pure function.
func eligibleClusters(serviceProviderClusters []*coreapi.ServiceProviderCluster, best semver.Version, clustersWithExactVersion map[string]bool) []*coreapi.ServiceProviderCluster {
	var eligible []*coreapi.ServiceProviderCluster
	for _, serviceProviderCluster := range serviceProviderClusters {
		// The experimental exact-version override is authoritative and managed by
		// the forced assignment controller; normal rollout must not advance it.
		if serviceProviderCluster.ResourceID != nil && serviceProviderCluster.ResourceID.Parent != nil &&
			clustersWithExactVersion[strings.ToLower(serviceProviderCluster.ResourceID.Parent.String())] {
			continue
		}
		desired := serviceProviderCluster.Spec.ControlPlaneVersion.DesiredVersion
		below := desired == nil || desired.LT(best)
		if !below {
			continue
		}
		pin := serviceProviderCluster.Spec.PinnedVersion
		switch {
		case pin.ExactVersion == nil:
			eligible = append(eligible, serviceProviderCluster)
		case pin.UntilExactVersion != nil && pin.UntilExactVersion.LTE(best):
			eligible = append(eligible, serviceProviderCluster)
		default:
			// pinned and still holding; not eligible
		}
	}
	return eligible
}

// clustersWithExperimentalExactVersion returns the set of cluster resource IDs
// (lowercased) whose ExperimentalFeatures.ControlPlaneExactVersion is set. Those
// clusters are held at that exact version by the forced assignment controller, so
// normal rollout excludes them from eligibility.
func (c *normalClusterDesiredVersionSyncer) clustersWithExperimentalExactVersion(ctx context.Context) (map[string]bool, error) {
	clusters, err := c.clusterLister.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Clusters: %w", err)
	}
	out := map[string]bool{}
	for _, cluster := range clusters {
		if cluster.ID != nil && cluster.ServiceProviderProperties.ExperimentalFeatures.ControlPlaneExactVersion != nil {
			out[strings.ToLower(cluster.ID.String())] = true
		}
	}
	return out, nil
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

	// Step 1: failure budget. The absolute count is a floor: abort only when the
	// number of failed clusters exceeds the larger of the absolute minimum and
	// the per-channel fraction of clusters desiring best.
	failureBudget := max(float64(failureBudgetAbsolute), failureBudgetFraction*float64(desiredAtBest))
	if float64(failed) > failureBudget {
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
		n := int(min(canaryThreshold-inFlightOrDone, int64(eligibleCount)))
		return rolloutDecisionResult{Outcome: outcomeCanary, SelectCount: n, Message: fmt.Sprintf("selecting %d canary clusters for %s", n, key)}
	}

	// Step 5: canary gate — wait for canary% successful before rolling.
	if successful < percentOfCeil(config.CanaryPercentage, total) {
		return rolloutDecisionResult{Outcome: outcomeProgressing, Message: fmt.Sprintf("waiting for canaries to become successful at %s", key)}
	}

	// Step 6: rolling — select until in-flight reaches rolling%.
	rollingThreshold := percentOfCeil(config.RollingPercentage, total)
	if inFlightOrDone < rollingThreshold {
		n := int(min(rollingThreshold-inFlightOrDone, int64(eligibleCount)))
		return rolloutDecisionResult{Outcome: outcomeRolling, SelectCount: n, Message: fmt.Sprintf("selecting %d rolling clusters for %s", n, key)}
	}

	return rolloutDecisionResult{Outcome: outcomeProgressing, Message: fmt.Sprintf("rollout to %s in progress", key)}
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

	serviceProviderClusters, err := serviceProviderClustersForChannel(ctx, c.serviceProviderClusterLister, c.clusterLister, key.YStreamChannel)
	if err != nil {
		return utils.TrackError(err)
	}

	clustersWithExactVersion, err := c.clustersWithExperimentalExactVersion(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	var eligible []*coreapi.ServiceProviderCluster
	if rollout.Spec.BestExactVersion != nil {
		eligible = eligibleClusters(serviceProviderClusters, *rollout.Spec.BestExactVersion, clustersWithExactVersion)
	}

	decision := rolloutDecision(rollout, len(serviceProviderClusters), len(eligible), c.config)

	// Advance the selected clusters, accumulating errors so one failure does not
	// stop the others; a partial failure still records the rollout condition and
	// marks it degraded.
	var assignErrs []error
	if decision.SelectCount > 0 {
		best := *rollout.Spec.BestExactVersion
		now := metav1.Time{Time: c.clock.Now()}
		for _, serviceProviderCluster := range c.selector.Select(eligible, decision.SelectCount) {
			if assignErr := c.assignDesiredVersion(ctx, serviceProviderCluster, best, now); assignErr != nil {
				assignErrs = append(assignErrs, assignErr)
			}
		}
	}
	assignErr := errors.Join(assignErrs...)

	conditionErr := c.recordCondition(ctx, rollout, decision, assignErr)
	return errors.Join(assignErr, conditionErr)
}

// assignDesiredVersion sets a single cluster's desired version to best, recording
// the transition time.
func (c *normalClusterDesiredVersionSyncer) assignDesiredVersion(ctx context.Context, serviceProviderCluster *coreapi.ServiceProviderCluster, best semver.Version, now metav1.Time) error {
	subscription, resourceGroup, cluster, ok := serviceProviderClusterCoords(serviceProviderCluster)
	if !ok {
		return fmt.Errorf("ServiceProviderCluster is missing a cluster-scoped resource ID")
	}
	replacement := serviceProviderCluster.DeepCopy()
	bestCopy := best
	setDesiredVersion(replacement, &bestCopy, now)
	if _, err := c.resourcesDBClient.ServiceProviderClusters(subscription, resourceGroup, cluster).Replace(ctx, replacement, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to replace ServiceProviderCluster %s/%s/%s: %w", subscription, resourceGroup, cluster, err)
	}
	return nil
}

// recordCondition writes the rollout's Progressing/Degraded conditions from the
// decision, skipping the write when nothing changed. A non-nil syncErr forces
// Degraded to true regardless of the decision.
func (c *normalClusterDesiredVersionSyncer) recordCondition(ctx context.Context, rollout *fleetapi.ControlPlaneVersionRollout, decision rolloutDecisionResult, syncErr error) error {
	replacement := rollout.DeepCopy()

	progressing := metav1.ConditionFalse
	degraded := metav1.ConditionFalse
	degradedReason := string(decision.Outcome)
	degradedMessage := decision.Message
	switch decision.Outcome {
	case outcomeCanary, outcomeRolling, outcomeProgressing:
		progressing = metav1.ConditionTrue
	case outcomeFailure:
		degraded = metav1.ConditionTrue
	case outcomeStable, outcomeNoBest:
		// both false
	}
	if syncErr != nil {
		degraded = metav1.ConditionTrue
		degradedReason = "AssignmentFailed"
		degradedMessage = fmt.Sprintf("failed to advance one or more clusters: %v", syncErr)
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
		Reason:  degradedReason,
		Message: degradedMessage,
	})

	if equality.Semantic.DeepEqual(rollout.Status.Conditions, replacement.Status.Conditions) {
		return nil
	}
	if _, err := c.fleetDBClient.ControlPlaneVersionRollouts().Replace(ctx, replacement, rollout, nil); cosmosstorageutils.IsPreconditionFailedError(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to replace ControlPlaneVersionRollout %q: %w", rollout.GetStampIdentifier(), err)
	}
	return nil
}

// serviceProviderClusterCoords extracts the subscription, resource group, and
// cluster name from a ServiceProviderCluster's resource ID (it is nested under
// its cluster).
func serviceProviderClusterCoords(serviceProviderCluster *coreapi.ServiceProviderCluster) (subscription, resourceGroup, cluster string, ok bool) {
	id := serviceProviderCluster.ResourceID
	if id == nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" {
		return "", "", "", false
	}
	return id.SubscriptionID, id.ResourceGroupName, id.Parent.Name, true
}
