package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

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

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolResourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: CreateNodePool", versionedInterface))

	clusterResourceID := nodePoolResourceID.GetParent()
	if clusterResourceID == nil {
		f.logger.Error(fmt.Sprintf("failed to obtain Azure parent resourceID for node pool %s", nodePoolResourceID))
		arm.WriteInternalServerError(writer)
		return
	}

	clusterDoc, err := f.dbClient.GetResourceDoc(ctx, clusterResourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("existing document not found for cluster %s when creating node pool", clusterResourceID))
			arm.WriteInternalServerError(writer)
			return
		}
		f.logger.Error(fmt.Sprintf("failed to fetch cluster document for %s when creating node pool: %v", clusterResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	csCluster, err := f.clusterServiceClient.GetCSCluster(ctx, clusterDoc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to fetch CS cluster for %s: %v", clusterResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	if csCluster.State() == cmv1.ClusterStateUninstalling {
		f.logger.Error(fmt.Sprintf("failed to create node pool for cluster %s as it is in %v state", clusterResourceID, cmv1.ClusterStateUninstalling))
		arm.WriteInternalServerError(writer)
		return
	}

	nodePoolDoc, err := f.dbClient.GetResourceDoc(ctx, nodePoolResourceID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", nodePoolResourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (nodePoolDoc != nil)
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
		csNodePool, err := f.clusterServiceClient.GetCSNodePool(ctx, nodePoolDoc.InternalID)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to fetch CS node pool for %s: %v", nodePoolResourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}

		hcpNodePool := ConvertCStoNodePool(nodePoolResourceID, csNodePool)

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
			f.logger.Error("Resource not found")
			arm.WriteResourceNotFoundError(writer, nodePoolResourceID)
			return
		}

		nodePoolDoc = database.NewResourceDocument(nodePoolResourceID)
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	if err = json.Unmarshal(body, versionedRequestNodePool); err != nil {
		f.logger.Error(err.Error())
		arm.WriteInvalidRequestContentError(writer, err)
		return
	}

	cloudError := versionedRequestNodePool.ValidateStatic(versionedCurrentNodePool, updating, request.Method)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool()
	versionedRequestNodePool.Normalize(hcpNodePool)

	hcpNodePool.Name = request.PathValue(PathSegmentNodePoolName)
	csNodePool, err := f.BuildCSNodePool(ctx, hcpNodePool, updating)

	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if updating {
		f.logger.Info(fmt.Sprintf("updating resource %s", nodePoolResourceID))
		csNodePool, err = f.clusterServiceClient.UpdateCSNodePool(ctx, nodePoolDoc.InternalID, csNodePool)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		f.logger.Info(fmt.Sprintf("creating resource %s", nodePoolResourceID))
		csNodePool, err = f.clusterServiceClient.PostCSNodePool(ctx, clusterDoc.InternalID, csNodePool)
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		nodePoolDoc.InternalID, err = ocm.NewInternalID(csNodePool.HREF())
		if err != nil {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	// This is called directly when creating a resource, and indirectly from
	// within a retry loop when updating a resource.
	updateResourceMetadata := func(doc *database.ResourceDocument) bool {
		var docUpdated bool

		// Record the latest system data values from ARM, if present.
		if systemData != nil {
			nodePoolDoc.SystemData = systemData
			docUpdated = true
		}

		// Here the difference between a nil map and an empty map is significant.
		// If the Tags map is nil, that means it was omitted from the request body,
		// so we leave any existing tags alone. If the Tags map is non-nil, even if
		// empty, that means it was specified in the request body and should fully
		// replace any existing tags.
		if hcpNodePool.TrackedResource.Tags != nil {
			nodePoolDoc.Tags = hcpNodePool.TrackedResource.Tags
			docUpdated = true
		}

		return docUpdated
	}

	if !updating {
		updateResourceMetadata(nodePoolDoc)
		err = f.dbClient.CreateResourceDoc(ctx, nodePoolDoc)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to create document for %s: %v", nodePoolResourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
		f.logger.Info(fmt.Sprintf("document created for %s", nodePoolResourceID))
	} else {
		updated, err := f.dbClient.UpdateResourceDoc(ctx, nodePoolResourceID, updateResourceMetadata)
		if err != nil {
			f.logger.Error(fmt.Sprintf("failed to update document for %s: %v", nodePoolResourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
		if updated {
			f.logger.Info(fmt.Sprintf("document updated for %s", nodePoolResourceID))
		}
	}

	err = f.StartOperation(writer, request, operationRequest, nodePoolDoc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to write operation document: %v", err))
		arm.WriteInternalServerError(writer)
		return
	}

	responseBody, err := marshalCSNodePool(csNodePool, nodePoolDoc, versionedInterface)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(successStatusCode)

	_, err = writer.Write(responseBody)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

func (f *Frontend) DeleteNodePool(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: DeleteNodePool", versionedInterface))

	doc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("node pool document cannot be deleted -- document not found for %s", resourceID))
			writer.WriteHeader(http.StatusNoContent)
		} else {
			f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
		}
		return
	}

	err = f.clusterServiceClient.DeleteCSNodePool(ctx, doc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to delete node pool %s: %v", resourceID, err))
		arm.WriteInternalServerError(writer)
		return
	}

	err = f.StartOperation(writer, request, database.OperationRequestDelete, doc.InternalID)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to write operation document: %v", err))
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) OperationResult(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: OperationResult", versionedInterface))

	doc, err := f.dbClient.GetOperationDoc(ctx, resourceID.Name)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("operation '%s' not found", resourceID))
			writer.WriteHeader(http.StatusNotFound)
		} else {
			f.logger.Error(err.Error())
			writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, doc) {
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	if !doc.Status.IsTerminal() {
		f.AddLocationHeader(writer, request, doc)
		writer.WriteHeader(http.StatusAccepted)
		return
	}

	// The response henceforth should be exactly as though the operation
	// completed synchronously.

	var successStatusCode int

	switch doc.Request {
	case database.OperationRequestCreate:
		successStatusCode = http.StatusCreated
	case database.OperationRequestUpdate:
		successStatusCode = http.StatusOK
	case database.OperationRequestDelete:
		// XXX Ideally, deletion of Azure resources should never fail.
		//     In the event of failure, it's unclear what to do here.
		writer.WriteHeader(http.StatusNoContent)
		return
	default:
		f.logger.Error(fmt.Sprintf("Unhandled request type: %s", doc.Request))
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	responseBody, cloudError := f.MarshalResource(ctx, doc.ExternalID, versionedInterface)
	if cloudError != nil {
		writer.WriteHeader(cloudError.StatusCode)
		return
	}

	writer.WriteHeader(successStatusCode)

	_, err = writer.Write(responseBody)
	if err != nil {
		f.logger.Error(err.Error())
	}
}

// marshalCSNodePool renders a CS NodePool object in JSON format, applying
// the necessary conversions for the API version of the request.
func marshalCSNodePool(csNodePool *cmv1.NodePool, doc *database.ResourceDocument, versionedInterface api.Version) ([]byte, error) {
	hcpNodePool := ConvertCStoNodePool(doc.Key, csNodePool)
	hcpNodePool.TrackedResource.Resource.SystemData = doc.SystemData
	hcpNodePool.TrackedResource.Tags = maps.Clone(doc.Tags)

	return json.Marshal(versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool))
}
