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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/serverutils"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func (f *Frontend) GetExternalAuth(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := utils.ResourceIDFromContext(ctx) // used for error reporting
	if err != nil {
		return utils.TrackError(err)
	}

	resultingInternalExternalAuth, err := f.getInternalExternalAuthFromStorage(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return utils.TrackError(err)
	}
	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (f *Frontend) ArmResourceListExternalAuths(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	resourceName := request.PathValue(PathSegmentResourceName)

	internalCluster, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, resourceName)
	if err != nil {
		return utils.TrackError(err)
	}

	pagedResponse := arm.NewPagedResponse()

	externalAuthsByClusterServiceID := make(map[string]*api.HCPOpenShiftClusterExternalAuth)
	internalExternalAuthIterator, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(resourceName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		return utils.TrackError(err)
	}
	for _, externalAuth := range internalExternalAuthIterator.Items(ctx) {
		externalAuthsByClusterServiceID[externalAuth.ServiceProviderProperties.ClusterServiceID.ID()] = externalAuth
	}
	err = internalExternalAuthIterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}

	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalExternalAuthIterator.GetContinuationToken())
	if err != nil {
		return utils.TrackError(err)
	}

	// Build a Cluster Service query that looks for
	// the specific IDs returned by the Cosmos query.
	queryIDs := make([]string, 0, len(externalAuthsByClusterServiceID))
	for key := range externalAuthsByClusterServiceID {
		queryIDs = append(queryIDs, "'"+key+"'")
	}
	query := fmt.Sprintf("id in (%s)", strings.Join(queryIDs, ", "))
	logger.Info(fmt.Sprintf("Searching Cluster Service for %q", query))

	csIterator := f.clusterServiceClient.ListExternalAuths(internalCluster.ServiceProviderProperties.ClusterServiceID, query)
	for csExternalAuth := range csIterator.Items(ctx) {
		if internalExternalAuth, ok := externalAuthsByClusterServiceID[csExternalAuth.ID()]; ok {
			internalExternalAuth, err = mergeToInternalExternalAuth(csExternalAuth, internalExternalAuth)
			if err != nil {
				return utils.TrackError(err)
			}
			resultingExternalExternalAuth := versionedInterface.NewHCPOpenShiftClusterExternalAuth(internalExternalAuth)
			jsonBytes, err := arm.MarshalJSON(resultingExternalExternalAuth)
			if err != nil {
				return utils.TrackError(err)
			}
			pagedResponse.AddValue(jsonBytes)
		}
	}
	err = csIterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (f *Frontend) CreateOrUpdateExternalAuth(writer http.ResponseWriter, request *http.Request) error {
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

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	externalAuthCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)
	oldInternalExternalAuth, err := externalAuthCosmosClient.Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return utils.TrackError(err)
	}

	updating := oldInternalExternalAuth != nil
	if updating {
		// re-write oldInternalCluster for as long as cluster-service needs to be consulted for pre-existing state.
		oldInternalExternalAuth, err = f.readInternalExternalAuthFromClusterService(ctx, oldInternalExternalAuth)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalExternalAuth.ID, oldInternalExternalAuth.Properties.ProvisioningState); err != nil {
			return utils.TrackError(err)
		}

		switch request.Method {
		case http.MethodPut:
			return f.updateExternalAuth(writer, request, oldInternalExternalAuth)
		case http.MethodPatch:
			return f.patchExternalAuth(writer, request, oldInternalExternalAuth)
		default:
			return fmt.Errorf("unsupported method %s", request.Method)
		}
	}

	switch request.Method {
	case http.MethodPut:
		return f.createExternalAuth(writer, request)
	case http.MethodPatch:
		return arm.NewResourceNotFoundError(resourceID)
	default:
		return fmt.Errorf("unsupported method %s", request.Method)
	}
}

