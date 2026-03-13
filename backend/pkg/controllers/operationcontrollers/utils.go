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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"

	"k8s.io/utils/clock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

const (
	InflightChecksFailedProvisionErrorCode = "OCM4001"
)

var localClock clock.Clock = clock.RealClock{}

type PostAsyncNotificationFunc func(ctx context.Context, operation *api.Operation) error

// Copied from uhc-clusters-service, because the
// OCM SDK does not define this for some reason.
type NodePoolStateValue string

const (
	NodePoolStateValidating       NodePoolStateValue = "validating"
	NodePoolStatePending          NodePoolStateValue = "pending"
	NodePoolStateInstalling       NodePoolStateValue = "installing"
	NodePoolStateReady            NodePoolStateValue = "ready"
	NodePoolStateUpdating         NodePoolStateValue = "updating"
	NodePoolStateValidatingUpdate NodePoolStateValue = "validating_update"
	NodePoolStatePendingUpdate    NodePoolStateValue = "pending_update"
	NodePoolStateUninstalling     NodePoolStateValue = "uninstalling"
	NodePoolStateRecoverableError NodePoolStateValue = "recoverable_error"
	NodePoolStateError            NodePoolStateValue = "error"
)

// UpdateOperationStatus updates Cosmos DB to reflect an updated resource status.
// If the operation has an associated resource, both documents are updated
// atomically using a transactional batch to prevent a window where the
// operation shows a terminal status but the resource still reflects the
// previous provisioning state.
//
// The resource update is skipped (but the operation is still updated) when:
//   - the operation has no ExternalID (no associated resource)
//   - the resource document was deleted (404 not found)
//   - a different operation now owns the resource (ActiveOperationID mismatch)
//   - the resource is already at the target non-terminal provisioning state
//
// In all of these cases the operation document is still persisted and ARM is
// notified, so the operation reaches its terminal state and does not get stuck.
func UpdateOperationStatus(ctx context.Context, cosmosClient database.DBClient, existingOperation *api.Operation, newOperationStatus arm.ProvisioningState, newOperationError *arm.CloudErrorBody, postAsyncNotificationFn PostAsyncNotificationFunc) error {
	logger := utils.LoggerFromContext(ctx)
	if existingOperation == nil {
		return nil
	}

	if !needToPatchOperation(existingOperation, newOperationStatus, newOperationError) {
		return nil
	}

	updatedOperation := existingOperation.DeepCopy()
	updatedOperation.LastTransitionTime = localClock.Now()
	updatedOperation.Status = newOperationStatus
	if newOperationError != nil {
		updatedOperation.Error = newOperationError
	}

	// Create a transaction to atomically update operation and resource documents.
	// All documents in the transaction must share the same partition key. Both
	// operation and resource documents are partitioned by subscription ID. If the
	// partition key scheme changes the transaction creation here must be updated accordingly.
	transaction := cosmosClient.NewTransaction(updatedOperation.OperationID.SubscriptionID)

	// Add the operation document replace to the transaction.
	if _, err := cosmosClient.Operations(updatedOperation.OperationID.SubscriptionID).AddReplaceToTransaction(ctx, transaction, updatedOperation, nil); err != nil {
		return utils.TrackError(err)
	}

	// Conditionally add a resource document update to the transaction.
	// The resource update is skipped in several edge cases
	// but the operation document is always updated via the transaction below.
	logger.Info("Updating external ID", "externalID", existingOperation.ExternalID)
	switch {
	case existingOperation.ExternalID == nil:
		// No associated resource document to update.
		logger.Info("No external ID, skipping update")

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.ClusterResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName)
		updated, err := getClusterForUpdate(ctx, logger, dbClient, existingOperation, newOperationStatus)
		if err != nil {
			return err
		}
		if updated != nil {
			logger.Info("Updating resource", "activeOperationID", updated.ServiceProviderProperties.ActiveOperationID, "newStatus", newOperationStatus)
			if _, err := dbClient.AddReplaceToTransaction(ctx, transaction, updated, nil); err != nil {
				return utils.TrackError(err)
			}
		}

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.NodePoolResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName).NodePools(existingOperation.ExternalID.Parent.Name)
		updated, err := getNodePoolForUpdate(ctx, logger, dbClient, existingOperation, newOperationStatus)
		if err != nil {
			return err
		}
		if updated != nil {
			logger.Info("Updating resource", "activeOperationID", updated.ServiceProviderProperties.ActiveOperationID, "newStatus", newOperationStatus)
			if _, err := dbClient.AddReplaceToTransaction(ctx, transaction, updated, nil); err != nil {
				return utils.TrackError(err)
			}
		}

	case strings.EqualFold(existingOperation.ExternalID.ResourceType.String(), api.ExternalAuthResourceType.String()):
		dbClient := cosmosClient.HCPClusters(existingOperation.ExternalID.SubscriptionID, existingOperation.ExternalID.ResourceGroupName).ExternalAuth(existingOperation.ExternalID.Parent.Name)
		updated, err := getExternalAuthForUpdate(ctx, logger, dbClient, existingOperation, newOperationStatus)
		if err != nil {
			return err
		}
		if updated != nil {
			logger.Info("Updating resource", "activeOperationID", updated.ServiceProviderProperties.ActiveOperationID, "newStatus", newOperationStatus)
			if _, err := dbClient.AddReplaceToTransaction(ctx, transaction, updated, nil); err != nil {
				return utils.TrackError(err)
			}
		}

	default:
		return utils.TrackError(fmt.Errorf("unknown resource type: %s", existingOperation.ExternalID.ResourceType.String()))
	}

	// Execute the transaction atomically.

	logger.Info("Updating operation status", "oldStatus", existingOperation.Status, "newStatus", newOperationStatus, "operationError", newOperationError)
	if _, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{}); err != nil {
		return utils.TrackError(err)
	}

	notifyOperationOwner(ctx, cosmosClient, updatedOperation, postAsyncNotificationFn)

	return nil
}

