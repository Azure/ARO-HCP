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

package billingcontrollers

import (
	"context"
	"errors"
	"fmt"
	"time"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type createBillingDoc struct {
	clock             utilsclock.PassiveClock
	cooldownChecker   controllerutils.CooldownChecker
	azureLocation     string
	clusterLister     listers.ClusterLister
	billingLister     listers.BillingLister
	resourcesDBClient database.ResourcesDBClient
	billingDBClient   database.BillingDBClient
}

// NewCreateBillingDocController creates a controller that ensures a billing document
// exists for clusters that have a ClusterUID and are in the Succeeded provisioning state.
func NewCreateBillingDocController(clock utilsclock.PassiveClock, azureLocation string, resourcesDBClient database.ResourcesDBClient, billingDBClient database.BillingDBClient, clusterLister listers.ClusterLister, billingLister listers.BillingLister) controllerutils.ClusterSyncer {
	return &createBillingDoc{
		clock:             clock,
		cooldownChecker:   controllerutils.NewTimeBasedCooldownChecker(60 * time.Second),
		azureLocation:     azureLocation,
		clusterLister:     clusterLister,
		billingLister:     billingLister,
		resourcesDBClient: resourcesDBClient,
		billingDBClient:   billingDBClient,
	}
}

func (c *createBillingDoc) NeedsWork(ctx context.Context, existingCluster *api.HCPOpenShiftCluster) bool {
	// Skip if the cluster is deleted or does not have a ClusterUID because the cluster is old and yet-to-be backfilled old data (backfill controller's responsibility).
	// All new clusters will have it from admission.
	if existingCluster == nil || len(existingCluster.ServiceProviderProperties.ClusterUID) == 0 {
		return false
	}

	// Skip if the billing document has already been created (BillingDocumentCosmosID is set)
	if len(existingCluster.ServiceProviderProperties.BillingDocumentCosmosID) != 0 {
		return false
	}

	// Skip if the cluster provision is not succeeded yet.
	if existingCluster.ServiceProviderProperties.ProvisioningState != arm.ProvisioningStateSucceeded {
		return false
	}

	return true
}

func (c *createBillingDoc) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := keyObj.AddLoggerValues(utils.LoggerFromContext(ctx))

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

	logger.Info("ensuring billing document exists",
		"clusterUID", existingCluster.ServiceProviderProperties.ClusterUID,
	)

	billingDocCRUD := c.billingDBClient.BillingDocs(existingCluster.ID.SubscriptionID)
	clusterUID := existingCluster.ServiceProviderProperties.ClusterUID

	// Try cache first
	doc, err := c.billingLister.GetByID(ctx, clusterUID)
	if err != nil && !database.IsNotFoundError(err) {
		return utils.TrackError(fmt.Errorf("failed to get billing document from cache: %w", err))
	}

	// If not in cache, check database
	if doc == nil {
		doc, err = billingDocCRUD.GetByID(ctx, clusterUID)
		if err != nil && !database.IsNotFoundError(err) {
			return utils.TrackError(fmt.Errorf("failed to get billing document from database: %w", err))
		}
	}

	// doc will be non-nil if it was found in either the cache or the database. If it wasn't found in either place, we'll create it.
	if doc == nil {
		// Billing document doesn't exist yet, create it
		subscription, err := c.resourcesDBClient.Subscriptions().Get(ctx, existingCluster.ID.SubscriptionID)
		if err != nil {
			return utils.TrackError(err)
		}

		fallbackTime := c.clock.Now()
		creationTime := ptr.Deref(existingCluster.SystemData.CreatedAt, fallbackTime)
		if creationTime.IsZero() {
			return utils.TrackError(errors.New("cluster creation time is zero"))
		}

		doc = database.NewBillingDocument(clusterUID, existingCluster.ID)
		doc.CreationTime = creationTime
		doc.Location = c.azureLocation
		if subscription.Properties != nil {
			doc.TenantID = ptr.Deref(subscription.Properties.TenantId, "")
		}
		managedRGName := existingCluster.ID.ResourceGroupName
		if len(existingCluster.CustomerProperties.Platform.ManagedResourceGroup) != 0 {
			managedRGName = existingCluster.CustomerProperties.Platform.ManagedResourceGroup
		}
		doc.ManagedResourceGroup = api.ToResourceGroupResourceIDString(existingCluster.ID.SubscriptionID, managedRGName)

		err = billingDocCRUD.Create(ctx, doc)
		if err != nil {
			return utils.TrackError(err)
		}

		logger.Info("created billing document for cluster",
			"clusterUID", clusterUID,
			"billingDocID", doc.ID,
		)
	}

	// Update the cluster to record the billing document ID (whether newly created or already existing)
	existingCluster.ServiceProviderProperties.BillingDocumentCosmosID = doc.ID
	_, err = clusterCRUD.Replace(ctx, existingCluster, nil)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to update cluster with billing document ID: %w", err))
	}

	logger.Info("updated cluster with billing document ID",
		"clusterUID", clusterUID,
		"billingDocID", doc.ID,
	)

	return nil
}

func (c *createBillingDoc) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
