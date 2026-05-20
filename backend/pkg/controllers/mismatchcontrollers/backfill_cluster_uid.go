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
	"fmt"
	"time"

	"github.com/google/uuid"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	controllerutil "github.com/Azure/ARO-HCP/internal/controllerutils"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type backfillClusterUID struct {
	clock             utilsclock.PassiveClock
	cooldownChecker   controllerutil.CooldownChecker
	clusterLister     listers.ClusterLister
	resourcesDBClient database.ResourcesDBClient
	billingDBClient   database.BillingDBClient
}

// NewBackfillClusterUIDController creates a controller that populates ClusterUID
// for existing clusters that don't have it set.
func NewBackfillClusterUIDController(clock utilsclock.PassiveClock, resourcesDBClient database.ResourcesDBClient, billingDBClient database.BillingDBClient, clusterLister listers.ClusterLister) controllerutils.ClusterSyncer {
	c := &backfillClusterUID{
		clock:             clock,
		cooldownChecker:   controllerutil.NewTimeBasedCooldownChecker(60 * time.Minute),
		clusterLister:     clusterLister,
		resourcesDBClient: resourcesDBClient,
		billingDBClient:   billingDBClient,
	}

	return c
}

func (c *backfillClusterUID) NeedsWork(ctx context.Context, existingCluster *api.HCPOpenShiftCluster) bool {
	// Skip if the cluster is deleted or already has ClusterUID.
	if existingCluster == nil || len(existingCluster.ServiceProviderProperties.ClusterUID) != 0 {
		return false
	}

	return true
}

func (c *backfillClusterUID) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cachedCluster, err := c.clusterLister.Get(ctx, keyObj.SubscriptionID, keyObj.ResourceGroupName, keyObj.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get cluster from cache: %w", err))
	}
	if !c.NeedsWork(ctx, cachedCluster) {
		// if the cache doesn't need work, then we'll be retriggered if those values change when the cache updates.
		// if the values don't change, then we still have no work to do.
		return nil
	}

	clusterCRUD := c.resourcesDBClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName)
	existingCluster, err := clusterCRUD.Get(ctx, keyObj.HCPClusterName)
	if database.IsNotFoundError(err) {
		return nil
	}
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to get Cluster: %w", err))
	}
	// check if we need to do work again. Sometimes the live data is more fresh than the cache and obviates the need to any work
	if !c.NeedsWork(ctx, existingCluster) {
		return nil
	}

	logger.Info("backfilling ClusterUID for cluster",
		"clusterResourceID", existingCluster.ID,
	)

	billingDocs, err := c.billingDBClient.BillingDocs(existingCluster.ID.SubscriptionID).ListActiveForCluster(ctx, existingCluster.ID)
	if err != nil {
		return utils.TrackError(err)
	}

	var billingDoc *database.BillingDocument
	for _, doc := range billingDocs {
		if existingCluster.SystemData.CreatedAt.Equal(doc.CreationTime) {
			billingDoc = doc
			break
		}
	}

	var clusterUID string
	if billingDoc == nil {
		logger.Info("no existing billing document found matching creation time, generating new ClusterUID",
			"clusterCreationTime", existingCluster.SystemData.CreatedAt,
		)
		clusterUID = uuid.New().String()
	} else {
		clusterUID = billingDoc.ID
		logger.Info("found billing document matching creation time, using its ID",
			"clusterUID", clusterUID,
			"billingCreationTime", billingDoc.CreationTime,
		)
	}

	existingCluster.ServiceProviderProperties.ClusterUID = clusterUID

	_, err = c.resourcesDBClient.HCPClusters(existingCluster.ID.SubscriptionID, existingCluster.ID.ResourceGroupName).Replace(ctx, existingCluster, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("successfully backfilled ClusterUID for cluster",
		"clusterResourceID", existingCluster.ID,
		"clusterUID", existingCluster.ServiceProviderProperties.ClusterUID,
	)

	return nil
}

func (c *backfillClusterUID) CooldownChecker() controllerutil.CooldownChecker {
	return c.cooldownChecker
}