func decodeDesiredExternalAuthCreate(ctx context.Context) (*api.HCPOpenShiftClusterExternalAuth, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	externalExternalAuthFromRequest := versionedInterface.NewHCPOpenShiftClusterExternalAuth(&api.HCPOpenShiftClusterExternalAuth{})
	if err := json.Unmarshal(body, &externalExternalAuthFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}
	if err := externalExternalAuthFromRequest.SetDefaultValues(externalExternalAuthFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalExternalAuth := externalExternalAuthFromRequest.ConvertToInternal()
	if len(newInternalExternalAuth.Name) > 0 && newInternalExternalAuth.Name != resourceID.Name {
		return nil, nameResourceIDMismatch(resourceID, newInternalExternalAuth.Name)
	}

	// ProxyResource info doesn't to come from the external resource information
	conversion.CopyReadOnlyProxyResourceValues(&newInternalExternalAuth.ProxyResource, ptr.To(arm.NewProxyResource(resourceID)))

	// set fields that were not included during the conversion, because the user does not provide them or because the
	// data is determined live on read.
	newInternalExternalAuth.SystemData = systemData

	return newInternalExternalAuth, nil
}

func (f *Frontend) createExternalAuth(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	newInternalExternalAuth, err := decodeDesiredExternalAuthCreate(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateExternalAuthCreate(ctx, newInternalExternalAuth)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	cluster, err := f.getInternalClusterFromStorage(ctx, resourceID.Parent)
	if err != nil {
		return utils.TrackError(err)
	}
	if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestCreate, newInternalExternalAuth.ID, newInternalExternalAuth.Properties.ProvisioningState); err != nil {
		return utils.TrackError(err)
	}
	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, newInternalExternalAuth, false)
	if err != nil {
		return utils.TrackError(err)
	}
	csExternalAuth, err := f.clusterServiceClient.PostExternalAuth(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
	if err != nil {
		return utils.TrackError(err)
	}
	newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(csExternalAuth.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	operationRequest := database.OperationRequestCreate

	transaction := f.dbClient.NewTransaction(newInternalExternalAuth.ID.SubscriptionID)

	createExternalAuthOperation := database.NewOperation(
		operationRequest,
		newInternalExternalAuth.ID,
		newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID,
		f.azureLocation,
		request.Header.Get(arm.HeaderNameHomeTenantID),
		request.Header.Get(arm.HeaderNameClientObjectID),
		request.Header.Get(arm.HeaderNameAsyncNotificationURI),
		correlationData)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, createExternalAuthOperation.NotificationURI, createExternalAuthOperation.OperationID))
	_, err = f.dbClient.Operations(newInternalExternalAuth.ID.SubscriptionID).AddCreateToTransaction(ctx, transaction, createExternalAuthOperation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalExternalAuth.ServiceProviderProperties.ActiveOperationID = createExternalAuthOperation.ResourceID.Name
	newInternalExternalAuth.Properties.ProvisioningState = createExternalAuthOperation.Status

	externalAuthCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)
	cosmosUID, err := externalAuthCosmosClient.AddCreateToTransaction(ctx, transaction, newInternalExternalAuth, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastInternalExternalAuth, err := transactionResult.GetItem(cosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalExternalAuth, ok := resultingUncastInternalExternalAuth.(*api.HCPOpenShiftClusterExternalAuth)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalExternalAuth)
	}
	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalExternalAuth, err = mergeToInternalExternalAuth(csExternalAuth, resultingInternalExternalAuth)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusCreated, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func decodeDesiredExternalAuthReplace(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// Decoding for update has a series of semantics for determining the final desired update
	// 1. exact user request
	// 2. defaults for that version
	// 3. if not set, the values that the user doesn't necessary have to set but are not static defaults.  These from from the old value.
	// 4. values that are missing because the external type doesn't represent them
	// 5. values that might change because our machinery changes them.

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// Initialize versionedRequestExternalAuth to include both
	// non-zero default values and current read-only values.
	// Exact user request
	externalExternalAuthFromRequest := versionedInterface.NewHCPOpenShiftClusterExternalAuth(&api.HCPOpenShiftClusterExternalAuth{})
	if err := json.Unmarshal(body, &externalExternalAuthFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	// Default values
	if err := externalExternalAuthFromRequest.SetDefaultValues(externalExternalAuthFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalExternalAuth := externalExternalAuthFromRequest.ConvertToInternal()
	if len(newInternalExternalAuth.Name) > 0 && newInternalExternalAuth.Name != resourceID.Name {
		return nil, nameResourceIDMismatch(resourceID, newInternalExternalAuth.Name)
	}

	// ServiceProviderProperties contains two types of information
	// 1. values that a user cannot change because the external type does not expose the information.
	//    We must overwrite those values with the oldInternalCluster values so the values don't change, because the user's input will always be empty.
	// 2. values that a user cannot change due to validation requirements, but the user *can* specify the values.
	//    We are overwriting these values that we consider to be status values.
	//    We do this because if a user has read a value, then modified it, then replaces it, we don't want to produce
	//    validation errors on status fields that the user isn't trying to modify.
	conversion.CopyReadOnlyExternalAuthValues(newInternalExternalAuth, oldInternalExternalAuth)
	newInternalExternalAuth.SystemData = systemData

	return newInternalExternalAuth, nil
}

func (f *Frontend) updateExternalAuth(writer http.ResponseWriter, request *http.Request, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	ctx := request.Context()

	newInternalExternalAuth, err := decodeDesiredExternalAuthReplace(ctx, oldInternalExternalAuth)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateExternalAuthInCosmos(ctx, writer, request, http.StatusOK, newInternalExternalAuth, oldInternalExternalAuth)
}

func decodeDesiredExternalAuthPatch(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// TODO find a way to represent the desired change without starting from internal state here (very confusing)
	// TODO we appear to lack a test, but this seems to take an original, apply the patch and unmarshal the result, meaning the above patch step is just incorrect.
	newExternalExternalAuth := versionedInterface.NewHCPOpenShiftClusterExternalAuth(oldInternalExternalAuth)
	if err := api.ApplyRequestBody(http.MethodPatch, body, newExternalExternalAuth); err != nil {
		return nil, utils.TrackError(err)
	}
	newInternalExternalAuth := newExternalExternalAuth.ConvertToInternal()
	if len(newInternalExternalAuth.Name) > 0 && newInternalExternalAuth.Name != resourceID.Name {
		return nil, nameResourceIDMismatch(resourceID, newInternalExternalAuth.Name)
	}

	conversion.CopyReadOnlyExternalAuthValues(newInternalExternalAuth, oldInternalExternalAuth)
	newInternalExternalAuth.SystemData = systemData

	return newInternalExternalAuth, nil
}

func (f *Frontend) patchExternalAuth(writer http.ResponseWriter, request *http.Request, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.
	ctx := request.Context()

	newInternalExternalAuth, err := decodeDesiredExternalAuthPatch(ctx, oldInternalExternalAuth)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateExternalAuthInCosmos(ctx, writer, request, http.StatusAccepted, newInternalExternalAuth, oldInternalExternalAuth)
}

func (f *Frontend) updateExternalAuthInCosmos(ctx context.Context, writer http.ResponseWriter, request *http.Request, httpStatusCode int, newInternalExternalAuth, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	logger := utils.LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateExternalAuthUpdate(ctx, newInternalExternalAuth, oldInternalExternalAuth)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, newInternalExternalAuth, true)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("updating resource %s", oldInternalExternalAuth.ID))
	csExternalAuth, err := f.clusterServiceClient.UpdateExternalAuth(ctx, oldInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	transaction := f.dbClient.NewTransaction(oldInternalExternalAuth.ID.SubscriptionID)

	externalAuthUpdateOperation := database.NewOperation(
		database.OperationRequestUpdate,
		newInternalExternalAuth.ID,
		newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID,
		f.azureLocation,
		request.Header.Get(arm.HeaderNameHomeTenantID),
		request.Header.Get(arm.HeaderNameClientObjectID),
		request.Header.Get(arm.HeaderNameAsyncNotificationURI),
		correlationData)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, externalAuthUpdateOperation.NotificationURI, externalAuthUpdateOperation.OperationID))
	_, err = f.dbClient.Operations(newInternalExternalAuth.ID.SubscriptionID).AddCreateToTransaction(ctx, transaction, externalAuthUpdateOperation, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalExternalAuth.ServiceProviderProperties.ActiveOperationID = externalAuthUpdateOperation.ResourceID.Name
	newInternalExternalAuth.Properties.ProvisioningState = externalAuthUpdateOperation.Status

	_, err = f.dbClient.HCPClusters(newInternalExternalAuth.ID.SubscriptionID, newInternalExternalAuth.ID.ResourceGroupName).
		ExternalAuth(newInternalExternalAuth.ID.Parent.Name).
		AddReplaceToTransaction(ctx, transaction, newInternalExternalAuth, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastInternalExternalAuth, err := transactionResult.GetItem(oldInternalExternalAuth.ServiceProviderProperties.CosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalExternalAuth, ok := resultingUncastInternalExternalAuth.(*api.HCPOpenShiftClusterExternalAuth)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalExternalAuth)
	}
	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalExternalAuth, err = mergeToInternalExternalAuth(csExternalAuth, resultingInternalExternalAuth)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, httpStatusCode, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// DeleteExternalAuth implements the deletion API contract for ARM
// * 200 if a deletion is successful
// * 202 if an asynchronous delete is initiated
// * 204 if a well-formed request attempts to delete a nonexistent resource
func (f *Frontend) DeleteExternalAuth(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// when we get a delete call (this happens from CI quite a bit), dump the state of the cluster resources.
	if err := serverutils.DumpDataToLogger(ctx, f.dbClient, resourceID); err != nil {
		// never fail, this is best effort
		logger.Error(err.Error())
	}

	externalAuth, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		// For resource not found errors on deletion, ARM requires
		writer.WriteHeader(http.StatusNoContent)
		return nil
	}
	if err != nil {
		return utils.TrackError(err)
	}

	if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestDelete, externalAuth.ID, externalAuth.Properties.ProvisioningState); err != nil {
		return utils.TrackError(err)
	}

	err = f.clusterServiceClient.DeleteExternalAuth(ctx, externalAuth.ServiceProviderProperties.ClusterServiceID)
	var ocmError *ocmerrors.Error
	if errors.As(err, &ocmError) && ocmError.Status() == http.StatusNotFound {
		// StatusNotFound means we have stale data in Cosmos DB.
		// This can happen in test environments if a user bypasses
		// the RP to delete a resource (e.g. "ocm delete"). It can
		// also happen if an asynchronous deletion operation fails.
		// we will fall through and cancel all operations and go through as normal a deletion flow as we can to avoid
		// leaking data related to the resource, like controller status.
		logger.Info("clusterService externalauth missing, trying to clean up", "err", err)
	} else if err != nil {
		return utils.TrackError(err)
	}

	transaction := f.dbClient.NewTransaction(externalAuth.ID.SubscriptionID)
	if err := f.addDeleteExternalAuthToTransaction(ctx, writer, request, transaction, externalAuth); err != nil {
		return utils.TrackError(err)
	}
	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	writer.WriteHeader(http.StatusAccepted)
	return nil
}

