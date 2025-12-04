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
	"fmt"
	"net/http"
	"strings"

	"k8s.io/utils/ptr"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func (f *Frontend) GetExternalAuth(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}
	resourceID, err := ResourceIDFromContext(ctx) // used for error reporting
	if err != nil {
		return err
	}

	resultingInternalExternalAuth, err := f.getInternalExternalAuthFromStorage(ctx, resourceID)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return err
	}
	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		return err
	}

	return nil
}

func (f *Frontend) ArmResourceListExternalAuths(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	resourceName := request.PathValue(PathSegmentResourceName)

	internalCluster, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, resourceName)
	if err != nil {
		return err
	}

	pagedResponse := arm.NewPagedResponse()

	externalAuthsByClusterServiceID := make(map[string]*api.HCPOpenShiftClusterExternalAuth)
	internalExternalAuthIterator, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(resourceName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		return err
	}
	for _, externalAuth := range internalExternalAuthIterator.Items(ctx) {
		externalAuthsByClusterServiceID[externalAuth.ServiceProviderProperties.ClusterServiceID.ID()] = externalAuth
	}
	err = internalExternalAuthIterator.GetError()
	if err != nil {
		return err
	}

	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalExternalAuthIterator.GetContinuationToken())
	if err != nil {
		return err
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
				return err
			}
			resultingExternalExternalAuth := versionedInterface.NewHCPOpenShiftClusterExternalAuth(internalExternalAuth)
			jsonBytes, err := arm.MarshalJSON(resultingExternalExternalAuth)
			if err != nil {
				return err
			}
			pagedResponse.AddValue(jsonBytes)
		}
	}
	err = csIterator.GetError()
	if err != nil {
		return ocm.CSErrorToCloudError(err, nil, writer.Header())
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		return err
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

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return err
	}

	externalAuthCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)
	oldInternalExternalAuth, err := externalAuthCosmosClient.Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return err
	}

	updating := oldInternalExternalAuth != nil
	if updating {
		// re-write oldInternalCluster for as long as cluster-service needs to be consulted for pre-existing state.
		oldInternalExternalAuth, err = f.readInternalExternalAuthFromClusterService(ctx, oldInternalExternalAuth)
		if err != nil {
			return err
		}
		// CheckForProvisioningStateConflict does not log conflict errors
		// but does log unexpected errors like database failures.
		if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalExternalAuth.ID, oldInternalExternalAuth.Properties.ProvisioningState); err != nil {
			return err
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
		return nil, err
	}
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, err
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	externalExternalAuthFromRequest := versionedInterface.NewHCPOpenShiftClusterExternalAuth(&api.HCPOpenShiftClusterExternalAuth{})
	if err := json.Unmarshal(body, &externalExternalAuthFromRequest); err != nil {
		return nil, err
	}
	if err := externalExternalAuthFromRequest.SetDefaultValues(externalExternalAuthFromRequest); err != nil {
		return nil, err
	}

	newInternalExternalAuth := &api.HCPOpenShiftClusterExternalAuth{}
	externalExternalAuthFromRequest.Normalize(newInternalExternalAuth)
	// ProxyResource info doesn't to come from the external resource information
	conversion.CopyReadOnlyProxyResourceValues(&newInternalExternalAuth.ProxyResource, ptr.To(arm.NewProxyResource(resourceID)))

	// set fields that were not included during the conversion, because the user does not provide them or because the
	// data is determined live on read.
	newInternalExternalAuth.SystemData = systemData

	return newInternalExternalAuth, nil
}

func (f *Frontend) createExternalAuth(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return err
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return err
	}

	newInternalExternalAuth, err := decodeDesiredExternalAuthCreate(ctx)
	if err != nil {
		return err
	}

	validationErrs := validation.ValidateExternalAuthCreate(ctx, newInternalExternalAuth)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, newInternalExternalAuth, false)
	if err != nil {
		return err
	}
	cluster, err := f.getInternalClusterFromStorage(ctx, resourceID.Parent)
	if err != nil {
		return err
	}
	csExternalAuth, err := f.clusterServiceClient.PostExternalAuth(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
	if err != nil {
		return err
	}
	newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(csExternalAuth.HREF())
	if err != nil {
		return err
	}

	operationRequest := database.OperationRequestCreate

	pk := database.NewPartitionKey(newInternalExternalAuth.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	createExternalAuthOperation := database.NewOperationDocument(operationRequest, newInternalExternalAuth.ID, newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, correlationData)
	createExternalAuthOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	createExternalAuthOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	createExternalAuthOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(createExternalAuthOperation, nil)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, createExternalAuthOperation.NotificationURI, createExternalAuthOperation.OperationID))

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalExternalAuth.ServiceProviderProperties.ActiveOperationID = operationCosmosUID
	newInternalExternalAuth.Properties.ProvisioningState = createExternalAuthOperation.Status

	externalAuthCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)
	cosmosUID, err := externalAuthCosmosClient.AddCreateToTransaction(ctx, transaction, newInternalExternalAuth, nil)
	if err != nil {
		return err
	}

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return err
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastInternalExternalAuth, err := transactionResult.GetItem(cosmosUID)
	if err != nil {
		return err
	}
	resultingInternalExternalAuth, ok := resultingUncastInternalExternalAuth.(*api.HCPOpenShiftClusterExternalAuth)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalExternalAuth)
	}
	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalExternalAuth, err = mergeToInternalExternalAuth(csExternalAuth, resultingInternalExternalAuth)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusCreated, responseBytes)
	if err != nil {
		return err
	}
	return nil
}

