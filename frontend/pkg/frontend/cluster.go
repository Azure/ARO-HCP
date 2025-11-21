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

	"dario.cat/mergo"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/validation"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/docker/docker/daemon/logger"
)

// GetHCPCluster implements the GET single resource API contract for HCP Clusters
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) GetHCPCluster(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	resourceID, err := ResourceIDFromContext(ctx) // used for error reporting
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resultingExternalCluster, cloudError := f.GetExternalClusterFromStorage(ctx, resourceID, versionedInterface)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) ArmResourceListClusters(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)

	pagedResponse := arm.NewPagedResponse()

	// Even though the bulk of the list content comes from Cluster Service,
	// we start by querying Cosmos DB because its continuation token meets
	// the requirements of a skipToken for ARM pagination. We then query
	// Cluster Service for the exact set of IDs returned by Cosmos.

	internalClusterIterator, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	clustersByClusterServiceID := make(map[string]*api.HCPOpenShiftCluster)
	for _, internalCluster := range internalClusterIterator.Items(ctx) {
		clustersByClusterServiceID[internalCluster.ServiceProviderProperties.ClusterServiceID.String()] = internalCluster
	}
	err = internalClusterIterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalClusterIterator.GetContinuationToken())
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Build a Cluster Service query that looks for
	// the specific IDs returned by the Cosmos query.
	queryIDs := make([]string, 0, len(clustersByClusterServiceID))
	for key := range clustersByClusterServiceID {
		queryIDs = append(queryIDs, "'"+key+"'")
	}
	query := fmt.Sprintf("id in (%s)", strings.Join(queryIDs, ", "))
	logger.Info(fmt.Sprintf("Searching Cluster Service for %q", query))

	csIterator := f.clusterServiceClient.ListClusters(query)

	for csCluster := range csIterator.Items(ctx) {
		if internalCluster, ok := clustersByClusterServiceID[csCluster.HREF()]; ok {
			resultingExternalCluster, err := mergeToExternalCluster(csCluster, internalCluster, versionedInterface)
			if err != nil {
				logger.Error(err.Error())
				arm.WriteInternalServerError(writer)
				return
			}
			jsonBytes, err := arm.MarshalJSON(resultingExternalCluster)
			if err != nil {
				logger.Error(err.Error())
				arm.WriteInternalServerError(writer)
				return
			}
			pagedResponse.AddValue(jsonBytes)
		}
	}
	err = csIterator.GetError()

	// Check for iteration error.
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, nil, writer.Header()))
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) CreateOrUpdateHCPCluster(writer http.ResponseWriter, request *http.Request) {
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

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	internalOldCluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	updating := internalOldCluster != nil
	if updating {
		switch request.Method {
		case http.MethodPut:
			f.updateHCPCluster(writer, request, internalOldCluster)

		case http.MethodPatch:
			f.patchHCPCluster(writer, request, internalOldCluster)

		default:
			logger.Error("unexpected method: " + request.Method)
			arm.WriteResourceNotFoundError(writer, resourceID)
		}

		return
	}

	f.createHCPCluster(writer, request)
}

func (f *Frontend) createHCPCluster(writer http.ResponseWriter, request *http.Request) {
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

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	switch request.Method {
	case http.MethodPut:
		// expected
	case http.MethodPatch:
		// PATCH requests never create a new resource.
		logger.Error("Resource not found")
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	default:
		logger.Error("unexpected method: " + request.Method)
		arm.WriteResourceNotFoundError(writer, resourceID)
		return

	}

	versionedInterface, err := VersionFromContext(ctx)
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

	// Initialize top-level resource fields from the request path.
	// If the request body specifies these fields, validation should
	// accept them as long as they match (case-insensitively) values
	// from the request path.
	newExternalCluster := versionedInterface.NewHCPOpenShiftCluster(api.NewDefaultHCPOpenShiftCluster(resourceID))
	successStatusCode := http.StatusCreated

	cloudError := api.ApplyRequestBody(request, body, newExternalCluster)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	// this sets many default values, which are then sometimes overridden by Normalize
	newInternalCluster := &api.HCPOpenShiftCluster{
		TrackedResource: arm.NewTrackedResource(resourceID),
	}
	newExternalCluster.Normalize(newInternalCluster)

	// set fields that were not included during the conversion, because the user does not provide them or because the
	// data is determined live on read.
	newInternalCluster.SystemData = systemData
	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	validationErrs := validation.ValidateClusterCreate(ctx, newInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs)
	if newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	// Now that validation is done we clear the user-assigned identities map since that is reconstructed from Cluster Service data
	// TODO this is bad, see above TODOs. We want to validate what we store.
	newInternalCluster.Identity.UserAssignedIdentities = nil

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(resourceID, request.Header, newInternalCluster, false)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.PostCluster(ctx, newClusterServiceClusterBuilder, nil)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
		return
	}

	newInternalCluster.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(resultingClusterServiceCluster.HREF())
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	// TODO extract to straight instance creation and then validation.
	clusterCreateOperation := database.NewOperationDocument(database.OperationRequestCreate, newInternalCluster.ID, newInternalCluster.ServiceProviderProperties.ClusterServiceID, correlationData)
	clusterCreateOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	clusterCreateOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	clusterCreateOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosID := transaction.CreateOperationDoc(clusterCreateOperation, nil)
	transaction.OnSuccess(operationNotificationFn(writer, request, clusterCreateOperation.NotificationURI, clusterCreateOperation.OperationID))

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalCluster.ServiceProviderProperties.ActiveOperationID = operationCosmosID
	newInternalCluster.ServiceProviderProperties.ProvisioningState = clusterCreateOperation.Status

	cosmosUID, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).AddCreateToTransaction(ctx, transaction, newInternalCluster, nil)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(cosmosUID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	resultingInternalCluster, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftCluster, database.HCPCluster](resultingCosmosCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resultingExternalCluster, err := mergeToExternalCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBytes)
	if err != nil {
		logger.Error(err.Error())
	}
}

