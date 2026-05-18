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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// clusterValidationSyncer is a Cluster syncer that performs a Cluster
// validation.
type clusterValidationSyncer struct {
	cooldownChecker   controllerutil.CooldownChecker
	resourcesDBClient database.ResourcesDBClient

	// validation is the validation to perform on the cluster.
	validation validations.ClusterValidation
}

var _ controllerutils.ClusterSyncer = (*clusterValidationSyncer)(nil)

// NewClusterValidationController creates a new controller that
// executes the provided Cluster validation on each cluster.
func NewClusterValidationController(
	validation validations.ClusterValidation,
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
) controllerutils.Controller {

	syncer := &clusterValidationSyncer{
		cooldownChecker:   controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient: resourcesDBClient,
		validation:        validation,
	}

	controller := controllerutils.NewClusterWatchingController(
		fmt.Sprintf("ClusterValidation%s", validation.Name()),
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)

	return controller
}

func (c *clusterValidationSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	existingCluster, err := c.resourcesDBClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	shouldProcess := c.shouldProcess(existingServiceProviderCluster)
	if !shouldProcess {
		return nil // no work to do
	}
	subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Subscription: %w", err))
	}

	// We store the validation error in a separate variable and we use that as the
	// error to return to the caller. This allows us to perform other remaining
	// tasks in the syncer even if the validation fails, and we ultimately
	// drive the behavior of its controller through the outcome of the validation.
	validationErr := c.validation.Validate(ctx, subscription, existingCluster)

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
	meta.SetStatusCondition(&existingServiceProviderCluster.Status.Validations, validationCondition)

	serviceProviderClustersCosmosClient := c.resourcesDBClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersCosmosClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return validationErr
}

// shouldProcess returns true when the condition associated to the validation does not exist or when it exists but
// it failed to run successfully in a previous attempt.
func (c *clusterValidationSyncer) shouldProcess(serviceProviderCluster *api.ServiceProviderCluster) bool {
	return !meta.IsStatusConditionTrue(serviceProviderCluster.Status.Validations, c.validation.Name())
}

func (c *clusterValidationSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
