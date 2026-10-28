package validation

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

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	ClusterValidationContainerRegistryPullCredentialsPermissionValidationControllerName = "ClusterValidationContainerRegistryPullCredentialsPermissionValidation"
	ClusterValidationDataPlaneIdentitiesPermissionsValidationControllerName             = "ClusterValidationDataPlaneIdentitiesPermissionsValidation"
	ClusterValidationControlPlaneIdentitiesPermissionsClusterValidationControllerName   = "ClusterValidationControlPlaneIdentitiesPermissionsClusterValidation"
	ClusterValidationAzureClusterManagedIdentitiesExistenceValidationControllerName     = "ClusterValidationAzureClusterManagedIdentitiesExistenceValidation"
	ClusterValidationAzureClusterResourceGroupExistenceValidationControllerName         = "ClusterValidationAzureClusterResourceGroupExistenceValidation"
	ClusterValidationAzureResourceProvidersRegistrationValidationControllerName         = "ClusterValidationAzureResourceProvidersRegistrationValidation"
	ClusterValidationAzureClusterVnetIntegrationSubnetSizeValidationControllerName      = "ClusterValidationAzureClusterVnetIntegrationSubnetSizeValidation"
	ClusterValidationAlwaysSuccessValidationControllerName                              = "ClusterValidationAlwaysSuccessValidation"
	// consecutiveUnknownCountsCacheCapacity bounds the size of the consecutiveUnknownCounts LRU cache.
	consecutiveUnknownCountsCacheCapacity = 50000

	// maxConsecutiveUnknownsBeforeWrite bounds how many consecutive Unknown validation results are
	// suppressed (i.e. the previously stored condition is kept as-is) before an Unknown condition is
	// allowed to overwrite it. This avoids flapping a cluster's validation status to Unknown on a
	// transient blip while still surfacing a persistent Unknown once it has been observed repeatedly.
	maxConsecutiveUnknownsBeforeWrite = 10
)

// clusterValidationSyncer is a Cluster syncer that performs a Cluster
// validation.
type clusterValidationSyncer struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	// retryCooldownChecker gates re-execution of a key(HCPCluster) that recently had a
	// retry scheduled. Prevents redundant validation runs while the cooldown
	// from a previous EarliestRetryAfter is still active.
	retryCooldownChecker *controllerutil.SettableCooldownChecker
	// enqueueAfter allows the syncer to schedule a delayed re-processing of a
	// key(HCPCluster), bypassing the workqueue's default rate limiter.
	enqueueAfter controllerutils.AfterEnqueuer

	serviceProviderClusterLister corelisters.ServiceProviderClusterLister

	// validation is the validation to perform on the cluster.
	validation validationutils.ClusterValidation

	// consecutiveUnknownCounts tracks, per HCPClusterKey, how many consecutive Unknown validation
	// results have been observed since the last non-Unknown result. It backs the suppression
	// policy in trackConsecutiveUnknowns, which avoids flapping a cluster's validation status
	// to Unknown on a transient blip.
	consecutiveUnknownCounts *lru.Cache

	// probeWriteTimes records, per HCPClusterKey, when the syncer last successfully wrote that
	// validation's probe time, so the rewrite throttle in shouldRewriteProbe can be anchored on
	// actual write moments rather than on run start times.
	probeWriteTimes *lru.Cache

	// clock provides the time recorded in Status.ValidationProbeTimes and the run duration metric.
	clock utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*clusterValidationSyncer)(nil)