// getClusterForUpdate returns a deep copy of the cluster with updated provisioning
// state, or nil if the resource update should be skipped.
func getClusterForUpdate(ctx context.Context, logger logr.Logger, dbClient database.HCPClusterCRUD, existingOperation *api.Operation, newOperationStatus arm.ProvisioningState) (*api.HCPOpenShiftCluster, error) {
	curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		logger.Info("Resource not found, skipping resource update")
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if existingOperation.OperationID == nil {
		return nil, utils.TrackError(fmt.Errorf("missing operation ID"))
	}
	if curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
		logger.Info("Resource has a different active operation, skipping resource update",
			"resourceActiveOperationID", curr.ServiceProviderProperties.ActiveOperationID,
			"thisOperationID", existingOperation.OperationID.Name)
		return nil, nil
	}
	if curr.ServiceProviderProperties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
		logger.Info("No update needed", "activeOperationID", curr.ServiceProviderProperties.ActiveOperationID, "oldStatus", curr.ServiceProviderProperties.ProvisioningState, "newStatus", newOperationStatus)
		return nil, nil
	}

	updated := curr.DeepCopy()
	updated.ServiceProviderProperties.ProvisioningState = newOperationStatus
	api.SetProvisioningCondition(&updated.ServiceProviderProperties.ProvisioningConditions, newOperationStatus, existingOperation.CorrelationRequestID)
	if newOperationStatus.IsTerminal() {
		updated.ServiceProviderProperties.ActiveOperationID = ""
	}
	return updated, nil
}