func decodeDesiredExternalAuthReplace(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Decoding for update has a series of semantics for determining the final desired update
	// 1. exact user request
	// 2. defaults for that version
	// 3. if not set, the values that the user doesn't necessary have to set but are not static defaults.  These from from the old value.
	// 4. values that are missing because the external type doesn't represent them
	// 5. values that might change because our machinery changes them.

	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, err
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// Initialize versionedRequestExternalAuth to include both
	// non-zero default values and current read-only values.
	// Exact user request
	externalExternalAuthFromRequest := versionedInterface.NewHCPOpenShiftClusterExternalAuth(&api.HCPOpenShiftClusterExternalAuth{})
	if err := json.Unmarshal(body, &externalExternalAuthFromRequest); err != nil {
		return nil, err
	}

	// Default values
	if err := externalExternalAuthFromRequest.SetDefaultValues(externalExternalAuthFromRequest); err != nil {
		return nil, err
	}

	newInternalExternalAuth := &api.HCPOpenShiftClusterExternalAuth{}
	externalExternalAuthFromRequest.Normalize(newInternalExternalAuth)

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
		return err
	}

	return f.updateExternalAuthInCosmos(ctx, writer, request, http.StatusOK, newInternalExternalAuth, oldInternalExternalAuth)
}

func decodeDesiredExternalAuthPatch(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, err
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// TODO find a way to represent the desired change without starting from internal state here (very confusing)
	// TODO we appear to lack a test, but this seems to take an original, apply the patch and unmarshal the result, meaning the above patch step is just incorrect.
	newExternalExternalAuth := versionedInterface.NewHCPOpenShiftClusterExternalAuth(oldInternalExternalAuth)
	if err := api.ApplyRequestBody(http.MethodPatch, body, newExternalExternalAuth); err != nil {
		return nil, err
	}
	newInternalExternalAuth := &api.HCPOpenShiftClusterExternalAuth{}
	newExternalExternalAuth.Normalize(newInternalExternalAuth)

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
		return err
	}

	return f.updateExternalAuthInCosmos(ctx, writer, request, http.StatusAccepted, newInternalExternalAuth, oldInternalExternalAuth)
}

func (f *Frontend) updateExternalAuthInCosmos(ctx context.Context, writer http.ResponseWriter, request *http.Request, httpStatusCode int, newInternalExternalAuth, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) error {
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return err
	}

	validationErrs := validation.ValidateExternalAuthUpdate(ctx, newInternalExternalAuth, oldInternalExternalAuth)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return err
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, newInternalExternalAuth, true)
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("updating resource %s", oldInternalExternalAuth.ID))
	csExternalAuth, err := f.clusterServiceClient.UpdateExternalAuth(ctx, oldInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
	if err != nil {
		return err
	}

	pk := database.NewPartitionKey(oldInternalExternalAuth.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	externalAuthUpdateOperation := database.NewOperationDocument(database.OperationRequestUpdate, newInternalExternalAuth.ID, newInternalExternalAuth.ServiceProviderProperties.ClusterServiceID, correlationData)
	externalAuthUpdateOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	externalAuthUpdateOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	externalAuthUpdateOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(externalAuthUpdateOperation, nil)

	f.ExposeOperation(writer, request, operationCosmosUID, transaction)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationCosmosUID)
	patchOperations.SetProvisioningState(externalAuthUpdateOperation.Status)
	patchOperations.SetSystemData(newInternalExternalAuth.SystemData)

	transaction.PatchResourceDoc(oldInternalExternalAuth.ServiceProviderProperties.CosmosUID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return err
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastInternalExternalAuth, err := transactionResult.GetItem(oldInternalExternalAuth.ServiceProviderProperties.CosmosUID)
	if err != nil {
		return err
	}
	resultingInternalExternalAuth, ok := resultingUncastInternalExternalAuth.(*api.HCPOpenShiftClusterExternalAuth)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalExternalAuth)
	}
	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalExternalAuth, err = mergeToInternalExternalAuth(csExternalAuth, resultingInternalExternalAuth)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(resultingInternalExternalAuth))
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, httpStatusCode, responseBytes)
	if err != nil {
		return err
	}
	return nil
}

// the necessary conversions for the API version of the request.
// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
func mergeToInternalExternalAuth(csEternalAuth *arohcpv1alpha1.ExternalAuth, internalObj *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	mergedExternalAuth, err := ocm.ConvertCStoExternalAuth(internalObj.ID, csEternalAuth)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	return f.readInternalExternalAuthFromClusterService(ctx, internalExternalAuth)

}

// readInternalExternalAuthFromClusterService takes an internal ExternalAuth read from cosmos, retrieves the corresponding cluster-service data,
// merges the states together, and returns the internal representation.
func (f *Frontend) readInternalExternalAuthFromClusterService(ctx context.Context, oldInternalExternalAuth *api.HCPOpenShiftClusterExternalAuth) (*api.HCPOpenShiftClusterExternalAuth, error) {
	oldClusterServiceExternalAuth, err := f.clusterServiceClient.GetExternalAuth(ctx, oldInternalExternalAuth.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, err
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	oldInternalExternalAuth, err = mergeToInternalExternalAuth(oldClusterServiceExternalAuth, oldInternalExternalAuth)
	if err != nil {
		return nil, err
	}

	return oldInternalExternalAuth, nil
}
