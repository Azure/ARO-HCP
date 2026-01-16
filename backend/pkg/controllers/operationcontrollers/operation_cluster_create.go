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

package operationcontrollers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterCreate struct {
	azureLocation        string
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationClusterCreateSynchronizer periodically lists all clusters and for each out when the cluster was created and its state.
func NewOperationClusterCreateSynchronizer(
	azureLocation string,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	c := &operationClusterCreate{
		azureLocation:        azureLocation,
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	return c
}

func (c *operationClusterCreate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestCreate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterCreate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("checking operation")

	operation, err := c.cosmosClient.Operations(key.SubscriptionID).Get(ctx, key.OperationName)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil // no work to do
	}
	if err != nil {
		return fmt.Errorf("failed to get active operation: %w", err)
	}
	if !c.ShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, opError, err := oldoperationscanner.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status", "newStatus", newOperationStatus)

	// Create a Cosmos DB billing document if a Create operation is successful.
	// Do this before calling updateOperationStatus so that in case of error the
	// backend will retry by virtue of the operation document still having a non-
	// terminal status.
	if newOperationStatus == arm.ProvisioningStateSucceeded {
		cluster, err := c.cosmosClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).Get(ctx, operation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}

		logger.Info("creating billing, interestingly not based on now")
		err = c.createBillingDocument(
			ctx,
			operation.ExternalID.ResourceGroupName,
			ptr.Deref(cluster.SystemData.CreatedAt, time.Time{}),
			operation)
		if err != nil {
			return utils.TrackError(err)
		}

	}

	logger.Info("updating status")
	err = database.UpdateOperationStatus(ctx, c.cosmosClient, operation, newOperationStatus, opError, PostAsyncNotification(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// createBillingDocument creates a Cosmos DB document in the Billing
// container for a newly-created cluster.
func (c *operationClusterCreate) createBillingDocument(ctx context.Context, resourceGroupName string, clusterCreationTime time.Time, op *api.Operation) error {
	logger := utils.LoggerFromContext(ctx)

	if clusterCreationTime.IsZero() {
		return fmt.Errorf("cluster creation time is zero")
	}

	doc := database.NewBillingDocument(op.ExternalID)
	doc.CreationTime = clusterCreationTime
	doc.Location = c.azureLocation
	doc.TenantID = op.TenantID
	doc.ManagedResourceGroup = fmt.Sprintf(
		"/%s/%s/%s/%s",
		azcorearm.SubscriptionResourceType.Type,
		doc.SubscriptionID,
		azcorearm.ResourceGroupResourceType.Type,
		resourceGroupName)

	if err := c.cosmosClient.CreateBillingDoc(ctx, doc); err != nil {
		return utils.TrackError(err)
	}

	logger.Info("Updated billing for cluster creation")
	return nil
}
