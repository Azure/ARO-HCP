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

package mismatchcontrollers

import (
	"context"
	"errors"
	"net/http"

	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosClusterMatching struct {
	clock                utilsclock.PassiveClock
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

// NewCosmosClusterMatchingController periodically looks for mismatched cluster-service and cosmos externalauths
func NewCosmosClusterMatchingController(clock utilsclock.PassiveClock, cosmosClient database.DBClient, clusterServiceClient ocm.ClusterServiceClientSpec) controllerutils.ClusterSyncer {
	c := &cosmosClusterMatching{
		clock:                clock,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
	}

	return c
}

func (c *cosmosClusterMatching) synchronizeClusters(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cosmosCluster, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = c.clusterServiceClient.GetCluster(ctx, cosmosCluster.ServiceProviderProperties.ClusterServiceID)
	var ocmGetClusterError *ocmerrors.Error
	isClusterServiceObjNotFound := errors.As(err, &ocmGetClusterError) && ocmGetClusterError.Status() == http.StatusNotFound
	if err != nil && !isClusterServiceObjNotFound {
		return utils.TrackError(err)
	}
	if err == nil {
		return nil
	}

	logger.Error(nil, "cosmos cluster doesn't have matching cluster-service cluster",
		"cosmosResourceID", cosmosCluster.ID,
		"clusterServiceID", cosmosCluster.ServiceProviderProperties.ClusterServiceID,
	)

	// we need to cleanup the cosmosCluster, finalizing billing first
	if err := controllerutils.MarkBillingDocumentDeleted(ctx, c.cosmosClient, cosmosCluster.ID, c.clock.Now()); err != nil {
		return utils.TrackError(err)
	}

	if err := controllerutils.DeleteRecursively(ctx, c.cosmosClient, cosmosCluster.ID); err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *cosmosClusterMatching) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeClusters(ctx, keyObj)
	return utils.TrackError(syncErr)
}
