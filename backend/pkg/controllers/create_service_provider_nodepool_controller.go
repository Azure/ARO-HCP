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

// createServiceProviderNodePoolSyncer ensures a ServiceProviderNodePool
// document exists for every HCPNodePool. Consumer controllers (validation,
// version, upgrade) read the ServiceProviderNodePool through a cached lister
// and bail out when it is missing; this syncer is the single place that
// actually creates the document, so the GetOrCreate pattern stays in one
// well-known location.
type createServiceProviderNodePoolSyncer struct {
	cooldownChecker               controllerutil.CooldownChecker
	resourcesDBClient             database.ResourcesDBClient
	nodePoolLister                listers.NodePoolLister
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister
}

var _ controllerutils.NodePoolSyncer = (*createServiceProviderNodePoolSyncer)(nil)

// NewCreateServiceProviderNodePoolController wires the controller that creates
// missing ServiceProviderNodePool documents.
func NewCreateServiceProviderNodePoolController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	nodePoolLister listers.NodePoolLister,
	serviceProviderNodePoolLister listers.ServiceProviderNodePoolLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &createServiceProviderNodePoolSyncer{
		cooldownChecker:               controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:             resourcesDBClient,
		nodePoolLister:                nodePoolLister,
		serviceProviderNodePoolLister: serviceProviderNodePoolLister,
	}

	return controllerutils.NewNodePoolWatchingController(
		"CreateServiceProviderNodePool",
		resourcesDBClient,
		backendInformers,
		1*time.Minute,
		syncer,
	)
}

func (c *createServiceProviderNodePoolSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce creates a ServiceProviderNodePool for the given HCPNodePool when
// one does not already exist. The lister is consulted first so steady-state
// runs avoid a Cosmos round-trip; if it is missing, GetOrCreate is called and
// any 409 conflict is handled by the underlying helper.
func (c *createServiceProviderNodePoolSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPNodePoolKey) error {
	_, err := c.nodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCPNodePool from lister: %w", err))
	}

	_, err = c.serviceProviderNodePoolLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName, key.HCPNodePoolName)
	if err == nil {
		return nil
	}
	if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderNodePool from lister: %w", err))
	}

	if _, err := database.GetOrCreateServiceProviderNodePool(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		return utils.TrackError(fmt.Errorf("failed to create ServiceProviderNodePool: %w", err))
	}

	return nil
}
