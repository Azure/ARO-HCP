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

package systemadmincredentialcontrollers

import (
	"context"
	"net/http"

	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// patchOperationStatus updates an Operation's Status / Error fields if
// they differ from the desired values, and sends the ARM async
// notification callback when the operation reaches a terminal state.
//
// This mirrors the behaviour of operationcontrollers.patchOperation /
// notifyOperationOwner so that ARM's NotificationURI is POSTed and then
// cleared, preventing the customer from missing the terminal-state
// callback. The notificationClient is used to POST the notification;
// pass nil to skip notifications (e.g. in tests).
func patchOperationStatus(
	ctx context.Context,
	clock utilsclock.PassiveClock,
	resourcesDBClient database.ResourcesDBClient,
	oldOp *api.Operation,
	newStatus arm.ProvisioningState,
	newError *arm.CloudErrorBody,
	notificationClient *http.Client,
) error {
	needsNotification := len(oldOp.NotificationURI) > 0 && newStatus.IsTerminal()
	if oldOp.Status == newStatus && oldOp.Error == newError && !needsNotification {
		return nil
	}
	updated := oldOp.DeepCopy()
	updated.LastTransitionTime = clock.Now()
	updated.Status = newStatus
	if newError != nil {
		updated.Error = newError
	}
	latestOp, err := resourcesDBClient.Operations(updated.OperationID.SubscriptionID).Replace(ctx, updated, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	// Send the ARM async notification and clear the NotificationURI,
	// matching operationcontrollers.notifyOperationOwner semantics.
	if notificationClient != nil && latestOp.Status.IsTerminal() && len(latestOp.NotificationURI) > 0 {
		logger := utils.LoggerFromContext(ctx)
		if err := operationcontrollers.PostAsyncNotification(ctx, notificationClient, latestOp); err != nil {
			logger.Error(err, "Failed to post async notification")
		} else {
			logger.Info("Posted async notification")
			operationsCRUD := resourcesDBClient.Operations(latestOp.OperationID.SubscriptionID)
			currentOp, err := operationsCRUD.Get(ctx, latestOp.OperationID.Name)
			if err != nil {
				logger.Error(err, "Failed to re-read operation to clear notification URI")
			} else {
				replacement := currentOp.DeepCopy()
				replacement.NotificationURI = ""
				if _, err := operationsCRUD.Replace(ctx, replacement, nil); err != nil {
					logger.Error(err, "Failed to clear notification URI")
				}
			}
		}
	}
	return nil
}
