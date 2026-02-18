// Copyright 2025 Microsoft Corporation
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
	"net/http"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type createCluster struct {
	cooldownChecker      controllerutils.CooldownChecker
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

// NewDataDumpController periodically lists all clusters and for each out when the cluster was created and its state.
func NewDataDumpController(activeOperationLister listers.ActiveOperationLister, cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec) controllerutils.ClusterSyncer {
	c := &createCluster{
		cooldownChecker:      controllerutils.DefaultActiveOperationPrioritizingCooldown(activeOperationLister),
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	return c
}

func (c *createCluster) SyncOnce(ctx context.Context, key controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	existingCluster, err := c.cosmosClient.HCPClusters(key.SubscriptionID, key.ResourceGroupName).Get(ctx, key.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, no work to do
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	if existingCluster.ServiceProviderProperties.ClusterServiceID

	existingServiceProviderCluster, err := controllerutils.GetOrCreateServiceProviderCluster(ctx, c.cosmosClient, key.GetResourceID())
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get or create ServiceProviderCluster: %w", err))
	}

	if err := serverutils.DumpDataToLogger(ctx, c.cosmosClient, key.GetResourceID()); err != nil {
		// never fail, this is best effort
		logger.Error(err, "failed to dump data to logger")
	}

	return nil
}

func (c *createCluster) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
