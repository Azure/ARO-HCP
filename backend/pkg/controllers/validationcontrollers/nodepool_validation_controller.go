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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolValidationSyncer is a NodePool syncer that performs a NodePool
// validation.
type nodePoolValidationSyncer struct {
	resourcesDBClient             database.ResourcesDBClient
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister

	// validation is the validation to perform on the node pool.
	validation validations.NodePoolValidation

	// earliestRetryTimes tracks per-key deadlines before which retries are
	// suppressed. Informer events may re-enqueue a key before the
	// EarliestRetryAfter delay has elapsed; this map lets SyncOnce skip
	// those early runs.
	earliestRetryTimes sync.Map
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
	logger := utils.LoggerFromContext(ctx)

	if deadline, ok := c.earliestRetryTimes.Load(key); ok {
		if time.Now().Before(deadline.(time.Time)) {
			return nil
		}
		c.earliestRetryTimes.Delete(key)
	}

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

	result := validations.DefaultResult(c.validation.Validate(ctx, existingCluster, subscription, existingNodePool))

	validationCondition := result.ToCondition(c.validation.Name())
	meta.SetStatusCondition(&existingServiceProviderNodePool.Status.Validations, validationCondition)

	serviceProviderNodePoolsCosmosClient := c.resourcesDBClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = serviceProviderNodePoolsCosmosClient.Replace(ctx, existingServiceProviderNodePool, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}

	if result.EarliestRetryAfter != nil {
		c.earliestRetryTimes.Store(key, time.Now().Add(*result.EarliestRetryAfter))
	}

	return result.ToSyncError(logger, c.validation.Name())
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it is not in a successful state (i.e. Failed or Unknown).
func (c *nodePoolValidationSyncer) shouldProcess(serviceProviderNodePool *api.ServiceProviderNodePool) bool {
	return !meta.IsStatusConditionTrue(serviceProviderNodePool.Status.Validations, c.validation.Name())
}
