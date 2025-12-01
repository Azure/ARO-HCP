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
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// GetHCPCluster implements the GET single resource API contract for HCP Clusters
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) GetHCPCluster(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}
	resourceID, err := ResourceIDFromContext(ctx) // used for error reporting
	if err != nil {
		return err
	}

	resultingExternalCluster, err := f.GetExternalClusterFromStorage(ctx, resourceID, versionedInterface)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		return err
	}

	return nil
}

func (f *Frontend) ArmResourceListClusters(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
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
		return err
	}
	clustersByClusterServiceID := make(map[string]*api.HCPOpenShiftCluster)
	for _, internalCluster := range internalClusterIterator.Items(ctx) {
		clustersByClusterServiceID[internalCluster.ServiceProviderProperties.ClusterServiceID.ID()] = internalCluster
	}
	err = internalClusterIterator.GetError()
	if err != nil {
		return err
	}
	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalClusterIterator.GetContinuationToken())
	if err != nil {
		return err
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
		if internalCluster, ok := clustersByClusterServiceID[csCluster.ID()]; ok {
			resultingExternalCluster, err := mergeToExternalCluster(csCluster, internalCluster, versionedInterface)
			if err != nil {
				return err
			}
			jsonBytes, err := arm.MarshalJSON(resultingExternalCluster)
			if err != nil {
				return err
			}
			pagedResponse.AddValue(jsonBytes)
		}
	}
	err = csIterator.GetError()

	// Check for iteration error.
	if err != nil {
		return ocm.CSErrorToCloudError(err, nil, writer.Header())
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		return err
	}

	return nil
}

func (f *Frontend) CreateOrUpdateHCPCluster(writer http.ResponseWriter, request *http.Request) error {
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

	resourceItemID, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return err
	}

	updating := resourceDoc != nil

	if updating {
		return f.updateHCPCluster(writer, request, resourceItemID, resourceDoc)
	}

	return f.createHCPCluster(writer, request)
}

func (f *Frontend) createHCPCluster(writer http.ResponseWriter, request *http.Request) error {
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
		return err
	}

	switch request.Method {
	case http.MethodPut:
		// expected
	case http.MethodPatch:
		return arm.NewResourceNotFoundError(resourceID)
	default:
		return fmt.Errorf("unsupported method %s", request.Method)

	}

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return err
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		return err
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return err
	}

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return err
	}

	// Initialize top-level resource fields from the request path.
	// If the request body specifies these fields, validation should
	// accept them as long as they match (case-insensitively) values
	// from the request path.
	newExternalCluster := versionedInterface.NewHCPOpenShiftCluster(api.NewDefaultHCPOpenShiftCluster(resourceID))
	successStatusCode := http.StatusCreated

	if err := api.ApplyRequestBody(request, body, newExternalCluster); err != nil {
		return err
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
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return err
	}

	// Now that validation is done we clear the user-assigned identities map since that is reconstructed from Cluster Service data
	// TODO this is bad, see above TODOs. We want to validate what we store.
	newInternalCluster.Identity.UserAssignedIdentities = nil

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(resourceID, request.Header, newInternalCluster, false)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.PostCluster(ctx, newClusterServiceClusterBuilder, nil)
	if err != nil {
		return ocm.CSErrorToCloudError(err, resourceID, writer.Header())
	}

	newInternalCluster.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(resultingClusterServiceCluster.HREF())
	if err != nil {
		return err
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
	// TODO once we we have separate creation/vaidation of operation documents, this can be done ahead of time.
	newInternalCluster.ServiceProviderProperties.ActiveOperationID = operationCosmosID
	newInternalCluster.ServiceProviderProperties.ProvisioningState = clusterCreateOperation.Status

	cosmosUID, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).AddCreateToTransaction(ctx, transaction, newInternalCluster, nil)
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
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(cosmosUID)
	if err != nil {
		return err
	}
	resultingInternalCluster, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftCluster, database.HCPCluster](resultingCosmosCluster)
	if err != nil {
		return err
	}

	resultingExternalCluster, err := mergeToExternalCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBytes)
	if err != nil {
		return err
	}
	return nil
}