// readInternalClusterFromClusterService takes an internal Cluster read from cosmos, retrieves the corresponding cluster-service data,
// merges the states together, and returns the internal representation.
// TODO remove the header it takes and collapse that to some general error handling.
func (f *Frontend) readInternalClusterFromClusterService(ctx context.Context, oldInternalCluster *api.HCPOpenShiftCluster, header http.Header) (*api.HCPOpenShiftCluster, *arm.CloudError) {
	logger := LoggerFromContext(ctx)

	oldClusterServiceCluster, err := f.clusterServiceClient.GetCluster(ctx, oldInternalCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, ocm.CSErrorToCloudError(err, oldInternalCluster.ID, header)
	}

	mergedOldClusterServiceCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(oldInternalCluster.ID, oldClusterServiceCluster)
	if err != nil {
		logger.Error(err.Error())
		return nil, arm.NewInternalServerError()
	}

	// Do not set the TrackedResource.Tags field here. We need
	// the Tags map to remain nil so we can see if the request
	// body included a new set of resource tags.

	mergedOldClusterServiceCluster.SystemData = oldInternalCluster.SystemData
	mergedOldClusterServiceCluster.ServiceProviderProperties.ProvisioningState = oldInternalCluster.ServiceProviderProperties.ProvisioningState
	if mergedOldClusterServiceCluster.Identity == nil {
		mergedOldClusterServiceCluster.Identity = &arm.ManagedServiceIdentity{}
	}
	if oldInternalCluster.Identity != nil {
		mergedOldClusterServiceCluster.Identity.PrincipalID = oldInternalCluster.Identity.PrincipalID
		mergedOldClusterServiceCluster.Identity.TenantID = oldInternalCluster.Identity.TenantID
		mergedOldClusterServiceCluster.Identity.Type = oldInternalCluster.Identity.Type
	}

	return mergedOldClusterServiceCluster, nil
}