// getNodePoolForUpdate returns a deep copy of the node pool with updated provisioning
// state, or nil if the resource update should be skipped.
func getNodePoolForUpdate(ctx context.Context, logger logr.Logger, dbClient database.NodePoolsCRUD, existingOperation *api.Operation, newOperationStatus arm.ProvisioningState) (*api.HCPOpenShiftClusterNodePool, error) {
	curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		logger.Info("Resource not found, skipping resource update")
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if existingOperation.OperationID == nil {
		return nil, utils.TrackError(fmt.Errorf("missing operation ID"))
	}
	if curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
		logger.Info("Resource has a different active operation, skipping resource update",
			"resourceActiveOperationID", curr.ServiceProviderProperties.ActiveOperationID,
			"thisOperationID", existingOperation.OperationID.Name)
		return nil, nil
	}
	if curr.Properties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
		logger.Info("No update needed", "activeOperationID", curr.ServiceProviderProperties.ActiveOperationID, "oldStatus", curr.Properties.ProvisioningState, "newStatus", newOperationStatus)
		return nil, nil
	}

	updated := curr.DeepCopy()
	updated.Properties.ProvisioningState = newOperationStatus
	api.SetProvisioningCondition(&updated.ServiceProviderProperties.ProvisioningConditions, newOperationStatus, existingOperation.CorrelationRequestID)
	if newOperationStatus.IsTerminal() {
		updated.ServiceProviderProperties.ActiveOperationID = ""
	}
	return updated, nil
}

// getExternalAuthForUpdate returns a deep copy of the external auth with updated
// provisioning state, or nil if the resource update should be skipped.
func getExternalAuthForUpdate(ctx context.Context, logger logr.Logger, dbClient database.ExternalAuthsCRUD, existingOperation *api.Operation, newOperationStatus arm.ProvisioningState) (*api.HCPOpenShiftClusterExternalAuth, error) {
	curr, err := dbClient.Get(ctx, existingOperation.ExternalID.Name)
	var responseErr *azcore.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode == http.StatusNotFound {
		logger.Info("Resource not found, skipping resource update")
		return nil, nil
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}
	if existingOperation.OperationID == nil {
		return nil, utils.TrackError(fmt.Errorf("missing operation ID"))
	}
	if curr.ServiceProviderProperties.ActiveOperationID != existingOperation.OperationID.Name {
		logger.Info("Resource has a different active operation, skipping resource update",
			"resourceActiveOperationID", curr.ServiceProviderProperties.ActiveOperationID,
			"thisOperationID", existingOperation.OperationID.Name)
		return nil, nil
	}
	if curr.Properties.ProvisioningState == newOperationStatus && !newOperationStatus.IsTerminal() {
		logger.Info("No update needed", "activeOperationID", curr.ServiceProviderProperties.ActiveOperationID, "oldStatus", curr.Properties.ProvisioningState, "newStatus", newOperationStatus)
		return nil, nil
	}

	updated := curr.DeepCopy()
	updated.Properties.ProvisioningState = newOperationStatus
	api.SetProvisioningCondition(&updated.ServiceProviderProperties.ProvisioningConditions, newOperationStatus, existingOperation.CorrelationRequestID)
	if newOperationStatus.IsTerminal() {
		updated.ServiceProviderProperties.ActiveOperationID = ""
	}
	return updated, nil
}

func needToPatchOperation(oldOperation *api.Operation, newOperationStatus arm.ProvisioningState, newOperationError *arm.CloudErrorBody) bool {
	statusChanged := oldOperation.Status != newOperationStatus
	errorChanged := oldOperation.Error != newOperationError
	needsNotification := len(oldOperation.NotificationURI) > 0 && newOperationStatus.IsTerminal()
	if statusChanged || errorChanged || needsNotification {
		return true
	}

	return false
}

