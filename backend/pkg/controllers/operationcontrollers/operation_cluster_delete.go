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
	"errors"
	"fmt"
	"net/http"
	"strings"

	utilsclock "k8s.io/utils/clock"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type operationClusterDelete struct {
	clock                utilsclock.PassiveClock
	cosmosClient         database.DBClient
	clusterServiceClient ocm.ClusterServiceClientSpec
	notificationClient   *http.Client
}

// NewOperationClusterDeleteSynchronizer periodically lists all clusters and for each out when the cluster was deleted and its state.
func NewOperationClusterDeleteSynchronizer(
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	notificationClient *http.Client,
) OperationSynchronizer {
	c := &operationClusterDelete{
		clock:                utilsclock.RealClock{},
		cosmosClient:         cosmosClient,
		clusterServiceClient: clusterServiceClient,
		notificationClient:   notificationClient,
	}

	return c
}

func (c *operationClusterDelete) ShouldProcess(ctx context.Context, operation *api.Operation) bool {
	if operation.Status.IsTerminal() {
		return false
	}
	if operation.Request != database.OperationRequestDelete {
		return false
	}
	if operation.ExternalID == nil || !strings.EqualFold(operation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()) {
		return false
	}
	return true
}

func (c *operationClusterDelete) SynchronizeOperation(ctx context.Context, key controllerutils.OperationKey) error {
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
	var ocmGetClusterError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetClusterError) && ocmGetClusterError.Status() == http.StatusNotFound {
		logger.Info("cluster was deleted")

		// Update the Cosmos DB billing document with a deletion timestamp.
		// Do this before calling setDeleteOperationAsCompleted so that in
		// case of error the backend will retry by virtue of the operation
		// document still having a non-terminal status.
		err = controllerutils.MarkBillingDocumentDeleted(ctx, c.cosmosClient, operation.ExternalID, c.clock.Now())
		if err != nil {
			return utils.TrackError(err)
		}

		err = oldoperationscanner.SetDeleteOperationAsCompleted(ctx, c.cosmosClient, operation, PostAsyncNotification(c.notificationClient))
		if err != nil {
			logger.Error(err, "Failed to handle a completed deletion")
		}
	}
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := oldoperationscanner.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = database.UpdateOperationStatus(ctx, c.cosmosClient, operation, newOperationStatus, newOperationError, PostAsyncNotification(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