func (f *Frontend) updateHCPCluster(writer http.ResponseWriter, request *http.Request, oldInternalCluster *api.HCPOpenShiftCluster) {
	// PUT requests overlay the request body onto a default resource
	// struct, which only has API-specified non-zero default values.
	// This means all required properties must be specified in the
	// request body, whether creating or updating a resource.

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if request.Method != http.MethodPut {
		logger.Error("unexpected method: " + request.Method)
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	}

	versionedInterface, err := VersionFromContext(ctx)
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

	var cloudErr *arm.CloudError
	oldInternalCluster, cloudErr = f.readInternalClusterFromClusterService(ctx, oldInternalCluster, writer.Header())
	if cloudErr != nil {
		logger.Error(cloudErr.Error())
		arm.WriteCloudError(writer, cloudErr)
		return
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalCluster.ID, oldInternalCluster.ServiceProviderProperties.ProvisioningState)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	var newInternalCluster *api.HCPOpenShiftCluster
	{ // scoped so that variables we want to remove aren't depended upon
		// Initialize versionedRequestCluster to include both
		// non-zero default values and current read-only values.
		defaultInternalClusterWithSomeFieldsSetToOldValues := api.NewDefaultHCPOpenShiftCluster(resourceID)

		// Some optional create-only fields have dynamic default
		// values that are determined downstream of this phase of
		// request processing. To ensure idempotency, add these
		// values to the target struct for the incoming request.
		defaultInternalClusterWithSomeFieldsSetToOldValues.CustomerProperties.Version.ID = oldInternalCluster.CustomerProperties.Version.ID
		defaultInternalClusterWithSomeFieldsSetToOldValues.CustomerProperties.DNS.BaseDomainPrefix = oldInternalCluster.CustomerProperties.DNS.BaseDomainPrefix
		defaultInternalClusterWithSomeFieldsSetToOldValues.CustomerProperties.Platform.ManagedResourceGroup = oldInternalCluster.CustomerProperties.Platform.ManagedResourceGroup

		// read-only values are an internal concern since they're the source, so we convert.
		// this could be faster done purely externally, but this allows a single set of rules for copying read only fields.
		conversion.CopyReadOnlyClusterValues(defaultInternalClusterWithSomeFieldsSetToOldValues, oldInternalCluster)

		defaultExternalClusterWithSomeFieldsSetToOldValues := versionedInterface.NewHCPOpenShiftCluster(defaultInternalClusterWithSomeFieldsSetToOldValues)

		externalClusterFromRequest := versionedInterface.NewHCPOpenShiftCluster(&api.HCPOpenShiftCluster{})
		err := json.Unmarshal(body, &externalClusterFromRequest)
		if err != nil {
			logger.Error(cloudError.Error())
			arm.WriteCloudError(writer, arm.NewInvalidRequestContentError(err))
			return
		}

		// this strategy means that our `newInternalCluster` is not what the user specified, but instead a series of overlays
		// 1. defaults (notice that these defaults are INTERNAL defaults, not external defaults)
		// 2. old values for some specific fields
		// 3. user requested values (notice that a user specifying nil for a default of old value will not actually clear that value)
		// 4. some forced overridden content
		newExternalCluster := defaultExternalClusterWithSomeFieldsSetToOldValues
		err = mergo.Merge(newExternalCluster, externalClusterFromRequest, mergo.WithOverride)
		if err != nil {
			logger.Error(cloudError.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		newExternalCluster.Normalize(newInternalCluster)

		newInternalCluster.SystemData = systemData
		// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
		// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
		// TODO we probably update validation to require this field is cleared.
		//newInternalCluster.Identity.UserAssignedIdentities = nil
	}

	f.updateHCPClusterInCosmos(ctx, writer, newInternalCluster, oldInternalCluster, versionedInterface)
}

func (f *Frontend) updateHCPClusterInCosmos(ctx context.Context, writer http.ResponseWriter, newInternalCluster, oldInternalCluster *api.HCPOpenShiftCluster, versionedInterface api.Version) {
	logger := LoggerFromContext(ctx)

	validationErrs := validation.ValidateClusterUpdate(ctx, newInternalCluster, oldInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	if newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs); newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	// Now that validation is done we clear the user-assigned identities map since that is reconstructed from Cluster Service data
	// TODO this is bad, see above TODOs. We want to validate what we store.
	newInternalCluster.Identity.UserAssignedIdentities = nil

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(oldInternalCluster.ID, request.Header, newInternalCluster, true)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	logger.Info(fmt.Sprintf("updating resource %s", oldInternalCluster.ID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.UpdateCluster(ctx, oldInternalCluster.ServiceProviderProperties.ClusterServiceID, newClusterServiceClusterBuilder)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, oldInternalCluster.ID, writer.Header()))
		return
	}

	pk := database.NewPartitionKey(oldInternalCluster.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)
	operationDoc := database.NewOperationDocument(database.OperationRequestUpdate, oldInternalCluster.ID, oldInternalCluster.ServiceProviderProperties.ClusterServiceID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values form ARM, if present.
	if newInternalCluster.SystemData != nil {
		patchOperations.SetSystemData(newInternalCluster.SystemData)
	}

	// Record managed identity type an any system-assigned identifiers.
	// Omit the user-assigned identities map since that is reconstructed
	// from Cluster Service data.
	patchOperations.SetIdentity(newInternalCluster.Identity)

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if newInternalCluster.Tags != nil {
		patchOperations.SetTags(newInternalCluster.Tags)
	}

	transaction.PatchResourceDoc(oldInternalCluster.ServiceProviderProperties.CosmosUID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(oldInternalCluster.ServiceProviderProperties.CosmosUID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	resultingInternalCluster, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftCluster, database.HCPCluster](resultingCosmosCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	resultingExternalCluster, err := mergeToExternalCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) patchHCPCluster(writer http.ResponseWriter, request *http.Request, oldInternalCluster *api.HCPOpenShiftCluster) {
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	if request.Method != http.MethodPatch {
		logger.Error("unexpected method: " + request.Method)
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	}

	versionedInterface, err := VersionFromContext(ctx)
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

	// re-write oldInternalCluster for as long as cluster-service needs to be consulted for pre-existing state.
	oldInternalCluster, cloudErr := f.readInternalClusterFromClusterService(ctx, oldInternalCluster, writer.Header())
	if cloudErr != nil {
		logger.Error(cloudErr.Error())
		arm.WriteCloudError(writer, cloudErr)
		return
	}
	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalCluster.ID, oldInternalCluster.ServiceProviderProperties.ProvisioningState)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	newInternalCluster := &api.HCPOpenShiftCluster{}
	{ // scoped to prevent accidental escapes of variables we want to remove
		// TODO find a way to represent the desired change without starting from internal state here (very confusing)
		// TODO we appear to lack a test, but this seems to take an original, apply the patch and unmarshal the result, meaning the above patch step is just incorrect.
		var newExternalCluster api.VersionedHCPOpenShiftCluster
		newExternalCluster = versionedInterface.NewHCPOpenShiftCluster(oldInternalCluster)
		cloudError = api.ApplyRequestBody(request, body, newExternalCluster)
		if cloudError != nil {
			logger.Error(cloudError.Error())
			arm.WriteCloudError(writer, cloudError)
			return
		}
		newExternalCluster.Normalize(newInternalCluster)
	}

	f.updateHCPClusterInCosmos(ctx, writer, newInternalCluster, oldInternalCluster, versionedInterface)
}