// patchOperation patches the status and error fields of an OperationDocument.
func patchOperation(ctx context.Context, dbClient database.DBClient, oldOperation *api.Operation, newOperationStatus arm.ProvisioningState, newOperationError *arm.CloudErrorBody, postAsyncNotificationFn PostAsyncNotificationFunc) error {
	logger := utils.LoggerFromContext(ctx)

	if !needToPatchOperation(oldOperation, newOperationStatus, newOperationError) {
		// we rewrite the status when we missed a notification
		// if we have nothing to write, we simply return without error
		return nil
	}

	operationToWrite := oldOperation.DeepCopy()
	operationToWrite.LastTransitionTime = localClock.Now()
	operationToWrite.Status = newOperationStatus
	if newOperationError != nil {
		operationToWrite.Error = newOperationError
	}

	// TODO see if we want to plumb etags through to prevent stomping.  Right now this will stomp a concurrent write.
	// we don't expect concurrent writes and the last one winning is ok.
	logger.Info("Updating operation status", "oldStatus", oldOperation.Status, "newStatus", newOperationStatus, "operationError", newOperationError)
	latestOperation, err := dbClient.Operations(operationToWrite.OperationID.SubscriptionID).Replace(ctx, operationToWrite, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	notifyOperationOwner(ctx, dbClient, latestOperation, postAsyncNotificationFn)

	return nil
}

// notifyOperationOwner logs the operation result and, if applicable, sends an async
// notification to ARM and clears the notification URI.
//
// The notification URI is cleared in a separate write after the notification is
// sent successfully. If the process crashes between sending the notification and
// clearing the URI, the notification may be sent again on the next reconcile.
func notifyOperationOwner(ctx context.Context, cosmosClient database.DBClient, operation *api.Operation, postAsyncNotificationFn PostAsyncNotificationFunc) {
	logger := utils.LoggerFromContext(ctx)

	message := fmt.Sprintf("Updated status to '%s'", operation.Status)
	switch operation.Status {
	case arm.ProvisioningStateSucceeded:
		switch operation.Request {
		case database.OperationRequestCreate:
			message = "Resource creation succeeded"
		case database.OperationRequestUpdate:
			message = "Resource update succeeded"
		case database.OperationRequestDelete:
			message = "Resource deletion succeeded"
		case database.OperationRequestRequestCredential:
			message = "Credential request succeeded"
		case database.OperationRequestRevokeCredentials:
			message = "Credential revocation succeeded"
		}
	case arm.ProvisioningStateFailed:
		switch operation.Request {
		case database.OperationRequestCreate:
			message = "Resource creation failed"
		case database.OperationRequestUpdate:
			message = "Resource update failed"
		case database.OperationRequestDelete:
			message = "Resource deletion failed"
		case database.OperationRequestRequestCredential:
			message = "Credential request failed"
		case database.OperationRequestRevokeCredentials:
			message = "Credential revocation failed"
		}
	}
	if operation.Error != nil {
		logger.WithValues(
			utils.LogValues{}.
				AddCloudErrorCode(operation.Error.Code).
				AddCloudErrorMessage(operation.Error.Message)...).
			Error(nil, message)
	} else {
		logger.Info(message)
	}

	if postAsyncNotificationFn != nil && operation.Status.IsTerminal() && len(operation.NotificationURI) > 0 {
		err := postAsyncNotificationFn(ctx, operation)
		if err == nil {
			logger.Info("Posted async notification")

			// Remove the notification URI from the document
			// so the ARM notification is only sent once.
			// Re-read the operation to get the current ETag,
			// since the in-memory copy may have a stale ETag
			// from before a transactional batch commit.
			operationsCRUD := cosmosClient.Operations(operation.OperationID.SubscriptionID)
			currentOperation, err := operationsCRUD.Get(ctx, operation.OperationID.Name)
			if err != nil {
				logger.Error(err, "Failed to re-read operation to clear notification URI")
			} else {
				currentOperation.NotificationURI = ""
				_, err = operationsCRUD.Replace(ctx, currentOperation, nil)
				if err != nil {
					logger.Error(err, "Failed to clear notification URI")
				}
			}
		} else {
			logger.Error(err, "Failed to post async notification")
		}
	}
}

// PostAsyncNotification submits an POST request with status payload to the given URL.
func postAsyncNotificationFn(notificationClient *http.Client) PostAsyncNotificationFunc {
	return func(ctx context.Context, operation *api.Operation) error {
		return PostAsyncNotification(ctx, notificationClient, operation)
	}
}

func PostAsyncNotification(ctx context.Context, notificationClient *http.Client, operation *api.Operation) error {
	data, err := arm.MarshalJSON(database.ToStatus(operation))
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, operation.NotificationURI, bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := notificationClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return errors.New(response.Status)
	}

	return nil
}

