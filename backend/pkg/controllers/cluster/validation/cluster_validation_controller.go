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
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/lru"

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

	// clock provides the time recorded in Status.ValidationProbeTimes and the run duration metric.
	clock utilsclock.PassiveClock

	// runTimeout bounds a single validation run; see validationutils.ValidationRunTimeout.
	runTimeout time.Duration
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
		clock:                        utilsclock.RealClock{},
		runTimeout:                   validationutils.ValidationRunTimeout,
	}

	controller := controllerutils.NewClusterWatchingController(
		name,
		resourcesDBClient,
		informers,
		nil, // as of now, validations do not depend on ReadDesire content
		1*time.Minute,
		syncer,
	)

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
	result := c.runValidation(ctx, subscription, existingCluster)
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
	// ValidationProbeRewriteInterval so that every retry does not become a
	// Cosmos write; a missing probe is always written. Passed and Skipped
	// outcomes drop the probe alongside the failure it described. Unknown
	// outcomes neither refresh nor drop it: refreshing would defeat the
	// consecutive-Unknown write suppression above, and a probe that goes stale
	// during an Unknown streak correctly keeps the operation non-terminal.
	switch result.Outcome.Type {
	case validationutils.OutcomeTypeFailed:
		storedProbe := replacement.Status.ValidationProbeTimes[c.validation.Name()]
		if storedProbe.IsZero() || c.clock.Since(storedProbe.Time) >= validationutils.ValidationProbeRewriteInterval {
			if replacement.Status.ValidationProbeTimes == nil {
				replacement.Status.ValidationProbeTimes = map[string]metav1.Time{}
			}
			replacement.Status.ValidationProbeTimes[c.validation.Name()] = metav1.NewTime(probeStartTime)
		}
	case validationutils.OutcomeTypePassed, validationutils.OutcomeTypeSkipped:
		delete(replacement.Status.ValidationProbeTimes, c.validation.Name())
	}

	if !equality.Semantic.DeepEqual(existingServiceProviderCluster, replacement) {
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// A concurrent writer updated the document and the informer will re-trigger this key. Keep the
			// normal retry cadence anyway: without the cooldown that re-trigger would immediately re-run the
			// validation and its Azure calls, and this run's result and probe time would be lost.
			c.handleRequeue(key, result)
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
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

// runValidation executes the validation bounded by runTimeout. A run that hits the timeout is
// reported as Unknown regardless of what the validation returned: a validation interrupted
// mid-flight may have reached a partial conclusion, and Unknown keeps the operation controllers
// from treating it as a confirmed failure.
//
// The timeout only interrupts work that honors context cancellation (Azure SDK calls do). A
// validation that blocks while ignoring ctx still holds the worker until it returns.
func (c *clusterValidationSyncer) runValidation(ctx context.Context, subscription *coreapi.Subscription, cluster *coreapi.Cluster) validationutils.ValidationResult {
	if c.runTimeout <= 0 {
		return c.validation.Validate(ctx, subscription, cluster)
	}

	runCtx, cancel := context.WithTimeout(ctx, c.runTimeout)
	defer cancel()

	result := c.validation.Validate(runCtx, subscription, cluster)
	if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		validationRunTimeouts.WithLabelValues(c.validation.Name()).Inc()
		message := fmt.Sprintf("validation did not complete within %s", c.runTimeout)
		return validationutils.UnknownValidation("Timeout", "Validation is taking longer than expected; it will be retried.", message, validationutils.ControllerReportingPolicyTypeError)
	}
	return result
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
