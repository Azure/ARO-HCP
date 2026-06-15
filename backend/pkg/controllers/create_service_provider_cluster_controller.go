// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// createServiceProviderClusterSyncer ensures the singleton ServiceProviderCluster
// Cosmos document exists for each HCP cluster. If the cluster has DeletionTimestamp
// set, creation is skipped.
type createServiceProviderClusterSyncer struct {
	cooldownChecker              controllerutil.CooldownChecker
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
	resourcesDBClient            database.ResourcesDBClient
}

var _ controllerutils.ClusterSyncer = (*createServiceProviderClusterSyncer)(nil)

func NewCreateServiceProviderClusterController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, clusterLister := informers.Clusters()
	_, serviceProviderClusterLister := informers.ServiceProviderClusters()

	syncer := &createServiceProviderClusterSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
		resourcesDBClient:            resourcesDBClient,
	}

	return controllerutils.NewClusterWatchingController(
		"CreateServiceProviderCluster",
		resourcesDBClient,
		informers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *createServiceProviderClusterSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *createServiceProviderClusterSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	cachedCluster, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if cachedCluster.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	_, err = c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err == nil {
		return nil
	}
	if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from cache: %w", err))
	}

	if err := database.CreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		if database.IsConflictError(err) {
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to create ServiceProviderCluster: %w", err))
	}

	return nil
}
