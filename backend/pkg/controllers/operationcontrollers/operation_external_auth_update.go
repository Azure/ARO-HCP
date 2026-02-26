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

type operationExternalAuthUpdate struct {
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

func NewOperationExternalAuthUpdateSynchronizer(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	c := &operationExternalAuthUpdate{
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	return c
}

func (c *operationExternalAuthUpdate) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestUpdate {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()) {
		return false
	}
	return true
}

func (c *operationExternalAuthUpdate) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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

	_, err = c.clusterServiceClient.GetExternalAuth(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	return pollExternalAuthStatus(ctx, c.cosmosClient, c.clusterServiceClient, operation, c.notificationClient)
}
