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
	"time"

	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type cosmosClusterMatching struct {
	clock                utilsclock.PassiveClock
	cooldownChecker      controllerutils.CooldownChecker
	resourcesDBClient    database.ResourcesDBClient
	billingDBClient      database.BillingDBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
}

// NewCosmosClusterMatchingController periodically looks for mismatched cluster-service and cosmos externalauths
func NewCosmosClusterMatchingController(clock utilsclock.PassiveClock, resourcesDBClient database.ResourcesDBClient, billingDBClient database.BillingDBClient, clusterServiceClient ocm.ClusterServiceClientSpec, informers informers.BackendInformers) controllerutils.Controller {
	syncer := &cosmosClusterMatching{
		clock:                clock,
		cooldownChecker:      controllerutils.NewTimeBasedCooldownChecker(1 * time.Hour),
		resourcesDBClient:    resourcesDBClient,
		billingDBClient:      billingDBClient,
		clusterServiceClient: clusterServiceClient,
	}

	controller := controllerutils.NewClusterWatchingController(
		"CosmosMatchingClusters",
		resourcesDBClient,
		informers,
		60*time.Minute,
		syncer,
	)

	return controller
}

func (c *cosmosClusterMatching) synchronizeClusters(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cosmosCluster, err := c.resourcesDBClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil // no work to do
	}
	if err != nil {
		return utils.TrackError(err)
	}
	if cosmosCluster.ServiceProviderProperties.ClusterServiceID == nil {
		// no work to do because clusters start without clusterServiceIDs and that means we haven't got an orphan
		return nil
	}

	_, err = c.clusterServiceClient.GetCluster(ctx, *cosmosCluster.ServiceProviderProperties.ClusterServiceID)
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
	if err := controllerutils.MarkBillingDocumentDeleted(ctx, c.billingDBClient, cosmosCluster.ID, c.clock.Now()); err != nil {
		// We are purposefully ignoring billing document errors while the cardinality of billing documents
		// is being addressed to ensure that one billing document corresponds with one resourceID/cluster doc
		logger.Error(err, "failed to mark billing document as deleted",
			"cosmosResourceID", cosmosCluster.ID,
			"clusterServiceID", cosmosCluster.ServiceProviderProperties.ClusterServiceID,
		)
	}

	if err := controllerutils.DeleteRecursively(ctx, c.resourcesDBClient, cosmosCluster.ID); err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (c *cosmosClusterMatching) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeClusters(ctx, keyObj)
	return utils.TrackError(syncErr)
}

func (c *cosmosClusterMatching) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