// convertClusterStatus attempts to translate a ClusterStatus object from
// Cluster Service into an ARM provisioning state and, if necessary, a
// structured OData error.
func convertClusterStatus(ctx context.Context, clusterServiceClient ocm.ClusterServiceClientSpec, operation *api.Operation, clusterStatus *arohcpv1alpha1.ClusterStatus) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var newOperationStatus = operation.Status
	var opError *arm.CloudErrorBody
	var err error

	switch state := clusterStatus.State(); state {
	case arohcpv1alpha1.ClusterStateError:
		newOperationStatus = arm.ProvisioningStateFailed
		// Provision error codes are defined in the CS repo:
		// https://gitlab.cee.redhat.com/service/uhc-clusters-service/-/blob/master/pkg/api/cluster_errors.go
		code := clusterStatus.ProvisionErrorCode()
		if code == "" {
			code = arm.CloudErrorCodeInternalServerError
		}
		message := clusterStatus.ProvisionErrorMessage()
		if message == "" {
			message = clusterStatus.Description()
		}
		// Construct the cloud error code depending on the provision error code.
		switch code {
		case InflightChecksFailedProvisionErrorCode:
			opError, err = convertInflightChecks(ctx, clusterServiceClient, operation.InternalID)
			if err != nil {
				return newOperationStatus, opError, err
			}
		default:
			opError = &arm.CloudErrorBody{Code: code, Message: message}
		}
	case arohcpv1alpha1.ClusterStateInstalling:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case arohcpv1alpha1.ClusterStateUpdating:
		newOperationStatus = arm.ProvisioningStateUpdating
	case arohcpv1alpha1.ClusterStateReady:
		// Resource deletion is successful when fetching its state
		// from Cluster Service returns a "404 Not Found" error. If
		// we see the resource in a "Ready" state during a deletion
		// operation, leave the current provisioning state as is.
		if operation.Request != database.OperationRequestDelete {
			newOperationStatus = arm.ProvisioningStateSucceeded
		}
	case arohcpv1alpha1.ClusterStateUninstalling:
		newOperationStatus = arm.ProvisioningStateDeleting
	case arohcpv1alpha1.ClusterStatePending, arohcpv1alpha1.ClusterStateValidating:
		// These are valid cluster states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if newOperationStatus != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("got ClusterState '%s' while ProvisioningState was '%s' instead of '%s'", state, newOperationStatus, arm.ProvisioningStateAccepted)
		}
	default:
		err = fmt.Errorf("unhandled ClusterState '%s'", state)
	}

	return newOperationStatus, opError, err
}

