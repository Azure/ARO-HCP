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

// createServiceProviderNodePoolSyncer ensures the singleton ServiceProviderNodePool
// Cosmos document exists for each HCP node pool. If the node pool has
// DeletionTimestamp set, creation is skipped.
type createServiceProviderNodePoolSyncer struct {
	cooldownChecker               controllerutil.CooldownChecker
	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
	resourcesDBClient             database.ResourcesDBClient
}

var _ controllerutils.NodePoolSyncer = (*createServiceProviderNodePoolSyncer)(nil)

func NewCreateServiceProviderNodePoolController(
	activeOperationLister listers.ActiveOperationLister,
	resourcesDBClient database.ResourcesDBClient,
	informers informers.BackendInformers,
) controllerutils.Controller {
	_, nodePoolLister := informers.NodePools()
	_, serviceProviderNodePoolLister := informers.ServiceProviderNodePools()

	syncer := &createServiceProviderNodePoolSyncer{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
		resourcesDBClient:             resourcesDBClient,
	}

	return controllerutils.NewNodePoolWatchingController(
		"CreateServiceProviderNodePool",
		resourcesDBClient,
		informers,
		1*time.Minute,
		syncer,
	)
}

func (c *createServiceProviderNodePoolSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

func (c *createServiceProviderNodePoolSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	cachedNodePool, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get node pool from cache: %w", err))
	}
	if cachedNodePool.ServiceProviderProperties.DeletionTimestamp != nil {
		return nil
	}

	_, err = c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err == nil {
		return nil
	}
	if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from cache: %w", err))
	}

	if err := database.CreateServiceProviderNodePool(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		if database.IsConflictError(err) {
			return nil
		}
		return utils.TrackError(fmt.Errorf("failed to create ServiceProviderNodePool: %w", err))
	}

	return nil
}