func (f *Frontend) updateHCPCluster(writer http.ResponseWriter, request *http.Request, cosmosID string, oldCosmosCluster *database.ResourceDocument) error {
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
		return err
	}

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return err
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		return err
	}

	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return err
	}

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return err
	}

	operationRequest := database.OperationRequestUpdate
	var oldExternalCluster api.VersionedHCPOpenShiftCluster
	var newExternalCluster api.VersionedHCPOpenShiftCluster
	var successStatusCode int

	oldClusterServiceCluster, err := f.clusterServiceClient.GetCluster(ctx, oldCosmosCluster.InternalID)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to fetch CS cluster for %s: %v", resourceID, err))
		return ocm.CSErrorToCloudError(err, resourceID, writer.Header())
	}

	internalOldCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID, oldClusterServiceCluster)
	if err != nil {
		return err
	}

	// Do not set the TrackedResource.Tags field here. We need
	// the Tags map to remain nil so we can see if the request
	// body included a new set of resource tags.

	internalOldCluster.SystemData = oldCosmosCluster.SystemData
	internalOldCluster.ServiceProviderProperties.ProvisioningState = oldCosmosCluster.ProvisioningState
	if internalOldCluster.Identity == nil {
		internalOldCluster.Identity = &arm.ManagedServiceIdentity{}
	}

	if oldCosmosCluster.Identity != nil {
		internalOldCluster.Identity.PrincipalID = oldCosmosCluster.Identity.PrincipalID
		internalOldCluster.Identity.TenantID = oldCosmosCluster.Identity.TenantID
		internalOldCluster.Identity.Type = oldCosmosCluster.Identity.Type
	}

	// This is slightly repetitive for the sake of clarity on PUT vs PATCH.
	switch request.Method {
	case http.MethodPut:
		// Initialize versionedRequestCluster to include both
		// non-zero default values and current read-only values.
		newInternalCluster := api.NewDefaultHCPOpenShiftCluster(resourceID)

		// Some optional create-only fields have dynamic default
		// values that are determined downstream of this phase of
		// request processing. To ensure idempotency, add these
		// values to the target struct for the incoming request.
		newInternalCluster.CustomerProperties.Version.ID = internalOldCluster.CustomerProperties.Version.ID
		newInternalCluster.CustomerProperties.DNS.BaseDomainPrefix = internalOldCluster.CustomerProperties.DNS.BaseDomainPrefix
		newInternalCluster.CustomerProperties.Platform.ManagedResourceGroup = internalOldCluster.CustomerProperties.Platform.ManagedResourceGroup

		// read-only values are an internal concern since they're the source, so we convert.
		// this could be faster done purely externally, but this allows a single set of rules for copying read only fields.
		conversion.CopyReadOnlyClusterValues(newInternalCluster, internalOldCluster)
		oldExternalCluster = versionedInterface.NewHCPOpenShiftCluster(internalOldCluster)
		newExternalCluster = versionedInterface.NewHCPOpenShiftCluster(newInternalCluster)

		successStatusCode = http.StatusOK

	case http.MethodPatch:
		oldExternalCluster = versionedInterface.NewHCPOpenShiftCluster(internalOldCluster)
		// TODO find a way to represent the desired change without starting from internal state here (very confusing)
		newExternalCluster = versionedInterface.NewHCPOpenShiftCluster(internalOldCluster)
		successStatusCode = http.StatusAccepted
	default:
		return fmt.Errorf("unsupported method %s", request.Method)
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	if err := f.CheckForProvisioningStateConflict(ctx, operationRequest, oldCosmosCluster); err != nil {
		return err
	}

	// TODO we appear to lack a test, but this seems to take an original, apply the patch and unmarshal the result, meaning the above patch step is just incorrect.
	if err := api.ApplyRequestBody(request, body, newExternalCluster); err != nil {
		return err
	}

	newInternalCluster := &api.HCPOpenShiftCluster{}
	newExternalCluster.Normalize(newInternalCluster)

	oldInternalCluster := &api.HCPOpenShiftCluster{}
	oldExternalCluster.Normalize(oldInternalCluster)
	validationErrs := validation.ValidateClusterUpdate(ctx, newInternalCluster, oldInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return err
	}

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(resourceID, request.Header, newInternalCluster, true)
	if err != nil {
		return err
	}

	logger.Info(fmt.Sprintf("updating resource %s", resourceID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.UpdateCluster(ctx, oldCosmosCluster.InternalID, newClusterServiceClusterBuilder)
	if err != nil {
		return ocm.CSErrorToCloudError(err, resourceID, writer.Header())
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)
	operationDoc := database.NewOperationDocument(operationRequest, oldCosmosCluster.ResourceID, oldCosmosCluster.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values form ARM, if present.
	if systemData != nil {
		patchOperations.SetSystemData(systemData)
	}

	// Record managed identity type an any system-assigned identifiers.
	// Omit the user-assigned identities map since that is reconstructed
	// from Cluster Service data.
	patchOperations.SetIdentity(&arm.ManagedServiceIdentity{
		PrincipalID: newInternalCluster.Identity.PrincipalID,
		TenantID:    newInternalCluster.Identity.TenantID,
		Type:        newInternalCluster.Identity.Type,
	})

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if newInternalCluster.Tags != nil {
		patchOperations.SetTags(newInternalCluster.Tags)
	}

	transaction.PatchResourceDoc(cosmosID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return err
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(cosmosID)
	if err != nil {
		return err
	}
	resultingInternalCluster, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftCluster, database.HCPCluster](resultingCosmosCluster)
	if err != nil {
		return err
	}

	resultingExternalCluster, err := mergeToExternalCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
	if err != nil {
		return err
	}
	responseBytes, err := arm.MarshalJSON(resultingExternalCluster)
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBytes)
	if err != nil {
		return err
	}
	return nil
}