// pollNodePoolStatus converts a node pool status from Cluster
// Service to info for an Azure async operation status endpoint.
func pollNodePoolStatus(
	ctx context.Context,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	operation *api.Operation,
	notificationClient *http.Client) error {
	// XXX This is currently called by the operationNodePoolCreate and
	//     operationNodePoolUpdate controllers because the logic flows
	//     are identical. If the logic flows ever diverge, then this
	//     function should be split up and the pieces moved back to
	//     their respective controllers.

	logger := utils.LoggerFromContext(ctx)

	nodePoolStatus, err := clusterServiceClient.GetNodePoolStatus(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus, newOperationError, err := convertNodePoolStatus(operation, nodePoolStatus)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info("new status", "newStatus", newOperationStatus)

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, cosmosClient, operation, newOperationStatus, newOperationError, postAsyncNotificationFn(notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// convertNodePoolStatus attempts to translate a NodePoolStatus object
// from Cluster Service into an ARM provisioning state and, if necessary,
// a structured OData error.
func convertNodePoolStatus(operation *api.Operation, nodePoolStatus *arohcpv1alpha1.NodePoolStatus) (arm.ProvisioningState, *arm.CloudErrorBody, error) {
	var newOperationStatus = operation.Status
	var opError *arm.CloudErrorBody
	var err error

	switch state := NodePoolStateValue(nodePoolStatus.State().NodePoolStateValue()); state {
	case NodePoolStateValidating, NodePoolStatePending, NodePoolStateValidatingUpdate, NodePoolStatePendingUpdate:
		// These are valid node pool states for ARO-HCP but there are
		// no unique ProvisioningState values for them. They should
		// only occur when ProvisioningState is Accepted.
		if operation.Status != arm.ProvisioningStateAccepted {
			err = fmt.Errorf("got NodePoolStatusValue '%s' while ProvisioningState was '%s' instead of '%s'", state, operation.Status, arm.ProvisioningStateAccepted)
		}
	case NodePoolStateInstalling:
		newOperationStatus = arm.ProvisioningStateProvisioning
	case NodePoolStateReady:
		// Resource deletion is successful when fetching its state
		// from Cluster Service returns a "404 Not Found" error. If
		// we see the resource in a "Ready" state during a deletion
		// operation, leave the current provisioning state as is.
		if operation.Request != database.OperationRequestDelete {
			newOperationStatus = arm.ProvisioningStateSucceeded
		}
	case NodePoolStateUpdating:
		newOperationStatus = arm.ProvisioningStateUpdating
	case NodePoolStateUninstalling:
		newOperationStatus = arm.ProvisioningStateDeleting
	case NodePoolStateRecoverableError, NodePoolStateError:
		// XXX OCM SDK offers no error code or message for failed node pool
		//     operations so "Internal Server Error" is all we can do for now.
		//     https://issues.redhat.com/browse/ARO-14969
		newOperationStatus = arm.ProvisioningStateFailed
		opError = arm.NewInternalServerError().CloudErrorBody
		if msg, ok := nodePoolStatus.GetMessage(); ok {
			opError.Message = msg
		}
	default:
		err = fmt.Errorf("unhandled NodePoolState '%s'", state)
	}

	return newOperationStatus, opError, err
}

// pollExternalAuthStatus converts an external auth status from Cluster
// Service to info for an Azure async operation status endpoint.
func pollExternalAuthStatus(
	ctx context.Context,
	cosmosClient database.DBClient,
	clusterServiceClient ocm.ClusterServiceClientSpec,
	operation *api.Operation,
	notificationClient *http.Client) error {
	// XXX This is currently called by the operationExternalAuthCreate and
	//     operationExternalAuthUpdate controllers because the logic flows
	//     are identical. If the logic flows ever diverge, then this
	//     function should be split up and the pieces moved back to
	//     their respective controllers.

	logger := utils.LoggerFromContext(ctx)

	_, err := clusterServiceClient.GetExternalAuth(ctx, operation.InternalID)
	if err != nil {
		return utils.TrackError(err)
	}

	newOperationStatus := arm.ProvisioningStateSucceeded
	logger.Info("new status", "newStatus", newOperationStatus)

	logger.Info("updating status")
	err = UpdateOperationStatus(ctx, cosmosClient, operation, newOperationStatus, nil, postAsyncNotificationFn(notificationClient))
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// convertInflightChecks gets a cluster internal ID, fetches inflight check errors from CS endpoint, and converts them
// to arm.CloudErrorBody type.
// The function should be triggered only if inflight errors occurred with provision error code OCM4001.
func convertInflightChecks(ctx context.Context, clusterServiceClient ocm.ClusterServiceClientSpec, internalId ocm.InternalID) (*arm.CloudErrorBody, error) {
	logger := utils.LoggerFromContext(ctx)

	inflightChecks, err := clusterServiceClient.GetClusterInflightChecks(ctx, internalId)
	if err != nil {
		return &arm.CloudErrorBody{}, err
	}

	var cloudErrors []arm.CloudErrorBody
	for _, inflightCheck := range inflightChecks.Items() {
		if inflightCheck.State() == arohcpv1alpha1.InflightCheckStateFailed {
			cloudErrors = append(cloudErrors, convertInflightCheck(inflightCheck, logger))
		}
	}

	// This is a fallback case and should not normally occur. If the provision error code is OCM4001,
	// there should be at least one inflight failure.
	if len(cloudErrors) == 0 {
		logger.Info("Cluster returned error code OCM4001, but no inflight failures were found", "internalId", internalId)
		return &arm.CloudErrorBody{
			Code: arm.CloudErrorCodeInternalServerError,
		}, nil
	}

	return arm.NewCloudErrorBodyFromSlice(cloudErrors, "Cluster provisioning failed due to multiple errors"), nil
}

func convertInflightCheck(inflightCheck *arohcpv1alpha1.InflightCheck, logger logr.Logger) arm.CloudErrorBody {
	message, succeeded := convertInflightCheckDetails(inflightCheck)
	if !succeeded {
		logger.Error(nil, "error converting inflight check details", "name", inflightCheck.Name())
	}

	return arm.CloudErrorBody{
		Code:    arm.CloudErrorCodeInternalServerError,
		Message: message,
	}
}

// convertInflightCheckDetails gets an inflight check object and extracts the error message.
func convertInflightCheckDetails(inflightCheck *arohcpv1alpha1.InflightCheck) (string, bool) {
	details, ok := inflightCheck.GetDetails()
	if !ok {
		return "", false
	}

	detailsMap, ok := details.(map[string]interface{})
	if !ok {
		return "", false
	}

	// Retrieve "error" key safely
	if errMsg, exists := detailsMap["error"]; exists {
		if errStr, ok := errMsg.(string); ok {
			return errStr, true
		}
	}

	return "", false
}

// setDeleteOperationAsCompleted updates Cosmos DB to reflect a completed resource deletion.
func SetDeleteOperationAsCompleted(ctx context.Context, cosmosClient database.DBClient, operation *api.Operation, postAsyncNotificationFn PostAsyncNotificationFunc) error {
	// Delete the resource document first. If it fails the backend will retry
	// by virtue of the operation document still having a non-terminal status.
	untypedCRUD, err := cosmosClient.UntypedCRUD(*operation.ExternalID)
	if err != nil {
		return utils.TrackError(err)
	}
	if err := untypedCRUD.Delete(ctx, operation.ExternalID); err != nil {
		return utils.TrackError(err)
	}

	// TODO once we rekey based on resourceID, consider doing this all in a transaction.
	// If any fail, we re-enter because the operation still exists
	// If a controller starts working the first time and the cluster is deleted in that timeframe, then the controller
	// may create an instance of its controller status.  We can create a controller to periodically scrape orphans
	// and either delete them or call them out.
	childIterator, err := untypedCRUD.List(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}
	for _, childResource := range childIterator.Items(ctx) {
		// clusters, nodepools, and externalauths have special deletion handling, so don't delete them from here.
		switch strings.ToLower(childResource.ResourceType) {
		case strings.ToLower(api.ClusterControllerResourceType.String()),
			strings.ToLower(api.NodePoolControllerResourceType.String()),
			strings.ToLower(api.ExternalAuthControllerResourceType.String()):
			continue
		}

		resourceInfo := database.ResourceDocument{}
		if err := json.Unmarshal(childResource.Properties, &resourceInfo); err != nil {
			return utils.TrackError(err)
		}
		if err := untypedCRUD.Delete(ctx, resourceInfo.ResourceID); err != nil {
			return utils.TrackError(err)
		}
	}
	if err := childIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	// Save a final "succeeded" operation status until TTL expires.
	err = patchOperation(ctx, cosmosClient, operation, arm.ProvisioningStateSucceeded, nil, postAsyncNotificationFn)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}
