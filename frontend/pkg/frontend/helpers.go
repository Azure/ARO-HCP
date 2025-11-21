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

package frontend

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func operationNotificationFn(writer http.ResponseWriter, request *http.Request, notificationURI string, operationID *azcorearm.ResourceID) database.DBTransactionCallback {
	return func(result database.DBTransactionResult) {
		// If ARM passed a notification URI, acknowledge it.
		if len(notificationURI) > 0 {
			writer.Header().Set(arm.HeaderNameAsyncNotification, "Enabled")
		}

		// Add callback header(s) based on the request method.
		switch request.Method {
		case http.MethodDelete, http.MethodPatch, http.MethodPost:
			AddLocationHeader(writer, request, operationID)
			fallthrough
		case http.MethodPut:
			AddAsyncOperationHeader(writer, request, operationID)
		}
	}
}

// checkForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Provisioning" or "Deleting" state.
// TODO we will collapse onto this function entirely once we complete the migration.  Creating a separate method now to avoid having to have a big bang
func checkForProvisioningStateConflict(
	ctx context.Context,
	cosmosClient database.DBClient,
	operationRequest database.OperationRequest,
	resourceID *azcorearm.ResourceID,
	provisioningState arm.ProvisioningState,
) *arm.CloudError {

	logger := LoggerFromContext(ctx)

	switch operationRequest {
	case database.OperationRequestCreate:
		// Resource must already exist for there to be a conflict.
	case database.OperationRequestDelete:
		if provisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				resourceID,
				"Resource is already deleting")
		}
	case database.OperationRequestUpdate:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot update resource while resource is %s",
				strings.ToLower(string(provisioningState)))
		}
	case database.OperationRequestRequestCredential:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot request credential while resource is %s",
				strings.ToLower(string(provisioningState)))
		}
	case database.OperationRequestRevokeCredentials:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !provisioningState.IsTerminal() {
			return arm.NewConflictError(
				resourceID,
				"Cannot revoke credentials while resource is %s",
				strings.ToLower(string(provisioningState)))
		}
	}

	parent := resourceID.Parent

	// ResourceType casing is preserved for parents in the same namespace.
	// TODO if I understand this correctly, this is ONLY the Cluster itself, in which case these calls could change.
	for parent.ResourceType.Namespace == resourceID.ResourceType.Namespace {
		_, parentDoc, err := cosmosClient.GetResourceDoc(ctx, parent)
		if err != nil {
			logger.Error(err.Error())
			return arm.NewInternalServerError()
		}

		// XXX There is still a small opportunity for nested resource requests to get
		//     through while the parent resource is in provisioning state "Accepted",
		//     which precedes "Provisioning". The problem is "Accepted" also precedes
		//     "Updating", which should NOT be blocked.
		//
		//     Cluster Service will catch and correctly reject such requests, so I'm
		//     leaving this gap open until Cluster Service is out of the picture and
		//     the RP has more direct control over resource provisioning.
		if parentDoc.ProvisioningState == arm.ProvisioningStateProvisioning {
			return arm.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is provisioning",
				strings.ToLower(string(operationRequest)))
		}

		if parentDoc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				resourceID,
				"Cannot %s resource while parent resource is deleting",
				strings.ToLower(string(operationRequest)))
		}

		parent = parent.Parent
	}

	return nil
}

// CheckForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Provisioning" or "Deleting" state.
func (f *Frontend) CheckForProvisioningStateConflict(ctx context.Context, operationRequest database.OperationRequest, doc *database.ResourceDocument) error {
	logger := LoggerFromContext(ctx)

	switch operationRequest {
	case database.OperationRequestCreate:
		// Resource must already exist for there to be a conflict.
	case database.OperationRequestDelete:
		if doc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				doc.ResourceID,
				"Resource is already deleting")
		}
	case database.OperationRequestUpdate:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !doc.ProvisioningState.IsTerminal() {
			return arm.NewConflictError(
				doc.ResourceID,
				"Cannot update resource while resource is %s",
				strings.ToLower(string(doc.ProvisioningState)))
		}
	case database.OperationRequestRequestCredential:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !doc.ProvisioningState.IsTerminal() {
			return arm.NewConflictError(
				doc.ResourceID,
				"Cannot request credential while resource is %s",
				strings.ToLower(string(doc.ProvisioningState)))
		}
	case database.OperationRequestRevokeCredentials:
		// Defer to Cluster Service for ProvisioningStateFailed since
		// it is ambiguous about whether the resource is functional.
		if !doc.ProvisioningState.IsTerminal() {
			return arm.NewConflictError(
				doc.ResourceID,
				"Cannot revoke credentials while resource is %s",
				strings.ToLower(string(doc.ProvisioningState)))
		}
	}

	parent := doc.ResourceID.Parent

	// ResourceType casing is preserved for parents in the same namespace.
	for parent.ResourceType.Namespace == doc.ResourceID.ResourceType.Namespace {
		_, parentDoc, err := f.dbClient.GetResourceDoc(ctx, parent)
		if err != nil {
			logger.Error(err.Error())
			return arm.NewInternalServerError()
		}

		// XXX There is still a small opportunity for nested resource requests to get
		//     through while the parent resource is in provisioning state "Accepted",
		//     which precedes "Provisioning". The problem is "Accepted" also precedes
		//     "Updating", which should NOT be blocked.
		//
		//     Cluster Service will catch and correctly reject such requests, so I'm
		//     leaving this gap open until Cluster Service is out of the picture and
		//     the RP has more direct control over resource provisioning.
		if parentDoc.ProvisioningState == arm.ProvisioningStateProvisioning {
			return arm.NewConflictError(
				doc.ResourceID,
				"Cannot %s resource while parent resource is provisioning",
				strings.ToLower(string(operationRequest)))
		}

		if parentDoc.ProvisioningState == arm.ProvisioningStateDeleting {
			return arm.NewConflictError(
				doc.ResourceID,
				"Cannot %s resource while parent resource is deleting",
				strings.ToLower(string(operationRequest)))
		}

		parent = parent.Parent
	}

	return nil
}