func (f *Frontend) addDeleteExternalAuthToTransaction(ctx context.Context, writer http.ResponseWriter, request *http.Request, transaction database.DBTransaction, externalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Cluster Service will take care of canceling any ongoing operations
	// on the resource or child resources, but we need to do some database
	// bookkeeping to reflect that.
	err = f.CancelActiveOperations(ctx, transaction, &database.DBClientListActiveOperationDocsOptions{
		ExternalID:             externalAuth.ID,
		IncludeNestedResources: true,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	operationDoc := database.NewOperation(
		database.OperationRequestDelete,
		externalAuth.ID,
		externalAuth.ServiceProviderProperties.ClusterServiceID,
		f.azureLocation,
		"",
		"",
		"",
		correlationData)
	if request != nil {
		// these are optional because when this is triggered via the subscription deletion flow, there is no
		// deletion request containing these headers so these operations cannot be directly tracked.
		operationDoc.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
		operationDoc.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
		operationDoc.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
		transaction.OnSuccess(addOperationResponseHeaders(writer, request, operationDoc.NotificationURI, operationDoc.OperationID))
	}
	_, err = f.dbClient.Operations(operationDoc.OperationID.SubscriptionID).AddCreateToTransaction(ctx, transaction, operationDoc, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	externalAuth.ServiceProviderProperties.ActiveOperationID = operationDoc.ResourceID.Name
	externalAuth.Properties.ProvisioningState = operationDoc.Status
	_, err = f.dbClient.HCPClusters(externalAuth.ID.SubscriptionID, externalAuth.ID.ResourceGroupName).ExternalAuth(externalAuth.ID.Parent.Name).
		AddReplaceToTransaction(ctx, transaction, externalAuth, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

// the necessary conversions for the API version of the request.
// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
func mergeToInternalExternalAuth(csEternalAuth *arohcpv1alpha1.ExternalAuth, internalObj *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	mergedExternalAuth, err := ocm.ConvertCStoExternalAuth(internalObj.ID, csEternalAuth)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// this does not use conversion.CopyReadOnly* because some ServiceProvider properties come from cluster-service-only or live reads
	mergedExternalAuth.SystemData = internalObj.SystemData
	mergedExternalAuth.Properties.ProvisioningState = internalObj.Properties.ProvisioningState
	mergedExternalAuth.ServiceProviderProperties.CosmosUID = internalObj.ServiceProviderProperties.CosmosUID
	mergedExternalAuth.ServiceProviderProperties.ClusterServiceID = internalObj.ServiceProviderProperties.ClusterServiceID
	mergedExternalAuth.ServiceProviderProperties.ActiveOperationID = internalObj.ServiceProviderProperties.ActiveOperationID

	return mergedExternalAuth, nil
}

func (f *Frontend) getInternalExternalAuthFromStorage(ctx context.Context, resourceID *azcorearm.ResourceID) (*api.HCPOpenShiftClusterExternalAuth, error) {
	internalExternalAuth, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil, arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// Replace the ID field from Cosmos with the given resourceID,
	// which typically comes from the URL. This helps preserve the
	// casing of the resource group and resource name from the URL
	// to meet RPC requirements:
	//
	// Put Resource | Arguments
	//
	// The resource group names and resource names should be matched
	// case insensitively. ... Additionally, the Resource Provider must
	// preserve the casing provided by the user. The service must return
	// the most recently specified casing to the client and must not
	// normalize or return a toupper or tolower form of the resource
	// group or resource name. The resource group name and resource
	// name must come from the URL and not the request body.
	if !strings.EqualFold(internalExternalAuth.ID.String(), resourceID.String()) {
		return nil, fmt.Errorf("unexpected resourceID: %s", internalExternalAuth.ID.String())
	}
	internalExternalAuth.ID = resourceID

	return f.readInternalExternalAuthFromClusterService(ctx, internalExternalAuth)

}

// readInternalExternalAuthFromClusterService takes an internal ExternalAuth read from cosmos, retrieves the corresponding cluster-service data,
// merges the states together, and returns the internal representation.
func (f *Frontend) readInternalExternalAuthFromClusterService(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	return readInternalExternalAuthFromClusterService(ctx, f.clusterServiceClient, oldInternalExternalAuth, f.azureLocation)
}

func readInternalExternalAuthFromClusterService(ctx context.Context, clusterServiceClient ocm.ClusterServiceClientSpec, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth, azureLocation string) (*api.HCPOpenShiftClusterExternalAuth, error) {
	oldClusterServiceExternalAuth, err := clusterServiceClient.GetExternalAuth(ctx, oldInternalExternalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	oldInternalExternalAuth, err = mergeToInternalExternalAuth(oldClusterServiceExternalAuth, oldInternalExternalAuth)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return oldInternalExternalAuth, nil
}
