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
	"fmt"
	"net/http"
	"time"

	utilsclock "k8s.io/utils/clock"
	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type createBillingDoc struct {
	clock           utilsclock.PassiveClock
	cooldownChecker controllerutils.CooldownChecker
	azureLocation   string
	cosmosClient    database.DBClient
}

// NewCreateBillingDocController creates a controller that ensures a billing document
// exists for clusters that have a BillingDocID and are in the Succeeded provisioning state.
func NewCreateBillingDocController(clock utilsclock.PassiveClock, azureLocation string, cosmosClient database.DBClient) controllerutils.ClusterSyncer {
	return &createBillingDoc{
		clock:           clock,
		cooldownChecker: controllerutils.NewTimeBasedCooldownChecker(60 * time.Minute),
		azureLocation:   azureLocation,
		cosmosClient:    cosmosClient,
	}
}

func (c *createBillingDoc) synchronizeCluster(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	logger := utils.LoggerFromContext(ctx)

	cluster, err := c.cosmosClient.HCPClusters(keyObj.SubscriptionID, keyObj.ResourceGroupName).Get(ctx, keyObj.HCPClusterName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // cluster doesn't exist, nothing to do
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Skip if the cluster doesn't have a BillingDocID yet (backfill controller's responsibility)
	if cluster.ServiceProviderProperties.BillingDocID == "" {
		return nil
	}

	// Only create billing documents for clusters in the Succeeded provisioning state
	if cluster.ServiceProviderProperties.ProvisioningState != arm.ProvisioningStateSucceeded {
		return nil
	}

	logger.Info("ensuring billing document exists",
		"clusterResourceID", cluster.ID,
		"billingDocID", cluster.ServiceProviderProperties.BillingDocID,
	)

	subscription, err := c.cosmosClient.Subscriptions().Get(ctx, cluster.ID.SubscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	// Use fallback time when createdAt is missing
	fallbackTime := c.clock.Now()
	creationTime := ptr.Deref(cluster.SystemData.CreatedAt, fallbackTime)
	if creationTime.IsZero() {
		return fmt.Errorf("cluster creation time is zero")
	}

	doc := database.NewBillingDocument(cluster.ServiceProviderProperties.BillingDocID, cluster.ID)
	doc.CreationTime = creationTime
	doc.Location = c.azureLocation
	doc.TenantID = ptr.Deref(subscription.Properties.TenantId, "")
	doc.ManagedResourceGroup = fmt.Sprintf(
		"/%s/%s/%s/%s",
		azcorearm.SubscriptionResourceType.Type,
		cluster.ID.SubscriptionID,
		azcorearm.ResourceGroupResourceType.Type,
		cluster.ID.ResourceGroupName)

	err = c.cosmosClient.CreateBillingDoc(ctx, doc)
	if database.IsResponseError(err, http.StatusConflict) {
		// Billing document already exists, nothing to do
		logger.Info("billing document already exists",
			"billingDocID", cluster.ServiceProviderProperties.BillingDocID,
		)
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info("created billing document for cluster",
		"clusterResourceID", cluster.ID,
		"billingDocID", cluster.ServiceProviderProperties.BillingDocID,
	)
	return nil
}

func (c *createBillingDoc) SyncOnce(ctx context.Context, keyObj controllerutils.HCPClusterKey) error {
	syncErr := c.synchronizeCluster(ctx, keyObj)
	return utils.TrackError(syncErr)
}

func (c *createBillingDoc) CooldownChecker() controllerutils.CooldownChecker {
	return c.cooldownChecker
}
