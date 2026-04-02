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
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/utils/apihelpers"
)

// clusterDegradedSummarySyncer is a ClusterSyncer that reads all cluster-level controllers
// and summarizes their degraded conditions into a single Degraded condition on the
// ServiceProviderCluster.
type clusterDegradedSummarySyncer struct {
	cooldownChecker   controllerutils.CooldownChecker
	cosmosClient      database.DBClient
	controllerLister  listers.ControllerLister
	inertia           *controllerutils.InertiaConfig
	clock             utilsclock.PassiveClock
}

var _ controllerutils.ClusterSyncer = (*clusterDegradedSummarySyncer)(nil)

// NewClusterDegradedSummaryController creates a new controller that watches all
// cluster-level controllers and summarizes their degraded conditions into the
// ServiceProviderCluster's Degraded condition.
func NewClusterDegradedSummaryController(
	cosmosClient database.DBClient,
	backendInformers informers.BackendInformers,
	inertia *controllerutils.InertiaConfig,
) controllerutils.Controller {
	_, controllerLister := backendInformers.Controllers()

	syncer := &clusterDegradedSummarySyncer{
		cooldownChecker:  controllerutils.NewTimeBasedCooldownChecker(30 * time.Second),
		cosmosClient:     cosmosClient,
		controllerLister: controllerLister,
		inertia:          inertia,
		clock:            utilsclock.RealClock{},
	}

	return controllerutils.NewClusterWatchingController(
		"ClusterDegradedSummary",
		cosmosClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *clusterDegradedSummarySyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	_, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}

	existingServiceProviderCluster, err := database.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	allControllers, err := c.controllerLister.ListForCluster(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to list controllers for cluster: %w", err))
	}

	// Filter to only cluster-level controllers (not nodepool or externalauth controllers)
	var clusterControllers []*api.Controller
	for _, controller := range allControllers {
		if apihelpers.ResourceTypeEqual(controller.GetResourceID().ResourceType, api.ClusterControllerResourceType) {
			clusterControllers = append(clusterControllers, controller)
		}
	}

	degradedCondition := computeDegradedCondition(clusterControllers, c.inertia, c.clock.Now())

	// Check if the condition actually changed before writing
	existingConditions := existingServiceProviderCluster.Status.Conditions
	apimeta.SetStatusCondition(&existingServiceProviderCluster.Status.Conditions, degradedCondition)
	if equality.Semantic.DeepEqual(existingConditions, existingServiceProviderCluster.Status.Conditions) {
		return nil
	}

	serviceProviderClustersClient := c.cosmosClient.ServiceProviderClusters(key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	_, err = serviceProviderClustersClient.Replace(ctx, existingServiceProviderCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to replace ServiceProviderCluster: %w", err))
	}

	return nil
}

func (c *clusterDegradedSummarySyncer) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
