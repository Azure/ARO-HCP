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

// clusterValidationSyncer is a Cluster syncer that performs a Cluster
// validation.
type clusterValidationSyncer struct {
	resourcesDBClient            database.ResourcesDBClient
	serviceProviderClusterLister listers.ServiceProviderClusterLister

	// validation is the validation to perform on the cluster.
	validation validations.ClusterValidation

	// earliestRetryTimes tracks per-key deadlines before which retries are
	// suppressed. Informer events may re-enqueue a key before the
	// EarliestRetryAfter delay has elapsed; this map lets SyncOnce skip
	// those early runs.
	earliestRetryTimes sync.Map
}

var _ controllerutils.ClusterSyncer = (*clusterValidationSyncer)(nil)

// NewClusterValidationController creates a new controller that
// executes the provided Cluster validation on each cluster.
func NewClusterValidationController(
	validation validations.ClusterValidation,
	resourcesDBClient database.ResourcesDBClient,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	syncer := &clusterValidationSyncer{
		resourcesDBClient:            resourcesDBClient,
		serviceProviderClusterLister: serviceProviderClusterLister,
		validation:                   validation,
	}

	controller := controllerutils.NewClusterWatchingController(
		fmt.Sprintf("ClusterValidation%s", validation.Name()),
		resourcesDBClient,
		informers,
		kubeApplierInformers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *clusterValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
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

	cachedServiceProviderCluster, err := c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		// CreateServiceProviderCluster will populate it; we'll be re-enqueued via the ServiceProviderCluster informer.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster: %w", err))
	}

	shouldProcess := c.shouldProcess(cachedServiceProviderCluster)
	if !shouldProcess {
		return nil // no work to do
	}
	existingServiceProviderCluster := cachedServiceProviderCluster.DeepCopy()
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	result := validations.DefaultResult(c.validation.Validate(ctx, subscription, existingCluster))

	validationCondition := result.ToCondition(c.validation.Name())

	replacement := existingServiceProviderCluster.DeepCopy()
	meta.SetStatusCondition(&replacement.Status.Validations, validationCondition)

	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	if result.EarliestRetryAfter != nil {
		c.earliestRetryTimes.Store(key, time.Now().Add(*result.EarliestRetryAfter))
	}

	return result.ToSyncError(logger, c.validation.Name())
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it is not in a successful state (i.e. Failed or Unknown).
func (c *clusterValidationSyncer) shouldProcess(serviceProviderCluster *api.ServiceProviderCluster) bool {
	return !meta.IsStatusConditionTrue(serviceProviderCluster.Status.Validations, c.validation.Name())
}
