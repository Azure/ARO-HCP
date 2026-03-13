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
	"net/http"
	"time"

	"github.com/google/uuid"

	utilsclock "k8s.io/utils/clock"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type backfillBillingDocID struct {
	clock           utilsclock.PassiveClock
	cooldownChecker controllerutils.CooldownChecker
	cosmosClient    database.DBClient
}

// NewBackfillBillingDocIDController creates a controller that populates BillingDocID
// for existing clusters that don't have it set. This is a temporary controller that
// will be removed once all legacy clusters have been migrated.
func NewBackfillBillingDocIDController(clock utilsclock.PassiveClock, cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	c := &backfillBillingDocID{
		clock:           clock,
		cooldownChecker: controllerutils.NewTimeBasedCooldownChecker(60 * time.Minute), // Run once per hour max per cluster
		cosmosClient:    cosmosClient,
	}

	return c
}

func (c *backfillBillingDocID) synchronizeCluster(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, nothing to backfill
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Check if BillingDocID is already set
	if cluster.ServiceProviderProperties.BillingDocID != "" {
		return nil // already has a BillingDocID, nothing to do
	}

	logger.Info("backfilling BillingDocID for cluster",
		"clusterResourceID", cluster.ID,
		"clusterServiceID", cluster.ServiceProviderProperties.ClusterServiceID,
	)

	// Query for an existing billing document for this cluster matching the creation time
	billingDoc, err := c.getBillingDocumentForClusterByCreationTime(ctx, cluster.ID, cluster.SystemData.CreatedAt)
	if err != nil {
		return utils.TrackError(err)
	}

	var billingDocID string
	if billingDoc == nil {
		// No billing document found matching creation time, generate a new UUID
		logger.Info("no existing billing document found matching creation time, generating new BillingDocID",
			"clusterCreationTime", cluster.SystemData.CreatedAt,
		)
		billingDocID = uuid.New().String()
	} else {
		// Billing document found, use its ID
		billingDocID = billingDoc.ID
		logger.Info("found billing document matching creation time, using its ID",
			"billingDocID", billingDocID,
			"billingCreationTime", billingDoc.CreationTime,
		)
	}

	// Update the cluster with the BillingDocID
	cluster.ServiceProviderProperties.BillingDocID = billingDocID

	// Update the cluster in Cosmos
	transaction := c.cosmosClient.NewTransaction(cluster.ID.SubscriptionID)
	_, err = c.cosmosClient.HCPClusters(cluster.ID.SubscriptionID, cluster.ID.ResourceGroupName).AddReplaceToTransaction(ctx, transaction, cluster, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("successfully backfilled BillingDocID for cluster",
		"clusterResourceID", cluster.ID,
		"billingDocID", cluster.ServiceProviderProperties.BillingDocID,
	)

	return nil
}

// getBillingDocumentForClusterByCreationTime queries the billing container for a billing document
// matching the given cluster resource ID and creation time (without a deletion timestamp).
func (c *backfillBillingDocID) getBillingDocumentForClusterByCreationTime(ctx context.Context, resourceID *azcorearm.ResourceID, creationTime *time.Time) (*database.BillingDocument, error) {
	if creationTime == nil {
		return nil, nil
	}
	return c.cosmosClient.GetBillingDocForClusterByCreationTime(ctx, resourceID, *creationTime)
}

func (c *backfillBillingDocID) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeCluster(ctx, keyObj)
	return utils.TrackError(syncErr)
}

func (c *backfillBillingDocID) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
