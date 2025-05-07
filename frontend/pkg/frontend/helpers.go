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
	"go.opentelemetry.io/otel/trace"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/tracing"
)

// CheckForProvisioningStateConflict returns a "409 Conflict" error response if the
// provisioning state of the resource is non-terminal, or any of its parent resources
// within the same provider namespace are in a "Deleting" state.
func (f *Frontend) CheckForProvisioningStateConflict(ctx context.Context, operationRequest database.OperationRequest, doc *database.ResourceDocument) *arm.CloudError {
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

func (f *Frontend) DeleteAllResources(ctx context.Context, subscriptionID string) *arm.CloudError {
	logger := LoggerFromContext(ctx)

	prefix, err := azcorearm.ParseResourceID("/subscriptions/" + subscriptionID)
	if err != nil {
		logger.Error(err.Error())
		return arm.NewInternalServerError()
	}

	transaction := f.dbClient.NewTransaction(database.NewPartitionKey(subscriptionID))

	dbIterator := f.dbClient.ListResourceDocs(prefix, -1, nil)

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

	switch resourceDoc.InternalID.Kind() {
	case arohcpv1alpha1.ClusterKind:
		err = f.clusterServiceClient.DeleteCluster(ctx, resourceDoc.InternalID)

	case arohcpv1alpha1.NodePoolKind:
		err = f.clusterServiceClient.DeleteNodePool(ctx, resourceDoc.InternalID)

	default:
		logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", resourceDoc.InternalID))
		return "", arm.NewInternalServerError()
	}

	if err != nil {
		cloudError := CSErrorToCloudError(err, resourceDoc.ResourceID)
		// Do not log attempts to delete a nonexistent
		// resource because the end result is the same.
		if cloudError.StatusCode != http.StatusNotFound {
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

	operationDoc := database.NewOperationDocument(operationRequest, resourceDoc.ResourceID, resourceDoc.InternalID)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	var patchOperations database.ResourceDocumentPatchOperations
	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)
	transaction.PatchResourceDoc(resourceItemID, patchOperations, nil)

	iterator := f.dbClient.ListResourceDocs(resourceDoc.ResourceID, -1, nil)

	for childItemID, childResourceDoc := range iterator.Items(ctx) {
		// This operation is not accessible through any REST endpoint.
		// Its purpose is to cause the backend to delete the resource
		// document once resource deletion completes.

		childOperationDoc := database.NewOperationDocument(operationRequest, childResourceDoc.ResourceID, childResourceDoc.InternalID)
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

func (f *Frontend) MarshalResource(ctx context.Context, resourceID *azcorearm.ResourceID, versionedInterface api.Version) ([]byte, *arm.CloudError) {
	var responseBody []byte

	logger := LoggerFromContext(ctx)

	_, doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			return nil, arm.NewResourceNotFoundError(resourceID)
		} else {
			return nil, arm.NewInternalServerError()
		}
	}

	switch doc.InternalID.Kind() {
	case arohcpv1alpha1.ClusterKind:
		csCluster, err := f.clusterServiceClient.GetCluster(ctx, doc.InternalID)
		if err != nil {
			logger.Error(err.Error())
			return nil, CSErrorToCloudError(err, resourceID)
		}
		tracing.SetClusterAttributes(trace.SpanFromContext(ctx), csCluster)

		responseBody, err = marshalCSCluster(csCluster, doc, versionedInterface)
		if err != nil {
			logger.Error(err.Error())
			return nil, arm.NewInternalServerError()
		}

	case arohcpv1alpha1.NodePoolKind:
		csNodePool, err := f.clusterServiceClient.GetNodePool(ctx, doc.InternalID)
		if err != nil {
			logger.Error(err.Error())
			return nil, CSErrorToCloudError(err, resourceID)
		}
		responseBody, err = marshalCSNodePool(csNodePool, doc, versionedInterface)
		if err != nil {
			logger.Error(err.Error())
			return nil, arm.NewInternalServerError()
		}

	default:
		logger.Error(fmt.Sprintf("unsupported Cluster Service path: %s", doc.InternalID))
		return nil, arm.NewInternalServerError()
	}

	return responseBody, nil
}