// NewClusterValidationController creates a new controller that executes the provided Cluster validation on each cluster.
func NewClusterValidationController(
	validation validationutils.ClusterValidation,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {
	return NewNamedClusterValidationController(fmt.Sprintf("ClusterValidation%s", validation.Name()), validation, resourcesDBClient, serviceProviderClusterLister, informers)
}

func NewNamedClusterValidationController(
	name string,
	validation validationutils.ClusterValidation,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderClusterLister corelisters.ServiceProviderClusterLister,
	informers coreinformers.BackendInformers,
) controllerutils.Controller {

	syncer := &clusterValidationSyncer{
		retryCooldownChecker:         controllerutil.NewSettableCooldownChecker(),
		resourcesDBClient:            resourcesDBClient,
		serviceProviderClusterLister: serviceProviderClusterLister,
		validation:                   validation,
		consecutiveUnknownCounts:     lru.New(consecutiveUnknownCountsCacheCapacity),
		probeWriteTimes:              lru.New(consecutiveUnknownCountsCacheCapacity),
		clock:                        utilsclock.RealClock{},
	}

	controller := controllerutils.NewClusterWatchingController(
		name,
		resourcesDBClient,
		informers,
		nil, // as of now, validations do not depend on ReadDesire content
		1*time.Minute,
		syncer,
	)
	controller.AddInterestingResource(coreapi.ClusterResourceType, "*")
	controller.AddInterestingResource(coreapi.ServiceProviderClusterResourceType, coreapi.ServiceProviderClusterResourceName)
	controller.AddInterestingResource(azcorearm.SubscriptionResourceType, "*")

	// Assert that genericWatchingController implements AfterEnqueuer, which lets the syncer explicitly schedule retries via EnqueueAfter rather than
	// relying on error-based rate-limited requeue. Panics at startup if the interface is not satisfied.
	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic("ClusterValidationController must implement AfterEnqueuer")
	}

	return controller
}

