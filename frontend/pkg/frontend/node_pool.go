package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (doc != nil)
	var operationRequest database.OperationRequest

	var versionedCurrentNodePool api.VersionedHCPOpenShiftClusterNodePool
	var versionedRequestNodePool api.VersionedHCPOpenShiftClusterNodePool
	var successStatusCode int

	if updating {
		// Note that because we found a database document for the cluster,
		// we expect Cluster Service to return us a node pool object.
		//
		// No special treatment here for "not found" errors. A "not found"
		// error indicates the database has gotten out of sync and so it's
		// appropriate to fail.
		csNodePool, err := f.clusterServiceClient.GetNodePool(ctx, doc.InternalID)
		if err != nil {
			logger.Error(fmt.Sprintf("failed to fetch CS node pool for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
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

		doc = database.NewResourceDocument(resourceID)
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, doc)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

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

	cloudError = versionedRequestNodePool.ValidateStatic(versionedCurrentNodePool, updating, request.Method)
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
		csNodePool, err = f.clusterServiceClient.UpdateNodePool(ctx, doc.InternalID, csNodePool)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
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
			arm.WriteInternalServerError(writer)
			return
		}

		doc.InternalID, err = ocm.NewInternalID(csNodePool.HREF())
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	operationDoc := database.NewOperationDocument(operationRequest, doc.ResourceID, doc.InternalID)

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
	updateResourceMetadata := func(doc *database.ResourceDocument) bool {
		doc.ActiveOperationID = operationID
		doc.ProvisioningState = operationDoc.Status

		// Record the latest system data values from ARM, if present.
		if systemData != nil {
			doc.SystemData = systemData
		}

		// Here the difference between a nil map and an empty map is significant.
		// If the Tags map is nil, that means it was omitted from the request body,
		// so we leave any existing tags alone. If the Tags map is non-nil, even if
		// empty, that means it was specified in the request body and should fully
		// replace any existing tags.
		if hcpNodePool.TrackedResource.Tags != nil {
			doc.Tags = hcpNodePool.TrackedResource.Tags
		}

		return true
	}

	if !updating {
		updateResourceMetadata(doc)
		err = f.dbClient.CreateResourceDoc(ctx, doc)
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
		doc, err = f.dbClient.GetResourceDoc(ctx, resourceID)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	responseBody, err := marshalCSNodePool(csNodePool, doc, versionedInterface)
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
	hcpNodePool.TrackedResource.Resource.SystemData = doc.SystemData
	hcpNodePool.TrackedResource.Tags = maps.Clone(doc.Tags)
	hcpNodePool.Properties.ProvisioningState = doc.ProvisioningState

	return versionedInterface.MarshalHCPOpenShiftClusterNodePool(hcpNodePool)
}
