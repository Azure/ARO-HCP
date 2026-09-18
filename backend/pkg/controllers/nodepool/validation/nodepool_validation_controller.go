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

package validation

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/listers/corelisters"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	// consecutiveUnknownCountsCacheCapacity bounds the size of the consecutiveUnknownCounts LRU cache.
	consecutiveUnknownCountsCacheCapacity = 50000

	// maxConsecutiveUnknownsBeforeWrite bounds how many consecutive Unknown validation results are
	// suppressed (i.e. the previously stored condition is kept as-is) before an Unknown condition is
	// allowed to overwrite it. This avoids flapping a node pool's validation status to Unknown on a
	// transient blip while still surfacing a persistent Unknown once it has been observed repeatedly.
	maxConsecutiveUnknownsBeforeWrite = 10
)

// nodePoolValidationSyncer is a NodePool syncer that performs a NodePool
// validation.
type nodePoolValidationSyncer struct {
	resourcesDBClient corecosmosstorage.ResourcesDBClient
	// retryCooldownChecker gates re-execution of a key(HCPNodePool) that recently had a
	// retry scheduled. Prevents redundant validation runs while the cooldown
	// from a previous EarliestRetryAfter is still active.
	retryCooldownChecker *controllerutil.SettableCooldownChecker
	// enqueueAfter allows the syncer to schedule a delayed re-processing of a
	// key(HCPNodePool), bypassing the workqueue's default rate limiter.
	enqueueAfter controllerutils.AfterEnqueuer

	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister

	// validation is the validation to perform on the node pool.
	validation validationutils.NodePoolValidation

	// consecutiveUnknownCounts tracks, per HCPNodePoolKey, how many consecutive Unknown validation
	// results have been observed since the last non-Unknown result. It backs the suppression
	// policy in trackConsecutiveUnknowns, which avoids flapping a node pool's validation status
	// to Unknown on a transient blip.
	consecutiveUnknownCounts *lru.Cache

	// controllerName is the workqueue / metric label for this validation controller.
	controllerName string
}

var _ controllerutils.NodePoolSyncer = (*nodePoolValidationSyncer)(nil)

// NewNodePoolValidationController creates a new controller that executes the provided NodePool validation on each node pool.
func NewNodePoolValidationController(
	validation validationutils.NodePoolValidation,
	resourcesDBClient corecosmosstorage.ResourcesDBClient,
	serviceProviderNodePoolLister corelisters.ServiceProviderNodePoolLister,
	informers coreinformers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	controllerName := fmt.Sprintf("NodePoolValidation%s", validation.Name())
	syncer := &nodePoolValidationSyncer{
		retryCooldownChecker:          controllerutil.NewSettableCooldownChecker(),
		resourcesDBClient:             resourcesDBClient,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		validation:                    validation,
		consecutiveUnknownCounts:      lru.New(consecutiveUnknownCountsCacheCapacity),
		controllerName:                controllerName,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		controllerName,
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)

	// Assert that genericWatchingController implements AfterEnqueuer, which lets the syncer explicitly schedule retries via EnqueueAfter rather than
	// relying on error-based rate-limited requeue. Panics at startup if the interface is not satisfied.
	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic("NodePoolValidationController must implement AfterEnqueuer")
	}

	return controller
}

