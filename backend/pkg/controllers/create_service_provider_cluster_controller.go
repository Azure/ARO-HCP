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

// createServiceProviderClusterSyncer ensures a ServiceProviderCluster document
// exists for every HCPCluster. Consumer controllers (validation, version, etc.)
// read the ServiceProviderCluster through a cached lister and bail out when it
// is missing; this syncer is the single place that actually creates the
// document, so the GetOrCreate pattern stays in one well-known location.
type createServiceProviderClusterSyncer struct {
	cooldownChecker              controllerutil.CooldownChecker
	resourcesDBClient            database.ResourcesDBClient
	clusterLister                listers.ClusterLister
	serviceProviderClusterLister listers.ServiceProviderClusterLister
}

var _ controllerutils.ClusterSyncer = (*createServiceProviderClusterSyncer)(nil)

// NewCreateServiceProviderClusterController wires the controller that creates
// missing ServiceProviderCluster documents.
func NewCreateServiceProviderClusterController(
	resourcesDBClient database.ResourcesDBClient,
	activeOperationLister listers.ActiveOperationLister,
	clusterLister listers.ClusterLister,
	serviceProviderClusterLister listers.ServiceProviderClusterLister,
	backendInformers informers.BackendInformers,
) controllerutils.Controller {
	syncer := &createServiceProviderClusterSyncer{
		cooldownChecker:              controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		resourcesDBClient:            resourcesDBClient,
		clusterLister:                clusterLister,
		serviceProviderClusterLister: serviceProviderClusterLister,
	}

	return controllerutils.NewClusterWatchingController(
		"CreateServiceProviderCluster",
		resourcesDBClient,
		backendInformers,
		nil,
		1*time.Minute,
		syncer,
	)
}

func (c *createServiceProviderClusterSyncer) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}

// SyncOnce creates a ServiceProviderCluster for the given HCPCluster when one
// does not already exist. The lister is consulted first so steady-state runs
// avoid a Cosmos round-trip; if it is missing, GetOrCreate is called and any
// 409 conflict is handled by the underlying helper.
func (c *createServiceProviderClusterSyncer) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	_, err := c.clusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get HCPCluster from lister: %w", err))
	}

	_, err = c.serviceProviderClusterLister.Get(ctx, key.SubscriptionID, key.ResourceGroupName, key.HCPClusterName)
	if err == nil {
		return nil
	}
	if !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get ServiceProviderCluster from lister: %w", err))
	}

	if _, err := database.GetOrCreateServiceProviderCluster(ctx, c.resourcesDBClient, key.GetResourceID()); err != nil {
		return utils.TrackError(fmt.Errorf("failed to create ServiceProviderCluster: %w", err))
	}

	return nil
}
