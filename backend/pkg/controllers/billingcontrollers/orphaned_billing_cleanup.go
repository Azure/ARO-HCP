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

package billingcontrollers

import (
	"context"
	"net/http"
	"time"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type orphanedBillingCleanup struct {
	clock           utilsclock.PassiveClock
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient
}

// NewOrphanedBillingCleanupController creates a controller that marks billing documents
// as deleted when their corresponding cluster no longer exists in Cosmos DB.
func NewOrphanedBillingCleanupController(clock utilsclock.PassiveClock, cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	c := &orphanedBillingCleanup{
		clock:           clock,
		cooldownChecker: controllerutils.NewTimeBasedCooldownChecker(60 * time.Minute), // Run once per hour max
		cosmosClient:    cosmosClient,
	}

	return c
}

func (c *orphanedBillingCleanup) synchronizeCluster(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	// Try to get the cluster from Cosmos
	_, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	clusterExists := true
	if database.IsResponseError(err, http.StatusNotFound) {
		clusterExists = false
	} else if err != nil {
		return utils.TrackError(err)
	}

	// If cluster exists, nothing to do
	if clusterExists {
		return nil
	}

	logger.Info("cluster not found in Cosmos, checking for orphaned billing documents",
		"subscriptionID", keyObj.SubscriptionID,
		"resourceGroupName", keyObj.ResourceGroupName,
		"clusterName", keyObj.HCPClusterName,
	)

	// Reconstruct the cluster resource ID
	clusterResourceID := database.NewClusterResourceID(keyObj.SubscriptionID, keyObj.ResourceGroupName, keyObj.HCPClusterName)

	// Query for active billing documents for this cluster (without deletionTime)
	billingDocs, err := c.cosmosClient.GetActiveBillingDocsForCluster(ctx, clusterResourceID)
	if err != nil {
		return utils.TrackError(err)
	}

	if len(billingDocs) == 0 {
		// No orphaned billing documents found
		return nil
	}

	logger.Info("found orphaned billing documents, marking as deleted",
		"clusterResourceID", clusterResourceID,
		"count", len(billingDocs),
	)

	// Mark all orphaned billing documents as deleted
	deletionTime := c.clock.Now()
	for _, doc := range billingDocs {
		if doc.DeletionTime != nil {
			// Already has a deletion time, skip
			continue
		}

		logger.Info("marking orphaned billing document as deleted",
			"billingDocID", doc.ID,
			"billingCreationTime", doc.CreationTime,
		)

		var patchOperations database.BillingDocumentPatchOperations
		patchOperations.SetDeletionTime(deletionTime)
		err := c.cosmosClient.PatchBillingDocByID(ctx, doc.SubscriptionID, doc.ID, patchOperations)
		if err != nil {
			logger.Error(err, "failed to mark billing document as deleted",
				"billingDocID", doc.ID,
			)
			return utils.TrackError(err)
		}

		logger.Info("successfully marked billing document as deleted",
			"billingDocID", doc.ID,
			"deletionTime", deletionTime,
		)
	}

	return nil
}

func (c *orphanedBillingCleanup) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeCluster(ctx, keyObj)
	return utils.TrackError(syncErr)
}

func (c *orphanedBillingCleanup) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
