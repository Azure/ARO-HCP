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

package database

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

var localClock clock.Clock = clock.RealClock{}

type PostAsyncNotificationFunc func(ctx context.Context, operation *api.Operation) error

// UpdateOperationStatus updates Cosmos DB to reflect an updated resource status.
func UpdateOperationStatus(ctx context.Context, cosmosClient DBClient, existingOperation *api.Operation, newOperationStatus arm.ProvisioningState, newOperationError *arm.CloudErrorBody, postAsyncNotificationFn PostAsyncNotificationFunc) error {
	logger := utils.LoggerFromContext(ctx)
	if existingOperation == nil {
		return nil
	}
	logger.Info("Updating operation status", "oldStatus", existingOperation.Status, "status", newOperationStatus, "operationError", newOperationError)

	err := PatchOperationDocument(ctx, cosmosClient, existingOperation, newOperationStatus, newOperationError, postAsyncNotificationFn)
	if err != nil {
		return utils.TrackError(err)
	}

	// TODO make this an etag based replace to avoid conflict
	switch {
	case existingOperation.ExternalID == nil:
		logger.Info("No external ID, skipping update")
		return nil

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName)
		curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}
		if existingOperation.OperationID == nil || curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
			return utils.TrackError(fmt.Errorf("precondition failed"))
		}
		if curr.ServiceProviderProperties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
			logger.Info("No update needed")
			return nil
		}
		curr.ServiceProviderProperties.ProvisioningState = newOperationStatus
		if newOperationStatus.IsTerminal() {
			curr.ServiceProviderProperties.ActiveOperationID = ""
		}

		logger.Info("Updating resource")
		if _, err := dbClient.Replace(ctx, curr, nil); err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName).NodePools(existingOperation.ExternalID.Parent.Name)
		curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}
		if existingOperation.OperationID == nil || curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
			return utils.TrackError(fmt.Errorf("precondition failed"))
		}
		if curr.Properties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
			logger.Info("No update needed")
			return nil
		}
		curr.Properties.ProvisioningState = newOperationStatus
		if newOperationStatus.IsTerminal() {
			curr.ServiceProviderProperties.ActiveOperationID = ""
		}

		logger.Info("Updating resource")
		if _, err := dbClient.Replace(ctx, curr, nil); err != nil {
			return utils.TrackError(err)
		}
		return nil

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName).ExternalAuth(existingOperation.ExternalID.Parent.Name)
		curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
		if err != nil {
			return utils.TrackError(err)
		}
		if existingOperation.OperationID == nil || curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
			return utils.TrackError(fmt.Errorf("precondition failed"))
		}
		if curr.Properties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
			logger.Info("No update needed")
			return nil
		}
		curr.Properties.ProvisioningState = newOperationStatus
		if newOperationStatus.IsTerminal() {
			curr.ServiceProviderProperties.ActiveOperationID = ""
		}

		logger.Info("Updating resource")
		if _, err := dbClient.Replace(ctx, curr, nil); err != nil {
			return utils.TrackError(err)
		}
		return nil

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", existingOperation.ExternalID.ResourceType.String()))
	}

}

// PatchOperationDocument patches the status and error fields of an OperationDocument.
func PatchOperationDocument(ctx context.Context, dbClient DBClient, oldOperation *api.Operation, newOperationStatus arm.ProvisioningState, newOperationError *arm.CloudErrorBody, postAsyncNotificationFn PostAsyncNotificationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	if len(oldOperation.NotificationURI) == 0 && oldOperation.Status == newOperationStatus && oldOperation.Error == newOperationError {
		// we rewrite the status when we missed a notification
		// if we have nothing to write, we simply return without error
		return nil
	}

	// shallow copy works since all the fields we're touching are shallow
	operationToWrite := *oldOperation
	operationToWrite.LastTransitionTime = localClock.Now()
	operationToWrite.Status = newOperationStatus
	if newOperationError != nil {
		operationToWrite.Error = newOperationError
	}

	// TODO see if we want to plumb etags through to prevent stomping.  Right now this will stomp a concurrent write.
	// we don't expect concurrent writes and the last one winning is ok.
	latestOperation, err := dbClient.Operations(operationToWrite.OperationID.SubscriptionID).Replace(ctx, &operationToWrite, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	message := fmt.Sprintf("Updated status to '%s'", newOperationStatus)
	switch newOperationStatus {
	case arm.ProvisioningStateSucceeded:
		switch latestOperation.Request {
		case OperationRequestCreate:
			message = "Resource creation succeeded"
		case OperationRequestUpdate:
			message = "Resource update succeeded"
		case OperationRequestDelete:
			message = "Resource deletion succeeded"
		case OperationRequestRequestCredential:
			message = "Credential request succeeded"
		case OperationRequestRevokeCredentials:
			message = "Credential revocation succeeded"
		}
	case arm.ProvisioningStateFailed:
		switch latestOperation.Request {
		case OperationRequestCreate:
			message = "Resource creation failed"
		case OperationRequestUpdate:
			message = "Resource update failed"
		case OperationRequestDelete:
			message = "Resource deletion failed"
		case OperationRequestRequestCredential:
			message = "Credential request failed"
		case OperationRequestRevokeCredentials:
			message = "Credential revocation failed"
		}
	}

	if newOperationError != nil {
		logger.With("cloud_error_code", newOperationError.Code, "cloud_error_message", newOperationError.Message).Error(message)
	} else {
		logger.Info(message)
	}

	if postAsyncNotificationFn != nil && newOperationStatus.IsTerminal() && len(latestOperation.NotificationURI) > 0 {
		err = postAsyncNotificationFn(ctx, latestOperation)
		if err == nil {
			logger.Info("Posted async notification")

			// Remove the notification URI from the document
			// so the ARM notification is only sent once.
			operationWithoutNotificationURI := *latestOperation
			operationWithoutNotificationURI.NotificationURI = ""
			_, err = dbClient.Operations(operationToWrite.OperationID.SubscriptionID).Replace(ctx, &operationWithoutNotificationURI, nil)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to clear notification URI: %v", err))
			}
		} else {
			logger.Error(fmt.Sprintf("Failed to post async notification: %v", err.Error()))
		}
	}

	return nil
}
