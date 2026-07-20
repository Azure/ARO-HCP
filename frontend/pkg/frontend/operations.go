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
	"net/url"
	"path"
	"strings"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils/armhelpers"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

func (f *Frontend) OperationStatus(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	operation, err := f.resourcesDBClient.Operations(resourceID.SubscriptionID).Get(ctx, resourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, operation) {
		logger.Info("operation result not visible to requester")
		writer.WriteHeader(http.StatusNotFound)
		return nil
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, database.ToStatus(operation))
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (f *Frontend) OperationResult(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	operation, err := f.resourcesDBClient.Operations(resourceID.SubscriptionID).Get(ctx, resourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, operation) {
		return arm.NewResourceNotFoundError(resourceID)
	}

	// Handle non-terminal statuses and (maybe?) failure/cancellation.
	//
	// XXX ARM requirements for failed async operations get fuzzy here.
	//
	//     My best understanding, based on a Stack Overflow answer [1], is
	//     returning an Azure-AsyncOperation header will cause ARM to poll
	//     that endpoint first.
	//
	//     If ARM finds the operation in a "Failed" or "Canceled" state,
	//     it will propagate details from the response's "error" property.
	//
	//     If ARM finds the operation in a "Succeeded" state, ONLY THEN is
	//     this endpoint called (if a Location header was also returned).
	//
	//     So for the "Failed or Canceled" case we just give a generic
	//     "Internal Server Error" response since, in theory, this case
	//     should never be reached.
	//
	//     [1] https://stackoverflow.microsoft.com/a/318573/106707
	//
	switch operation.Status {
	case arm.ProvisioningStateSucceeded:
		// Handled below.
	case arm.ProvisioningStateFailed, arm.ProvisioningStateCanceled:
		return fmt.Errorf("invalid operation status: %s", operation.Status)
	default:
		// Operation is still in progress.
		AddLocationHeader(writer, request, operation.OperationID)
		writer.WriteHeader(http.StatusAccepted)
		return nil
	}

	// The response henceforth should be exactly as though the operation
	// succeeded synchronously.

	var successStatusCode int

	switch operation.Request {
	case database.OperationRequestCreate:
		successStatusCode = http.StatusCreated
	case database.OperationRequestUpdate:
		successStatusCode = http.StatusOK
	case database.OperationRequestDelete:
		writer.WriteHeader(http.StatusNoContent)
		return nil
	case database.OperationRequestRequestCredential:
		successStatusCode = http.StatusOK
	case database.OperationRequestRevokeCredentials:
		writer.WriteHeader(http.StatusNoContent)
		return nil
	default:
		return fmt.Errorf("unhandled request type: %s", operation.Request)
	}

	var responseBody []byte

	switch {
	case operation.InternalID.Kind() == cmv1.BreakGlassCredentialKind:
		csBreakGlassCredential, err := f.clusterServiceClient.GetBreakGlassCredential(ctx, operation.InternalID)
		if err != nil {
			return utils.TrackError(err)
		}

		responseBody, err = versionedInterface.MarshalHCPOpenShiftClusterAdminCredential(ocm.ConvertCStoAdminCredential(csBreakGlassCredential))
		if err != nil {
			return utils.TrackError(err)
		}

	case armhelpers.ResourceTypeEqual(operation.ExternalID.ResourceType, api.ClusterResourceType):
		resultingInternalCluster, err := f.getInternalClusterFromStorage(ctx, operation.ExternalID)
		if err != nil {
			return utils.TrackError(err)
		}
		responseBody, err = arm.MarshalJSON(versionedInterface.NewHCPOpenShiftCluster(resultingInternalCluster))
		if err != nil {
			return utils.TrackError(err)
		}

	case operation.ExternalID.ResourceType.String() == api.NodePoolResourceType.String():
		resultingInternalNodePool, err := f.getInternalNodePoolFromStorage(ctx, operation.ExternalID)
		if err != nil {
			return utils.TrackError(err)
		}
		responseBody, err = arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(resultingInternalNodePool))
		if err != nil {
			return utils.TrackError(err)
		}

	case operation.ExternalID.ResourceType.String() == api.ExternalAuthResourceType.String():
		resultingInternalExternalAuth, err := f.getInternalExternalAuthFromStorage(ctx, operation.ExternalID)
		if err != nil {
			return utils.TrackError(err)
		}
		responseBody, err = arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
		if err != nil {
			return utils.TrackError(err)
		}

	default:
		return fmt.Errorf("unsupported operation reference: %s", operation.ExternalID)
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBody)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// AddAsyncOperationHeader adds an "Azure-AsyncOperation" header to the ResponseWriter
// with a URL of the operation status endpoint.
func AddAsyncOperationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err, "failed to parse request referer")
		return
	}

	u.Path = operationID.String()

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set(arm.HeaderNameAsyncOperation, u.String())
}

// AddLocationHeader adds a "Location" header to the ResponseWriter with a URL of the
// operation result endpoint.
func AddLocationHeader(writer http.ResponseWriter, request *http.Request, operationID *azcorearm.ResourceID) {
	logger := utils.LoggerFromContext(request.Context())

	// MiddlewareReferer ensures Referer is present.
	u, err := url.ParseRequestURI(request.Referer())
	if err != nil {
		logger.Error(err, "failed to parse request referer")
		return
	}

	u.Path = path.Join("/",
		"subscriptions", operationID.SubscriptionID,
		"providers", operationID.ResourceType.Namespace,
		"locations", operationID.Location,
		api.OperationResultResourceTypeName, operationID.Name)

	apiVersion := request.URL.Query().Get(APIVersionKey)
	if apiVersion != "" {
		values := u.Query()
		values.Set(APIVersionKey, apiVersion)
		u.RawQuery = values.Encode()
	}

	writer.Header().Set("Location", u.String())
}

// OperationIsVisible returns true if the request is being called from the same
// tenant and subscription that the operation originated in.
func (f *Frontend) OperationIsVisible(request *http.Request, operation *api.Operation) bool {
	var visible = true

	logger := utils.LoggerFromContext(request.Context())

	tenantID := request.Header.Get(arm.HeaderNameHomeTenantID)
	clientID := request.Header.Get(arm.HeaderNameClientObjectID)
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	if operation.OperationID != nil {
		if operation.TenantID != "" && !strings.EqualFold(tenantID, operation.TenantID) {
			logger.Info(fmt.Sprintf("Unauthorized tenant '%s' in status request for operation '%s'", tenantID, operation.OperationID))
			visible = false
		}

		if operation.ClientID != "" && !strings.EqualFold(clientID, operation.ClientID) {
			logger.Info(fmt.Sprintf("Unauthorized client '%s' in status request for operation '%s'", clientID, operation.OperationID))
			visible = false
		}

		if !strings.EqualFold(subscriptionID, operation.OperationID.SubscriptionID) {
			logger.Info(fmt.Sprintf("Unauthorized subscription '%s' in status request for operation '%s'", subscriptionID, operation.OperationID))
			visible = false
		}
	} else {
		logger.Info(fmt.Sprintf("Status request for implicit operation '%s'", operation.OperationID))
		visible = false
	}

	return visible
}
