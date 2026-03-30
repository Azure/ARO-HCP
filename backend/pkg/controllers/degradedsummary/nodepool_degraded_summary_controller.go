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

package degradedsummary

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// nodePoolDegradedSummarySyncer is a NodePoolSyncer that reads all nodepool-level controllers
// and summarizes their degraded conditions into a single Degraded condition on the
// ServiceProviderNodePool.
type nodePoolDegradedSummarySyncer struct {
	cooldownChecker  controllerutils.CooldownChecker
	cosmosClient     database.DBClient
	controllerLister listers.ControllerLister
	inertia          *controllerutils.InertiaConfig
	clock            utilsclock.PassiveClock
}

var _ controllerutils.NodePoolSyncer = (*nodePoolDegradedSummarySyncer)(nil)

// NewNodePoolDegradedSummaryController creates a new controller that watches all
// nodepool-level controllers and summarizes their degraded conditions into the
// ServiceProviderNodePool's Degraded condition.
func NewNodePoolDegradedSummaryController(
	cosmosClient database.DBClient,
	backendInformers informers.BackendInformers,
	inertia *controllerutils.InertiaConfig,
) controllerutils.Controller {
	_, controllerLister := backendInformers.Controllers()

	syncer := &nodePoolDegradedSummarySyncer{
		cooldownChecker:  controllerutils.NewTimeBasedCooldownChecker(30 * time.Second),
		cosmosClient:     cosmosClient,
		controllerLister: controllerLister,
		inertia:          inertia,
		clock:            utilsclock.RealClock{},
	}

	return controllerutils.NewNodePoolWatchingController(
		"NodePoolDegradedSummary",
		cosmosClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *nodePoolDegradedSummarySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	_, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).NodePools(key.HCPClusterName).Get(ctx, key.HCPNodePoolName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get NodePool: %w", err))
	}

	existingServiceProviderNodePool, err := database.GetOrCreateServiceProviderNodePool(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderNodePool: %w", err))
	}

	nodePoolControllers, err := c.controllerLister.ListForNodePool(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list controllers for nodepool: %w", err))
	}

	degradedCondition := computeDegradedCondition(nodePoolControllers, c.inertia, c.clock.Now())

	// Check if the condition actually changed before writing
	existingConditions := existingServiceProviderNodePool.Status.Conditions
	apimeta.SetStatusCondition(&existingServiceProviderNodePool.Status.Conditions, degradedCondition)
	if equality.Semantic.DeepEqual(existingConditions, existingServiceProviderNodePool.Status.Conditions) {
		return nil
	}

	serviceProviderNodePoolsClient := c.cosmosClient.ServiceProviderNodePools(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	_, err = serviceProviderNodePoolsClient.Replace(ctx, existingServiceProviderNodePool, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderNodePool: %w", err))
	}

	return nil
}

func (c *nodePoolDegradedSummarySyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