func (c *clusterValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Skip processing if the key is still within its cooldown window from a previous validation. All outcomes can schedule a cooldown via
	// EarliestRetryAfter so validations run continuously without racing. Re-enqueue so the item is revisited once the cooldown expires.
	if !c.retryCooldownChecker.CanSync(ctx, key) {
		if c.enqueueAfter != nil {
			// Add a one-second buffer so the requeue lands strictly after the cooldown expires, avoiding a race where the item fires just before CanSync flips to true.
			c.enqueueAfter.EnqueueAfter(key, c.retryCooldownChecker.TimeUntilReady(key)+time.Second)
		}
		return nil
	}

	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	if _, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName); cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	} else if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	probeStartTime := c.clock.Now()
	result := c.validation.Validate(ctx, subscription, existingCluster)
	if err := result.Validate(); err != nil {
		return utils.TrackError(fmt.Errorf("validation %s returned invalid ValidationResult: %w", c.validation.Name(), err))
	}
	validationRunDuration.WithLabelValues(c.validation.Name(), string(result.Outcome.Type)).Observe(c.clock.Since(probeStartTime).Seconds())

	// Read the ServiceProviderCluster only after the validation returns: a run can take a while, and
	// basing the write on a copy taken before it would make optimistic-concurrency conflicts (and the
	// resulting lost result) more likely.
	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}
	existingServiceProviderCluster := cachedServiceProviderCluster.DeepCopy()

	if result.Outcome.Type != validationutils.OutcomeTypePassed {
		logger.Info("Validation outcome", "validation", c.validation.Name(), "result", result)
	}

	replacement := existingServiceProviderCluster.DeepCopy()

	// If the validation was skipped, remove its condition so it doesn't appear in status. Otherwise, reconcile the condition with consecutive-Unknown
	// suppression to avoid flapping on transient errors.
	if result.Outcome.Type == validationutils.OutcomeTypeSkipped {
		meta.RemoveStatusCondition(&replacement.Status.Validations, c.validation.Name())
	} else {
		previousCondition := meta.FindStatusCondition(existingServiceProviderCluster.Status.Validations, c.validation.Name())
		desiredCondition := result.ToCondition(c.validation.Name())

		consecutiveUnknowns := c.trackConsecutiveUnknowns(key, desiredCondition)
		if c.shouldWriteCondition(previousCondition, consecutiveUnknowns) {
			meta.SetStatusCondition(&replacement.Status.Validations, desiredCondition)
		}
	}

	// Record when the last conclusive Failed run started, independently of
	// whether the stored condition changed. The conditions' LastTransitionTime
	// only advances on outcome changes, so it cannot distinguish "failed and
	// recently re-confirmed" from "failed but not re-checked lately"; the
	// operation controllers use these probe times to make that distinction
	// before failing a create or update terminally. The start time is used
	// because it is the earliest moment the run could have observed Azure, so
	// it never overstates how fresh the evidence is.
	// A still-failing validation rewrites its probe at most once per
	// ValidationProbeRewriteInterval, measured from the last successful write
	// (see shouldRewriteProbe), so that every retry does not become a Cosmos
	// write; a missing probe is always written. Passed and Skipped outcomes drop
	// the probe alongside the failure it described. Unknown outcomes neither
	// refresh nor drop it: refreshing would defeat the consecutive-Unknown write
	// suppression above, and a probe that goes stale during an Unknown streak
	// correctly keeps the operation non-terminal.
	probeRewritten := false
	switch result.Outcome.Type {
	case validationutils.OutcomeTypeFailed:
		storedProbe := replacement.Status.ValidationProbeTimes[c.validation.Name()]
		if storedProbe.IsZero() || c.shouldRewriteProbe(key, storedProbe.Time) {
			if replacement.Status.ValidationProbeTimes == nil {
				replacement.Status.ValidationProbeTimes = map[string]metav1.Time{}
			}
			replacement.Status.ValidationProbeTimes[c.validation.Name()] = metav1.NewTime(probeStartTime)
			probeRewritten = true
		}
	case validationutils.OutcomeTypePassed, validationutils.OutcomeTypeSkipped:
		delete(replacement.Status.ValidationProbeTimes, c.validation.Name())
	}

	if !equality.Semantic.DeepEqual(existingServiceProviderCluster, replacement) {
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			c.handleConflictRequeue(key, result)
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
		if probeRewritten {
			// Record the write moment only after it actually landed, so a rewrite lost to a
			// precondition conflict does not push the next one out by a full interval.
			c.probeWriteTimes.Add(key, c.clock.Now())
		}
	}

	c.handleRequeue(key, result)

	// ControllerReportingPolicy governs only how this Unknown result is reported to the controller
	// machinery (e.g. workqueue error metrics); it has no bearing on the requeue scheduling already
	// handled above by handleRequeue based on EarliestRetryAfter. Keep this as the last step of SyncOnce.
	if result.Outcome.Type == validationutils.OutcomeTypeUnknown && result.Outcome.Unknown.ControllerReportingPolicy == validationutils.ControllerReportingPolicyTypeError {
		return utils.TrackError(fmt.Errorf("validation %s returned an inconclusive (Unknown) result: %s", c.validation.Name(), result.InternalMessage()))
	}

	return nil
}

// handleConflictRequeue schedules the next run after this run's result was lost to a concurrent
// ServiceProviderCluster write. The informer re-triggers the key after such a conflict; a cooldown
// keeps that re-trigger from immediately repeating the validation's Azure calls. The cooldown is
// capped at the non-passed retry delay because the result was not persisted: applying a Passed
// result's long cooldown here would leave a recovered validation reporting False, and its operation
// Provisioning, for up to passedRetryBase plus jitter. A delayed requeue is always scheduled so the
// lost result is recomputed even if the informer event was already consumed.
func (c *clusterValidationSyncer) handleConflictRequeue(key controllerutils.HCPClusterKey, result validationutils.ValidationResult) {
	if result.EarliestRetryAfter == nil {
		return
	}

	retryAfter := validationutils.NonPassedRetryDelay()
	if *result.EarliestRetryAfter < retryAfter {
		retryAfter = *result.EarliestRetryAfter
	}
	c.retryCooldownChecker.SetCooldown(key, retryAfter)
	if c.enqueueAfter != nil {
		c.enqueueAfter.EnqueueAfter(key, retryAfter+time.Second)
	}
}