func (f *Frontend) DeleteAllResources(ctx context.Context, subscriptionID string) error {
	logger := LoggerFromContext(ctx)

	prefix, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)
	if err != nil {
		logger.Error(err.Error())
		return arm.NewInternalServerError()
	}

	transaction := f.dbClient.NewTransaction(database.NewPartitionKey(subscriptionID))

	dbIterator := f.dbClient.ListResourceDocs(prefix, nil)

	// Start a deletion operation for all clusters under the subscription.
	// Cluster Service will delete all node pools belonging to these clusters
	// so we don't need to explicitly delete node pools here.
	for resourceItemID, resourceDoc := range dbIterator.Items(ctx) {
		if !strings.EqualFold(resourceDoc.ResourceID.ResourceType.String(), api.ClusterResourceType.String()) {
			continue
		}

		// Allow this method to be idempotent.
		if resourceDoc.ProvisioningState != arm.ProvisioningStateDeleting {
			_, cloudError := f.DeleteResource(ctx, transaction, resourceItemID, resourceDoc)
			if cloudError != nil {
				return cloudError
			}
		}
	}

	err = dbIterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		return arm.NewInternalServerError()
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		logger.Error(err.Error())
		return arm.NewInternalServerError()
	}

	return nil
}

func (f *Frontend) DeleteResource(ctx context.Context, transaction database.DBTransaction, resourceItemID string, resourceDoc *database.ResourceDocument) (string, *arm.CloudError) {
	const operationRequest = database.OperationRequestDelete
	var err error

	logger := LoggerFromContext(ctx)

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	switch resourceDoc.InternalID.Kind() {
	case arohcpv1alpha1.ClusterKind:
		err = f.clusterServiceClient.DeleteCluster(ctx, resourceDoc.InternalID)

	case arohcpv1alpha1.NodePoolKind:
		err = f.clusterServiceClient.DeleteNodePool(ctx, resourceDoc.InternalID)

	case arohcpv1alpha1.ExternalAuthKind:
		err = f.clusterServiceClient.DeleteExternalAuth(ctx, resourceDoc.InternalID)

	default:
		logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", resourceDoc.InternalID))
		return "", arm.NewInternalServerError()
	}

	if err != nil {
		cloudError := ocm.CSErrorToCloudError(err, resourceDoc.ResourceID, nil)
		if cloudError.StatusCode == http.StatusNotFound {
			// StatusNotFound means we have stale data in Cosmos DB.
			// This can happen in test environments if a user bypasses
			// the RP to delete a resource (e.g. "ocm delete"). It can
			// also happen if an asynchronous deletion operation fails.
			// To provide a way out of this mess we will try to delete
			// the errant Cosmos DB document here.
			logger.Info(fmt.Sprintf("Deleting errant Resources container item for '%s'", resourceDoc.ResourceID))
			recoveryErr := f.dbClient.DeleteResourceDoc(ctx, resourceDoc.ResourceID)
			if recoveryErr != nil {
				logger.Error(recoveryErr.Error())
			}
		} else {
			logger.Error(err.Error())
		}
		return "", cloudError
	}

	// Cluster Service will take care of canceling any ongoing operations
	// on the resource or child resources, but we need to do some database
	// bookkeeping to reflect that.

	err = f.CancelActiveOperations(ctx, transaction, &database.DBClientListActiveOperationDocsOptions{
		ExternalID:             resourceDoc.ResourceID,
		IncludeNestedResources: true,
	})
	if err != nil {
		logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	operationDoc := database.NewOperationDocument(operationRequest, resourceDoc.ResourceID, resourceDoc.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	var patchOperations database.ResourceDocumentPatchOperations
	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)
	transaction.PatchResourceDoc(resourceItemID, patchOperations, nil)

	iterator := f.dbClient.ListResourceDocs(resourceDoc.ResourceID, nil)

	for childItemID, childResourceDoc := range iterator.Items(ctx) {
		// This operation is not accessible through any REST endpoint.
		// Its purpose is to cause the backend to delete the resource
		// document once resource deletion completes.

		childOperationDoc := database.NewOperationDocument(operationRequest, childResourceDoc.ResourceID, childResourceDoc.InternalID, correlationData)
		childOperationID := transaction.CreateOperationDoc(childOperationDoc, nil)

		var patchOperations database.ResourceDocumentPatchOperations
		patchOperations.SetActiveOperationID(&childOperationID)
		patchOperations.SetProvisioningState(childOperationDoc.Status)
		transaction.PatchResourceDoc(childItemID, patchOperations, nil)
	}

	err = iterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		return "", arm.NewInternalServerError()
	}

	return operationID, nil
}

func (f *Frontend) GetExternalClusterFromStorage(ctx context.Context, resourceID *azcorearm.ResourceID, versionedInterface api.Version) (api.VersionedHCPOpenShiftCluster, error) {
	logger := LoggerFromContext(ctx)

	internalCluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		return nil, arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		logger.Error(err.Error())
		return nil, arm.NewInternalServerError()
	}

	csCluster, err := f.clusterServiceClient.GetCluster(ctx, internalCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err.Error())
		return nil, ocm.CSErrorToCloudError(err, resourceID, nil)
	}

	externalCluster, err := mergeToExternalCluster(csCluster, internalCluster, versionedInterface)
	if err != nil {
		logger.Error(err.Error())
		return nil, arm.NewInternalServerError()
	}

	return externalCluster, nil
}
