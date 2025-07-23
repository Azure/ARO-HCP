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
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func (f *Frontend) CreateOrUpdateExternalAuth(writer http.ResponseWriter, request *http.Request) {
	var err error

	// This handles both PUT and PATCH requests. PATCH requests will
	// never create a new resource. The only other notable difference
	// is the target struct that request bodies are overlayed onto:
	//
	// PUT requests overlay the request body onto a default resource
	// struct, which only has API-specified non-zero default values.
	// This means all required properties must be specified in the
	// request body, whether creating or updating a resource.
	//
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	resourceItemID, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (resourceDoc != nil)
	var operationRequest database.OperationRequest

	var currentExternalAuth api.VersionedHCPOpenShiftClusterExternalAuth
	var requestExternalAuth api.VersionedHCPOpenShiftClusterExternalAuth
	var successStatusCode int

	if updating {
		csExternalAuth, err := f.clusterServiceClient.GetExternalAuth(ctx, resourceDoc.InternalID)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to fetch CS external auth for %s: %v", resourceID, err))
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}

		hcpExternalAuth := ConvertCStoExternalAuth(resourceID, csExternalAuth)
		operationRequest = database.OperationRequestUpdate

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			currentExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(hcpExternalAuth)
			requestExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(nil)
			successStatusCode = http.StatusOK
		case http.MethodPatch:
			currentExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(hcpExternalAuth)
			requestExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(hcpExternalAuth)
			successStatusCode = http.StatusAccepted
		}
	} else {
		operationRequest = database.OperationRequestCreate

		switch request.Method {
		case http.MethodPut:
			currentExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(nil)
			requestExternalAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(nil)
			successStatusCode = http.StatusCreated
		case http.MethodPatch:
			// PATCH requests never create a new resource.
			logger.Error("Resource not found")
			arm.WriteResourceNotFoundError(writer, resourceID)
			return
		}

		resourceDoc = database.NewResourceDocument(resourceID)
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, resourceDoc)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	cloudError = requestExternalAuth.ValidateStatic(currentExternalAuth, updating, request)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpExternalAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth()
	requestExternalAuth.Normalize(hcpExternalAuth)

	hcpExternalAuth.Name = request.PathValue(PathSegmentExternalAuthName)
	csExternalAuth, err := f.BuildCSExternalAuth(ctx, hcpExternalAuth, updating)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if updating {
		logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		csExternalAuth, err = f.clusterServiceClient.UpdateExternalAuth(ctx, resourceDoc.InternalID, csExternalAuth)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}
	} else {
		logger.Info(fmt.Sprintf("creating resource %s", resourceID))
		_, clusterDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID.Parent)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		csExternalAuth, err = f.clusterServiceClient.PostExternalAuth(ctx, clusterDoc.InternalID, csExternalAuth)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}

		resourceDoc.InternalID, err = ocm.NewInternalID(csExternalAuth.HREF())
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	transaction := f.dbClient.NewTransaction(pk)

	operationDoc := database.NewOperationDocument(operationRequest, resourceDoc.ResourceID, resourceDoc.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	if !updating {
		resourceItemID = transaction.CreateResourceDoc(resourceDoc, nil)
	}

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		patchOperations.SetSystemData(systemData)
	}

	transaction.PatchResourceDoc(resourceItemID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resourceDoc, err = transactionResult.GetResourceDoc(resourceItemID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	responseBody, err := marshalCSExternalAuth(csExternalAuth, resourceDoc, versionedInterface)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBody)
	if err != nil {
		logger.Error(err.Error())
	}
}

func updateExternalAuth() {
	// var err error
}

// the necessary conversions for the API version of the request.
func marshalCSExternalAuth(csEternalAuth *arohcpv1alpha1.ExternalAuth, doc *database.ResourceDocument, versionedInterface api.Version) ([]byte, error) {
	hcpExternalAuth := ConvertCStoExternalAuth(doc.ResourceID, csEternalAuth)
	hcpExternalAuth.SystemData = doc.SystemData
	// TODO: Use the correct state from the document.
	hcpExternalAuth.Properties.ProvisioningState = arm.ExternalAuthProvisioningStateSucceeded

	return versionedInterface.MarshalHCPOpenShiftClusterExternalAuth(hcpExternalAuth)
}
