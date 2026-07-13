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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterValidationSyncer is a Cluster syncer that performs a Cluster
// validation.
type clusterValidationSyncer struct {
	resourcesDBClient database.ResourcesDBClient

	// validation is the validation to perform on the cluster.
	validation validations.ClusterValidation
}

var _ controllerutils.ClusterSyncer = (*clusterValidationSyncer)(nil)

// NewClusterValidationController creates a new controller that
// executes the provided Cluster validation on each cluster.
func NewClusterValidationController(
	validation validations.ClusterValidation,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
	kubeApplierInformers *unionkubeapplierinformers.UnionKubeApplierInformers,
) controllerutils.Controller {

	syncer := &clusterValidationSyncer{
		resourcesDBClient: resourcesDBClient,
		validation:        validation,
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

func (c *clusterValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) (controllerutil.SyncResult, error) {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return controllerutil.SyncResult{}, nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return controllerutil.SyncResult{}, nil
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	shouldProcess := c.shouldProcess(existingServiceProviderCluster)
	if !shouldProcess {
		return controllerutil.SyncResult{}, nil // no work to do
	}
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	// We store the validation error in a separate variable and we use that as the
	// error to return to the caller. This allows us to perform other remaining
	// tasks in the syncer even if the validation fails, and we ultimately
	// drive the behavior of its controller through the outcome of the validation.
	result := c.validation.Validate(ctx, subscription, existingCluster)
	updatedValidation := validationResultToStatus(c.validation.Name(), result, time.Now())
	replacement := existingServiceProviderCluster.DeepCopy()
	replacement.Status.Validations = upsertValidationStatus(replacement.Status.Validations, updatedValidation)

	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, replacement, nil)
	if database.IsPreconditionFailedError(err) {
		// if we have a conflict error, then we're guaranteed that our informer will eventually see an update and trigger us again.
		return controllerutil.SyncResult{}, nil
	}
	if err != nil {
		return controllerutil.SyncResult{}, utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return validationResultToSyncResult(result), validationResultToError(result)
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *clusterValidationSyncer) shouldProcess(serviceProviderCluster *api.ServiceProviderCluster) bool {
	for _, v := range serviceProviderCluster.Status.Validations {
		if v.Type != c.validation.Name() {
			continue
		}
		// Re-run unless it is Passed.
		return v.Condition.Status != metav1.ConditionTrue
	}
	return true
}