func (c *nodePoolValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Skip processing if the key is still within its cooldown window from a previous validation. All outcomes can schedule a cooldown via
	// EarliestRetryAfter so validations run continuously without racing. Re-enqueue so the item is revisited once the cooldown expires.
	if !c.retryCooldownChecker.CanSync(ctx, key) {
		if c.controllerName != "" {
			controllerutils.ReconcileCooldownSkips.WithLabelValues(c.controllerName).Inc()
		}
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

	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
		return nil // node pool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if existingNodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	cachedServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if cosmosstorageutils.IsNotFoundError(err) {
		// CreateServiceProviderNodePool will populate it; we'll be re-enqueued via the ServiceProviderNodePool informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}

	if !c.shouldProcess(cachedServiceProviderNodePool) {
		return nil // no work to do
	}
	existingServiceProviderNodePool := cachedServiceProviderNodePool.DeepCopy()
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingNodePool.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	result := c.validation.Validate(ctx, existingCluster, subscription, existingNodePool)
	if err := result.Validate(); err != nil {
		return utils.TrackError(fmt.Errorf("validation %s returned invalid ValidationResult: %w", c.validation.Name(), err))
	}

	if result.Outcome.Type != validationutils.OutcomeTypePassed {
		logger.Info("Validation outcome", "validation", c.validation.Name(), "result", result)
	}

	replacement := existingServiceProviderNodePool.DeepCopy()

	// If the validation was skipped, remove its condition so it doesn't appear in status. Otherwise, reconcile the condition with consecutive-Unknown
	// suppression to avoid flapping on transient errors.
	if result.Outcome.Type == validationutils.OutcomeTypeSkipped {
		meta.RemoveStatusCondition(&replacement.Status.Validations, c.validation.Name())
	} else {
		previousCondition := meta.FindStatusCondition(existingServiceProviderNodePool.Status.Validations, c.validation.Name())
		desiredCondition := result.ToCondition(c.validation.Name())

		consecutiveUnknowns := c.trackConsecutiveUnknowns(key, desiredCondition)
		if c.shouldWriteCondition(previousCondition, consecutiveUnknowns) {
			meta.SetStatusCondition(&replacement.Status.Validations, desiredCondition)
		}
	}

	if !equality.Semantic.DeepEqual(existingServiceProviderNodePool, replacement) {
		serviceProviderNodePoolsCosmosClient := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
		_, err = serviceProviderNodePoolsCosmosClient.Replace(ctx, replacement, nil)
		if cosmosstorageutils.IsPreconditionFailedError(err) {
			// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
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

// handleRequeue sets the earliest-retry gate and, for Failed/Unknown outcomes, schedules a
// delayed workqueue requeue. Passed and Skipped outcomes set only the gate (no requeue).
// See EarliestRetryAfter on ValidationResult for the full semantics.
func (c *nodePoolValidationSyncer) handleRequeue(key controllerutils.HCPNodePoolKey, result validationutils.ValidationResult) {
	if result.EarliestRetryAfter == nil {
		return
	}

	c.retryCooldownChecker.SetCooldown(key, *result.EarliestRetryAfter)

	if c.enqueueAfter != nil && (result.Outcome.Type == validationutils.OutcomeTypeFailed || result.Outcome.Type == validationutils.OutcomeTypeUnknown) {
		c.enqueueAfter.EnqueueAfter(key, *result.EarliestRetryAfter+time.Second)
	}
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// its status is not True.
func (c *nodePoolValidationSyncer) shouldProcess(serviceProviderNodePool *coreapi.ServiceProviderNodePool) bool {
	return !meta.IsStatusConditionTrue(serviceProviderNodePool.Status.Validations, c.validation.Name())
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
// This backs a suppression policy that avoids flapping a node pool's validation status to Unknown on a
// transient blip: a persistent Unknown streak is still allowed to overwrite the stored condition once it
// exceeds maxConsecutiveUnknownsBeforeWrite, and a Passed/Failed result (consecutiveUnknowns == 0) always
// overwrites immediately, resetting the streak.
func (c *nodePoolValidationSyncer) shouldWriteCondition(previousCondition *metav1.Condition, consecutiveUnknowns int) bool {
	return previousCondition == nil || consecutiveUnknowns == 0 || consecutiveUnknowns > maxConsecutiveUnknownsBeforeWrite
}

// trackConsecutiveUnknowns maintains the count of consecutive Unknown validation results for the given key. When condition is Unknown it increments and returns the
// running count; otherwise it resets the counter and returns 0.
func (c *nodePoolValidationSyncer) trackConsecutiveUnknowns(key controllerutils.HCPNodePoolKey, condition metav1.Condition) int {
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
