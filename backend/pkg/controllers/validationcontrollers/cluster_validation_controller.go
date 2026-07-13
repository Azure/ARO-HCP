package validationcontrollers

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

// clusterValidationSyncer is a Cluster syncer that performs a Cluster
// validation.
type clusterValidationSyncer struct {
	cooldownChecker      controllerutil.CooldownChecker
	retryCooldownChecker *controllerutil.SettableCooldownChecker
	enqueueAfter         controllerutils.AfterEnqueuer
	resourcesDBClient    database.ResourcesDBClient

	// consecutiveUnknownCounts tracks how many consecutive Unknown results
	// each key has produced. When a previous condition exists and the
	// result is Unknown, the stored condition is preserved for the first
	// maxConsecutiveUnknownsBeforeWrite attempts.
	consecutiveUnknownCounts *lru.Cache

	// validation is the validation to perform on the cluster.
	validation validations.ClusterValidation
}

const maxConsecutiveUnknownsBeforeWrite = 10

var _ controllerutils.ClusterSyncer = (*clusterValidationSyncer)(nil)

// NewClusterValidationController creates a new controller that
// executes the provided Cluster validation on each cluster.
func NewClusterValidationController(
	validation validations.ClusterValidation,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	syncer := &clusterValidationSyncer{
		cooldownChecker:          controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		retryCooldownChecker:     controllerutil.NewSettableCooldownChecker(),
		resourcesDBClient:        resourcesDBClient,
		consecutiveUnknownCounts: lru.New(1000000),
		validation:               validation,
	}

	controller := controllerutils.NewClusterWatchingController(
		fmt.Sprintf("ClusterValidation%s", validation.Name()),
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

func (c *clusterValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

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

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if !c.shouldProcess(existingServiceProviderCluster) {
		return nil
	}
	if !c.retryCooldownChecker.CanSync(ctx, key) {
		if c.enqueueAfter != nil {
			c.enqueueAfter.EnqueueAfter(key, c.retryCooldownChecker.TimeUntilReady(key)+time.Second)
		}
		return nil
	}
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	result := c.validation.Validate(ctx, subscription, existingCluster)
	result = validations.DefaultResult(result)

	validationCondition := validations.BuildCondition(c.validation.Name(), result)
	switch result.Outcome {
	case validations.OutcomeTypeUnknown:
		count := c.incrementUnknownCount(key)
		if existingCondition := meta.FindStatusCondition(existingServiceProviderCluster.Status.Validations, c.validation.Name()); existingCondition != nil && existingCondition.Status != metav1.ConditionUnknown && count <= maxConsecutiveUnknownsBeforeWrite {
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
			"serviceProviderMessage", result.Failed.ServiceProviderMessage,
		)
	case validations.OutcomeTypePassed:
		logger.Info("Writing passed state")
	}
	// if we get here, we're going to overwrite the previous status
	c.resetUnknownCount(key)

	replacement := existingServiceProviderCluster.DeepCopy()
	meta.SetStatusCondition(&replacement.Status.Validations, validationCondition)

	if !equality.Semantic.DeepEqual(existingServiceProviderCluster, replacement) {
		serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
		_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
		if database.IsPreconditionFailedError(err) {
			return nil
		}
		if err != nil {
			return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
		}
	}

	c.handleRequeue(key, result)
	return nil
}

func (c *clusterValidationSyncer) handleRequeue(key controllerutils.HCPClusterKey, result *validations.ValidationResult) {
	if result.EarliestRetryAfter != nil {
		c.retryCooldownChecker.SetCooldown(key, *result.EarliestRetryAfter)
	}

	if result.Outcome == validations.OutcomeTypePassed {
		return
	}

	retryAfter := *result.EarliestRetryAfter + time.Second
	c.enqueueAfter.EnqueueAfter(key, retryAfter)
}

func (c *clusterValidationSyncer) incrementUnknownCount(key controllerutils.HCPClusterKey) int {
	count := 1
	if existing, ok := c.consecutiveUnknownCounts.Get(key); ok {
		count = existing.(int) + 1
	}
	c.consecutiveUnknownCounts.Add(key, count)
	return count
}

func (c *clusterValidationSyncer) resetUnknownCount(key controllerutils.HCPClusterKey) {
	c.consecutiveUnknownCounts.Remove(key)
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *clusterValidationSyncer) shouldProcess(serviceProviderCluster *api.ServiceProviderCluster) bool {
	return !meta.IsStatusConditionTrue(serviceProviderCluster.Status.Validations, c.validation.Name())
}

func (c *clusterValidationSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
