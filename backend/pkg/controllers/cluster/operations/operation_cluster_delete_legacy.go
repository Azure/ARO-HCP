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

package operations

import (
	"context"
	"errors"
	"net/http"
	"strings"

	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	sharedops "github.com/Azure/ARO-HCP/backend/pkg/controllers/shared/operations"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (c *operationClusterDelete) legacyShouldProcess(ctx context.Context, operation *api.Operation) bool {
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

func (c *operationClusterDelete) legacySynchronizeOperation(ctx context.Context, operation *api.Operation) error {
	logger := utils.LoggerFromContext(ctx)

	if !c.legacyShouldProcess(ctx, operation) {
		return nil // no work to do
	}

	if len(operation.InternalID.String()) == 0 {
		return nil
	}

	clusterStatus, err := c.clusterServiceClient.GetClusterStatus(ctx, operation.InternalID)
	var ocmGetClusterError *ocmerrors.Error
	if err != nil && errors.As(err, &ocmGetClusterError) && ocmGetClusterError.Status() == http.StatusNotFound {
		logger.Info("cluster was deleted")

		// Some nodepool controllers require of the Cosmos document to do their cleanup work so we block
		// the cluster cosmos deletion until the nodepools are gone.
		nodePoolIterator, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).NodePools(operation.ExternalID.Name).List(ctx, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		foundAtLeastOneNodePool := false
		for range nodePoolIterator.Items(ctx) {
			foundAtLeastOneNodePool = true
			break
		}
		err = nodePoolIterator.GetError()
		if err != nil {
			return utils.TrackError(err)
		}
		if foundAtLeastOneNodePool {
			logger.Info("cluster still has cosmos nodepools")
			return nil
		}

		// Some external auth controllers require the Cosmos document to do their cleanup work, so we block
		// the cluster cosmos deletion until the external auths are gone.
		externalAuthIterator, err := c.resourcesDBClient.HCPClusters(operation.ExternalID.SubscriptionID, operation.ExternalID.ResourceGroupName).ExternalAuth(operation.ExternalID.Name).List(ctx, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		foundAtLeastOneExternalAuth := false
		for range externalAuthIterator.Items(ctx) {
			foundAtLeastOneExternalAuth = true
			break
		}
		err = externalAuthIterator.GetError()
		if err != nil {
			return utils.TrackError(err)
		}
		if foundAtLeastOneExternalAuth {
			logger.Info("cluster still has cosmos externalauths")
			return nil
		}

		// Update the Cosmos DB billing document with a deletion timestamp.
		err = controllerutils.MarkBillingDocumentDeleted(ctx, c.billingDBClient, operation.ExternalID, c.clock.Now())
		if errors.Is(err, database.ErrAmbiguousResult) {
			logger.Error(err, "Failed to mark CosmosDB billing record for deletion")
		} else if err != nil {
			return utils.TrackError(err)
		}

		err = sharedops.SetDeleteOperationAsCompleted(ctx, c.clock, c.resourcesDBClient, operation, sharedops.PostAsyncNotificationFn(c.notificationClient))
		if err != nil {
			return utils.TrackError(err)
		}
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := sharedops.ConvertClusterStatus(ctx, c.clusterServiceClient, operation, clusterStatus)
	if err != nil {
		return utils.TrackError(err)
	}

	err = sharedops.UpdateOperationStatus(ctx, c.clock, c.resourcesDBClient, operation, newOperationStatus, newOperationError, sharedops.PostAsyncNotificationFn(c.notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
