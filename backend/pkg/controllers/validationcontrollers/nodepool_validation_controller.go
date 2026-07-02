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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	cooldownChecker               controllerutil.CooldownChecker
	resourcesDBClient             database.ResourcesDBClient
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister

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
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	syncer := &nodePoolValidationSyncer{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:             resourcesDBClient,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		validation:                    validation,
	}

	controller := controllerutils.NewNodePoolWatchingController(
		fmt.Sprintf("NodePoolValidation%s", validation.Name()),
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)

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

	cachedServiceProviderNodePool, err := c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderNodePool will populate it; we'll be re-enqueued via the ServiceProviderNodePool informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool: %w", err))
	}

	shouldProcess := c.shouldProcess(cachedServiceProviderNodePool)
	if !shouldProcess {
		return nil // no work to do
	}
	existingServiceProviderNodePool := cachedServiceProviderNodePool.DeepCopy()
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingNodePool.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	// We store the validation error in a separate variable and we use that as the
	// error to return to the caller. This allows us to perform other remaining
	// tasks in the syncer even if the validation fails, and we ultimately
	// drive the behavior of its controller through the outcome of the validation.
	validationErr := c.validation.Validate(ctx, existingCluster, subscription, existingNodePool)

	validationCondition := metav1.Condition{
		Type: c.validation.Name(),
	}
	if validationErr != nil {
		validationCondition.Status = metav1.ConditionFalse
		validationCondition.Reason = "Failed"
		validationCondition.Message = fmt.Sprintf("Validation failed: %s", validationErr.Error())
	} else {
		validationCondition.Status = metav1.ConditionTrue
		validationCondition.Reason = "Succeeded"
		validationCondition.Message = "Validation succeeded"
	}
	meta.SetStatusCondition(&existingServiceProviderNodePool.Status.Validations, validationCondition)

	serviceProviderNodePoolsCosmosClient := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = serviceProviderNodePoolsCosmosClient.Replace(ctx, existingServiceProviderNodePool, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}

	return validationErr
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *nodePoolValidationSyncer) shouldProcess(serviceProviderNodePool *api.ServiceProviderNodePool) bool {
	return !meta.IsStatusConditionTrue(serviceProviderNodePool.Status.Validations, c.validation.Name())
}

func (c *nodePoolValidationSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
