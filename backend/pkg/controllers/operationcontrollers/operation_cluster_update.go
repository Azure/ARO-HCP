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

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterUpdate struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationClusterUpdateSynchronizer periodically lists all clusters and for each out when the cluster was created and its state.
func NewOperationClusterUpdateSynchronizer(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	c := &operationClusterUpdate{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	return c
}

func (c *operationClusterUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	newOperationStatus, opError, err := convertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = UpdateOperationStatus(ctx, c.cosmosClient, operation, newOperationStatus, opError, postAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
