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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"

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

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
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
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}

		hcpNodePool := ConvertCStoNodePool(resourceID, csNodePool)

		// Do not set the TrackedResource.Tags field here. We need
		// the Tags map to remain nil so we can see if the request
		// body included a new set of resource tags.

		operationRequest = database.OperationRequestUpdate

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
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
			versionedCurrentNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
			versionedRequestNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(nil)
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

	clusterResourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID.Parent)
	if err != nil {
		logger.Error(err.Error())
		if errors.Is(err, database.ErrNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID.Parent)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	csCluster, err := f.clusterServiceClient.GetCluster(ctx, clusterResourceDoc.InternalID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID.Parent))
		return
	}

	hcpCluster := ConvertCStoHCPOpenShiftCluster(resourceID.Parent, csCluster)

	body, err := BodyFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	if err = json.Unmarshal(body, versionedRequestNodePool); err != nil {
		logger.Error(err.Error())
		arm.WriteInvalidRequestContentError(writer, err)
		return
	}

	cloudError = versionedRequestNodePool.ValidateStatic(versionedCurrentNodePool, hcpCluster, updating, request)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool()
	versionedRequestNodePool.Normalize(hcpNodePool)

	hcpNodePool.Name = request.PathValue(PathSegmentNodePoolName)
	csNodePool, err := f.BuildCSNodePool(ctx, hcpNodePool, updating)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if updating {
		logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		csNodePool, err = f.clusterServiceClient.UpdateNodePool(ctx, resourceDoc.InternalID, csNodePool)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}
	} else {
		logger.Info(fmt.Sprintf("creating resource %s", resourceID))
		clusterDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID.Parent)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		csNodePool, err = f.clusterServiceClient.PostNodePool(ctx, clusterDoc.InternalID, csNodePool)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, CSErrorToCloudError(err, resourceID))
			return
		}

		resourceDoc.InternalID, err = ocm.NewInternalID(csNodePool.HREF())
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	operationDoc := database.NewOperationDocument(operationRequest, resourceDoc.ResourceID, resourceDoc.InternalID)

	operationID, err := f.dbClient.CreateOperationDoc(ctx, operationDoc)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)
	err = f.ExposeOperation(writer, request, pk, operationID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// This is called directly when creating a resource, and indirectly from
	// within a retry loop when updating a resource.
	updateResourceMetadata := func(updateDoc *database.ResourceDocument) bool {
		updateDoc.ActiveOperationID = operationID
		updateDoc.ProvisioningState = operationDoc.Status

		// Record the latest system data values from ARM, if present.
		if systemData != nil {
			updateDoc.SystemData = systemData
		}

		// Here the difference between a nil map and an empty map is significant.
		// If the Tags map is nil, that means it was omitted from the request body,
		// so we leave any existing tags alone. If the Tags map is non-nil, even if
		// empty, that means it was specified in the request body and should fully
		// replace any existing tags.
		if hcpNodePool.Tags != nil {
			updateDoc.Tags = hcpNodePool.Tags
		}

		return true
	}

	if !updating {
		updateResourceMetadata(resourceDoc)
		err = f.dbClient.CreateResourceDoc(ctx, resourceDoc)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		logger.Info(fmt.Sprintf("document created for %s", resourceID))
	} else {
		updated, err := f.dbClient.UpdateResourceDoc(ctx, resourceID, updateResourceMetadata)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		if updated {
			logger.Info(fmt.Sprintf("document updated for %s", resourceID))
		}
		// Get the updated resource document for the response.
		resourceDoc, err = f.dbClient.GetResourceDoc(ctx, resourceID)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
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
	hcpNodePool := ConvertCStoNodePool(doc.ResourceID, csNodePool)
	hcpNodePool.SystemData = doc.SystemData
	hcpNodePool.Tags = maps.Clone(doc.Tags)
	hcpNodePool.Properties.ProvisioningState = doc.ProvisioningState

	return versionedInterface.MarshalHCPOpenShiftClusterNodePool(hcpNodePool)
}
