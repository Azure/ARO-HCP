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
	"maps"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func (f *Frontend) CreateOrUpdateNodePool(writer http.ResponseWriter, request *http.Request) {
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

	body, err := BodyFromContext(ctx)
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

	var versionedCurrentNodePool api.VersionedHCPOpenShiftClusterNodePool
	var versionedRequestNodePool api.VersionedHCPOpenShiftClusterNodePool
	var successStatusCode int

	if updating {
		csNodePool, err := f.clusterServiceClient.GetNodePool(ctx, resourceDoc.InternalID)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to fetch CS node pool for %s: %v", resourceID, err))
			arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
			return
		}

		hcpNodePool, err := ocm.ConvertCStoNodePool(resourceID, csNodePool)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		// Do not set the TrackedResource.Tags field here. We need
		// the Tags map to remain nil so we can see if the request
		// body included a new set of resource tags.

		hcpNodePool.SystemData = resourceDoc.SystemData
		hcpNodePool.Properties.ProvisioningState = resourceDoc.ProvisioningState

		operationRequest = database.OperationRequestUpdate

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			// Initialize versionedRequestNodePool to include both
			// non-zero default values and current read-only values.
			reqNodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID)

			// Some optional create-only fields have dynamic default
			// values that are determined downstream of this phase of
			// request processing. To ensure idempotency, add these
			// values to the target struct for the incoming request.
			reqNodePool.Properties.Version.ID = hcpNodePool.Properties.Version.ID
			reqNodePool.Properties.Platform.SubnetID = hcpNodePool.Properties.Platform.SubnetID

			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(reqNodePool)
			api.CopyReadOnlyValues(versionedCurrentNodePool, versionedRequestNodePool)
			successStatusCode = http.StatusOK
		case http.MethodPatch:
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			successStatusCode = http.StatusAccepted
		}
	} else {
		operationRequest = database.OperationRequestCreate

		switch request.Method {
		case http.MethodPut:
			// Initialize top-level resource fields from the request path.
			// If the request body specifies these fields, validation should
			// accept them as long as they match (case-insensitively) values
			// from the request path.
			hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID)

			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
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

	// Node pool validation checks some fields against the parent cluster
	// so we have to request the cluster from Cluster Service.

	_, clusterResourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID.Parent)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID.Parent)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	csCluster, err := f.clusterServiceClient.GetCluster(ctx, clusterResourceDoc.InternalID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID.Parent, writer.Header()))
		return
	}

	hcpCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID.Parent, csCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	cloudError = api.ApplyRequestBody(request, body, versionedRequestNodePool)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	cloudError = api.ValidateVersionedHCPOpenShiftClusterNodePool(versionedRequestNodePool, versionedCurrentNodePool, hcpCluster, updating)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID)
	versionedRequestNodePool.Normalize(hcpNodePool)

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, hcpNodePool, updating)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var csNodePool *arohcpv1alpha1.NodePool

	if updating {
		logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		csNodePool, err = f.clusterServiceClient.UpdateNodePool(ctx, resourceDoc.InternalID, csNodePoolBuilder)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
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

		csNodePool, err = f.clusterServiceClient.PostNodePool(ctx, clusterDoc.InternalID, csNodePoolBuilder)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
			return
		}

		resourceDoc.InternalID, err = ocm.NewInternalID(csNodePool.HREF())
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
		resourceItemID = transaction.CreateResourceDoc(resourceDoc, database.FilterNodePoolState, nil)
	}

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		patchOperations.SetSystemData(systemData)
	}

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if hcpNodePool.Tags != nil {
		patchOperations.SetTags(hcpNodePool.Tags)
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

	responseBody, err := marshalCSNodePool(csNodePool, resourceDoc, versionedInterface)
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

// the necessary conversions for the API version of the request.
func marshalCSNodePool(csNodePool *arohcpv1alpha1.NodePool, doc *database.ResourceDocument, versionedInterface api.Version) ([]byte, error) {
	hcpNodePool, err := ocm.ConvertCStoNodePool(doc.ResourceID, csNodePool)
	if err != nil {
		return nil, err
	}

	hcpNodePool.SystemData = doc.SystemData
	hcpNodePool.Tags = maps.Clone(doc.Tags)
	hcpNodePool.Properties.ProvisioningState = doc.ProvisioningState

	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool))
}