// handleRequeue sets the earliest-retry gate and, for Failed/Unknown outcomes, schedules a
// delayed workqueue requeue. Passed and Skipped outcomes set only the gate (no requeue).
// See EarliestRetryAfter on ValidationResult for the full semantics.
func (c *clusterValidationSyncer) handleRequeue(key controllerutils.HCPClusterKey, result validationutils.ValidationResult) {
	if result.EarliestRetryAfter == nil {
		return
	}

	c.retryCooldownChecker.SetCooldown(key, *result.EarliestRetryAfter)

	if c.enqueueAfter != nil && (result.Outcome.Type == validationutils.OutcomeTypeFailed || result.Outcome.Type == validationutils.OutcomeTypeUnknown) {
		c.enqueueAfter.EnqueueAfter(key, *result.EarliestRetryAfter+time.Second)
	}
}

// shouldWriteCondition reports whether the newly computed validation condition should be written, versus
// suppressed in favor of leaving previousCondition (the condition currently stored for this validation, or
// nil if none is stored yet) untouched.
//
// The write is suppressed only while all of the following hold:
//   - previousCondition is non-nil (there's something worth preserving), and
//   - consecutiveUnknowns is non-zero (the newly computed condition is Unknown; trackConsecutiveUnknowns
//     returns 0 for any non-Unknown result), and
//   - consecutiveUnknowns has not yet exceeded maxConsecutiveUnknownsBeforeWrite.
//
// This backs a suppression policy that avoids flapping a cluster's validation status to Unknown on a
// transient blip: a persistent Unknown streak is still allowed to overwrite the stored condition once it
// exceeds maxConsecutiveUnknownsBeforeWrite, and a Passed/Failed result (consecutiveUnknowns == 0) always
// overwrites immediately, resetting the streak.
// shouldRewriteProbe reports whether a still-failing run may rewrite its probe time. The
// throttle is anchored on the last successful probe write, not on the stored probe's run
// start: the write lands when a run completes, so anchoring on the run start lets runs
// with alternating durations rewrite more often than ValidationProbeRewriteInterval (a
// 59s run followed by 1s runs can rewrite every retry delay plus one second). When no
// write is on record — after a syncer restart or leader change — the stored probe's age
// is used as a fallback: it is necessary but not sufficient for the interval to have
// elapsed since the write, so at most one rewrite per restart can land early.
func (c *clusterValidationSyncer) shouldRewriteProbe(key controllerutils.HCPClusterKey, storedProbe time.Time) bool {
	if lastWrite, ok := c.probeWriteTimes.Get(key); ok {
		return c.clock.Since(lastWrite.(time.Time)) >= validationutils.ValidationProbeRewriteInterval
	}
	return c.clock.Since(storedProbe) >= validationutils.ValidationProbeRewriteInterval
}

func (c *clusterValidationSyncer) shouldWriteCondition(previousCondition *metav1.Condition, consecutiveUnknowns int) bool {
	return previousCondition == nil || consecutiveUnknowns == 0 || consecutiveUnknowns > maxConsecutiveUnknownsBeforeWrite
}

// trackConsecutiveUnknowns maintains the count of consecutive Unknown validation results for the given key. When condition is Unknown it increments and returns the
// running count; otherwise it resets the counter and returns 0.
func (c *clusterValidationSyncer) trackConsecutiveUnknowns(key controllerutils.HCPClusterKey, condition metav1.Condition) int {
	if condition.Status != metav1.ConditionUnknown {
		c.consecutiveUnknownCounts.Remove(key)
		return 0
	}

	count := 1
	if v, ok := c.consecutiveUnknownCounts.Get(key); ok {
		count = v.(int) + 1
	}
	c.consecutiveUnknownCounts.Add(key, count)
	return count
}
