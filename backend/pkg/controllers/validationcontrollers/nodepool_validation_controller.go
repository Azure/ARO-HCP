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

package validationcontrollers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/lru"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolValidationSyncer is a NodePool syncer that performs a NodePool
// validation.
type nodePoolValidationSyncer struct {
	cooldownChecker          controllerutil.CooldownChecker
	retryCooldownChecker     *controllerutil.SettableCooldownChecker
	enqueueAfter             controllerutils.AfterEnqueuer
	resourcesDBClient        database.ResourcesDBClient
	consecutiveUnknownCounts *lru.Cache

	// validation is the validation to perform on the node pool.
	validation validations.NodePoolValidation
}

var _ controllerutils.NodePoolSyncer = (*nodePoolValidationSyncer)(nil)

// NewNodePoolValidationController creates a new controller that
// executes the provided NodePool validation on each node pool.
func NewNodePoolValidationController(
	validation validations.NodePoolValidation,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	syncer := &nodePoolValidationSyncer{
		cooldownChecker:          controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		retryCooldownChecker:     controllerutil.NewSettableCooldownChecker(),
		resourcesDBClient:        resourcesDBClient,
		consecutiveUnknownCounts: lru.New(1000000),
		validation:               validation,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		fmt.Sprintf("NodePoolValidation%s", validation.Name()),
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)

	if enqueuer, ok := controller.(controllerutils.AfterEnqueuer); ok {
		syncer.enqueueAfter = enqueuer
	} else {
		panic("ClusterValidationController must implement AfterEnqueuer")
	}

	return controller
}

func (c *nodePoolValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingNodePool, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil // node pool doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}
	if existingNodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	if !c.shouldProcess(existingServiceProviderNodePool) {
		return nil
	}
	if !c.retryCooldownChecker.CanSync(ctx, key) {
		c.enqueueAfter.EnqueueAfter(key, c.retryCooldownChecker.TimeUntilReady(key)+time.Second)
		return nil
	}
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingNodePool.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	result := c.validation.Validate(ctx, existingCluster, subscription, existingNodePool)
	result = validations.DefaultResult(result)

	logger := utils.LoggerFromContext(ctx)
	validationCondition := validations.BuildCondition(c.validation.Name(), result)
	switch result.Outcome {
	case validations.OutcomeTypeUnknown:
		count := c.incrementUnknownCount(key)
		if existingCondition := meta.FindStatusCondition(existingServiceProviderNodePool.Status.Validations, c.validation.Name()); existingCondition != nil && existingCondition.Status != metav1.ConditionUnknown && count <= maxConsecutiveUnknownsBeforeWrite {
			logger.Info("Validation returned Unknown but previous condition exists, so preserving previous state for a few attempts",
				"previousStatus", existingCondition.Status,
				"previousReason", existingCondition.Reason,
				"unknownReason", validationCondition.Reason,
				"unknownMessage", validationCondition.Message,
				"serviceProviderMessage", result.Unknown.ServiceProviderMessage,
				"consecutiveUnknownCount", count,
			)
			c.handleRequeue(key, result)
			return nil
		}
		logger.Info("Writing unknown state",
			"reason", validationCondition.Reason,
			"serviceProviderMessage", result.Unknown.ServiceProviderMessage,
			"consecutiveUnknownCount", count,
		)
	case validations.OutcomeTypeFailed:
		logger.Info("Writing failed state",
			"reason", result.Failed.Reason,
			"userMessage", result.Failed.UserMessage,
			"serviceProviderMessage", result.Failed.ServiceProviderMessage,
		)
	case validations.OutcomeTypePassed:
		logger.Info("Writing passed state")
	}
	// if we get here, we're going to overwrite the previous status
	c.resetUnknownCount(key)

	replacement := existingServiceProviderNodePool.DeepCopy()
	meta.SetStatusCondition(&replacement.Status.Validations, validationCondition)

	if !equality.Semantic.DeepEqual(existingServiceProviderNodePool, replacement) {
		serviceProviderNodePoolsCosmosClient := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
		_, err = serviceProviderNodePoolsCosmosClient.Replace(ctx, replacement, nil)
		if database.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
		}
	}

	c.handleRequeue(key, result)
	return nil
}

func (c *nodePoolValidationSyncer) handleRequeue(key controllerutils.HCPNodePoolKey, result *validations.ValidationResult) {
	if result.EarliestRetryAfter != nil {
		c.retryCooldownChecker.SetCooldown(key, *result.EarliestRetryAfter)
	}

	if result.Outcome == validations.OutcomeTypePassed {
		return
	}

	retryAfter := *result.EarliestRetryAfter + time.Second
	c.enqueueAfter.EnqueueAfter(key, retryAfter)
}

func (c *nodePoolValidationSyncer) incrementUnknownCount(key controllerutils.HCPNodePoolKey) int {
	count := 1
	if existing, ok := c.consecutiveUnknownCounts.Get(key); ok {
		count = existing.(int) + 1
	}
	c.consecutiveUnknownCounts.Add(key, count)
	return count
}

func (c *nodePoolValidationSyncer) resetUnknownCount(key controllerutils.HCPNodePoolKey) {
	c.consecutiveUnknownCounts.Remove(key)
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *nodePoolValidationSyncer) shouldProcess(serviceProviderNodePool *api.ServiceProviderNodePool) bool {
	return !meta.IsStatusConditionTrue(serviceProviderNodePool.Status.Validations, c.validation.Name())
}

func (c *nodePoolValidationSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
