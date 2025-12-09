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
	"maps"
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
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

// GetHCPCluster implements the GET single resource API contract for HCP Clusters
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) GetHCPCluster(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := ResourceIDFromContext(ctx) // used for error reporting
	if err != nil {
		return utils.TrackError(err)
	}

	resultingInternalCluster, err := f.getInternalClusterFromStorage(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftCluster(resultingInternalCluster))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (f *Frontend) ArmResourceListClusters(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
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
		return utils.TrackError(err)
	}
	clustersByClusterServiceID := make(map[string]*api.HCPOpenShiftCluster)
	for _, internalCluster := range internalClusterIterator.Items(ctx) {
		clustersByClusterServiceID[internalCluster.ServiceProviderProperties.ClusterServiceID.ID()] = internalCluster
	}
	err = internalClusterIterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}
	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalClusterIterator.GetContinuationToken())
	if err != nil {
		return utils.TrackError(err)
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
			// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
			internalCluster, err = mergeToInternalCluster(csCluster, internalCluster)
			if err != nil {
				return utils.TrackError(err)
			}
			resultingExternalCluster := versionedInterface.NewHCPOpenShiftCluster(internalCluster)
			jsonBytes, err := arm.MarshalJSON(resultingExternalCluster)
			if err != nil {
				return utils.TrackError(err)
			}
			pagedResponse.AddValue(jsonBytes)
		}
	}
	// Check for iteration error.
	if err := csIterator.GetError(); err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		return utils.TrackError(err)
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
		return utils.TrackError(err)
	}

	oldInternalCluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return utils.TrackError(err)
	}

	updating := oldInternalCluster != nil
	if updating {
		// re-write oldInternalCluster for as long as cluster-service needs to be consulted for pre-existing state.
		oldInternalCluster, err = f.readInternalClusterFromClusterService(ctx, oldInternalCluster)
		if err != nil {
			return utils.TrackError(err)
		}
		// CheckForProvisioningStateConflict does not log conflict errors
		// but does log unexpected errors like database failures.
		if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalCluster.ID, oldInternalCluster.ServiceProviderProperties.ProvisioningState); err != nil {
			return utils.TrackError(err)
		}

		switch request.Method {
		case http.MethodPut:
			return f.updateHCPCluster(writer, request, oldInternalCluster)
		case http.MethodPatch:
			return f.patchHCPCluster(writer, request, oldInternalCluster)
		default:
			return fmt.Errorf("unsupported method %s", request.Method)
		}
	}

	switch request.Method {
	case http.MethodPut:
		return f.createHCPCluster(writer, request)
	case http.MethodPatch:
		return arm.NewResourceNotFoundError(resourceID)
	default:
		return fmt.Errorf("unsupported method %s", request.Method)
	}
}

func decodeDesiredClusterCreate(ctx context.Context) (*api.HCPOpenShiftCluster, error) {
	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	resourceID, err := ResourceIDFromContext(ctx)
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

	externalClusterFromRequest := versionedInterface.NewHCPOpenShiftCluster(&api.HCPOpenShiftCluster{})
	if err := json.Unmarshal(body, &externalClusterFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}
	if err := externalClusterFromRequest.SetDefaultValues(externalClusterFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalCluster := &api.HCPOpenShiftCluster{}
	externalClusterFromRequest.Normalize(newInternalCluster)
	// TrackedResource info doesn't appear to come from the external resource information
	conversion.CopyReadOnlyTrackedResourceValues(&newInternalCluster.TrackedResource, ptr.To(arm.NewTrackedResource(resourceID)))

	// set fields that were not included during the conversion, because the user does not provide them or because the
	// data is determined live on read.
	newInternalCluster.SystemData = systemData
	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	return newInternalCluster, nil
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

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	newInternalCluster, err := decodeDesiredClusterCreate(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateClusterCreate(ctx, newInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	// Now that validation is done we clear the user-assigned identities map since that is reconstructed from Cluster Service data
	// TODO this is bad, see above TODOs. We want to validate what we store.
	newInternalCluster.Identity.UserAssignedIdentities = nil

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(newInternalCluster.ID, request.Header, newInternalCluster, false)
	if err != nil {
		return utils.TrackError(err)
	}
	logger.Info(fmt.Sprintf("creating resource %s", newInternalCluster.ID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.PostCluster(ctx, newClusterServiceClusterBuilder, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	newInternalCluster.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(resultingClusterServiceCluster.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	pk := database.NewPartitionKey(newInternalCluster.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	// TODO extract to straight instance creation and then validation.
	clusterCreateOperation := database.NewOperationDocument(database.OperationRequestCreate, newInternalCluster.ID, newInternalCluster.ServiceProviderProperties.ClusterServiceID, correlationData)
	clusterCreateOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	clusterCreateOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	clusterCreateOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(clusterCreateOperation, nil)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, clusterCreateOperation.NotificationURI, clusterCreateOperation.OperationID))

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalCluster.ServiceProviderProperties.ActiveOperationID = operationCosmosUID
	newInternalCluster.ServiceProviderProperties.ProvisioningState = clusterCreateOperation.Status

	cosmosUID, err := f.dbClient.HCPClusters(newInternalCluster.ID.SubscriptionID, newInternalCluster.ID.ResourceGroupName).AddCreateToTransaction(ctx, transaction, newInternalCluster, nil)
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
	resultingUncastInternalCluster, err := transactionResult.GetItem(cosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalCluster, ok := resultingUncastInternalCluster.(*api.HCPOpenShiftCluster)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalCluster)
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalCluster, err = mergeToInternalCluster(resultingClusterServiceCluster, resultingInternalCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftCluster(resultingInternalCluster))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusCreated, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func decodeDesiredClusterReplace(ctx context.Context, oldInternalCluster *api.HCPOpenShiftCluster) (*api.HCPOpenShiftCluster, error) {
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

	body, err := BodyFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	systemData, err := SystemDataFromContext(ctx)
	if err != nil {
		return nil, utils.TrackError(err)
	}
	// Exact user request
	externalClusterFromRequest := versionedInterface.NewHCPOpenShiftCluster(&api.HCPOpenShiftCluster{})
	if err := json.Unmarshal(body, &externalClusterFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	// Default values
	if err := externalClusterFromRequest.SetDefaultValues(externalClusterFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalCluster := &api.HCPOpenShiftCluster{}
	externalClusterFromRequest.Normalize(newInternalCluster)

	// values a user doesn't have to provide, but are not static defaults (set dynamically during create).  Set these from old value
	if len(newInternalCluster.CustomerProperties.Version.ID) == 0 {
		newInternalCluster.CustomerProperties.Version.ID = oldInternalCluster.CustomerProperties.Version.ID
	}
	if len(newInternalCluster.CustomerProperties.DNS.BaseDomainPrefix) == 0 {
		newInternalCluster.CustomerProperties.DNS.BaseDomainPrefix = oldInternalCluster.CustomerProperties.DNS.BaseDomainPrefix
	}
	if len(newInternalCluster.CustomerProperties.Platform.ManagedResourceGroup) == 0 {
		newInternalCluster.CustomerProperties.Platform.ManagedResourceGroup = oldInternalCluster.CustomerProperties.Platform.ManagedResourceGroup
	}

	// ServiceProviderProperties contains two types of information
	// 1. values that a user cannot change because the external type does not expose the information.
	//    We must overwrite those values with the oldInternalCluster values so the values don't change, because the user's input will always be empty.
	// 2. values that a user cannot change due to validation requirements, but the user *can* specify the values.
	//    We are overwriting these values that we consider to be status values.
	//    We do this because if a user has read a value, then modified it, then replaces it, we don't want to produce
	//    validation errors on status fields that the user isn't trying to modify.
	conversion.CopyReadOnlyClusterValues(newInternalCluster, oldInternalCluster)
	newInternalCluster.SystemData = systemData

	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	return newInternalCluster, nil
}

func (f *Frontend) updateHCPCluster(writer http.ResponseWriter, request *http.Request, oldInternalCluster *api.HCPOpenShiftCluster) error {
	// PUT requests overlay the request body onto a default resource
	// struct, which only has API-specified non-zero default values.
	// This means all required properties must be specified in the
	// request body, whether creating or updating a resource.

	ctx := request.Context()

	newInternalCluster, err := decodeDesiredClusterReplace(ctx, oldInternalCluster)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateHCPClusterInCosmos(ctx, writer, request, http.StatusOK, newInternalCluster, oldInternalCluster)
}

func decodeDesiredClusterPatch(ctx context.Context, oldInternalCluster *api.HCPOpenShiftCluster) (*api.HCPOpenShiftCluster, error) {
	versionedInterface, err := VersionFromContext(ctx)
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
	var newExternalCluster = versionedInterface.NewHCPOpenShiftCluster(oldInternalCluster)
	if err := api.ApplyRequestBody(http.MethodPatch, body, newExternalCluster); err != nil {
		return nil, utils.TrackError(err)
	}
	newInternalCluster := &api.HCPOpenShiftCluster{}
	newExternalCluster.Normalize(newInternalCluster)

	// ServiceProviderProperties contains two types of information
	// 1. values that a user cannot change because the external type does not expose the information.
	//    We must overwrite those values with the oldInternalCluster values so the values don't change, because the user's input will always be empty.
	// 2. values that a user cannot change due to validation requirements, but the user *can* specify the values.
	//    We are overwriting these values that we consider to be status values.
	//    We do this because if a user has read a value, then modified it, then replaces it, we don't want to produce
	//    validation errors on status fields that the user isn't trying to modify.
	conversion.CopyReadOnlyClusterValues(newInternalCluster, oldInternalCluster)
	newInternalCluster.SystemData = systemData
	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	return newInternalCluster, nil
}

func (f *Frontend) patchHCPCluster(writer http.ResponseWriter, request *http.Request, oldInternalCluster *api.HCPOpenShiftCluster) error {
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.
	ctx := request.Context()

	newInternalCluster, err := decodeDesiredClusterPatch(ctx, oldInternalCluster)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateHCPClusterInCosmos(ctx, writer, request, http.StatusAccepted, newInternalCluster, oldInternalCluster)
}

func (f *Frontend) updateHCPClusterInCosmos(ctx context.Context, writer http.ResponseWriter, request *http.Request, httpStatusCode int, newInternalCluster, oldInternalCluster *api.HCPOpenShiftCluster) error {
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateClusterUpdate(ctx, newInternalCluster, oldInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	// Now that validation is done we clear the user-assigned identities map since that is reconstructed from Cluster Service data
	// TODO this is bad, see above TODOs. We want to validate what we store.
	newInternalCluster.Identity.UserAssignedIdentities = nil

	newClusterServiceClusterBuilder, err := ocm.BuildCSCluster(oldInternalCluster.ID, request.Header, newInternalCluster, true)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("updating resource %s", oldInternalCluster.ID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.UpdateCluster(ctx, oldInternalCluster.ServiceProviderProperties.ClusterServiceID, newClusterServiceClusterBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	pk := database.NewPartitionKey(oldInternalCluster.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)
	clusterUpdateOperation := database.NewOperationDocument(database.OperationRequestUpdate, oldInternalCluster.ID, oldInternalCluster.ServiceProviderProperties.ClusterServiceID, correlationData)
	clusterUpdateOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	clusterUpdateOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	clusterUpdateOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(clusterUpdateOperation, nil)

	f.ExposeOperation(writer, request, operationCosmosUID, transaction)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationCosmosUID)
	patchOperations.SetProvisioningState(clusterUpdateOperation.Status)

	// Record the latest system data values form ARM, if present.
	patchOperations.SetSystemData(newInternalCluster.SystemData)

	// Record managed identity type an any system-assigned identifiers.
	// Omit the user-assigned identities map since that is reconstructed
	// from Cluster Service data.
	patchOperations.SetIdentity(newInternalCluster.Identity)

	// TODO is this statement also true for PUT?
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
		return utils.TrackError(err)
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastObj, err := transactionResult.GetItem(oldInternalCluster.ServiceProviderProperties.CosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalCluster := resultingUncastObj.(*api.HCPOpenShiftCluster)

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalCluster, err = mergeToInternalCluster(resultingClusterServiceCluster, resultingInternalCluster)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftCluster(resultingInternalCluster))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, httpStatusCode, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// mergeToInternalCluster renders a CS Cluster object in JSON format, applying
// the necessary conversions for the API version of the request.
// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
func mergeToInternalCluster(csCluster *arohcpv1alpha1.Cluster, internalCluster *api.HCPOpenShiftCluster) (*api.HCPOpenShiftCluster, error) {
	clusterServiceBasedInternalCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(internalCluster.ID, csCluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// this does not use conversion.CopyReadOnly* because some ServiceProvider properties come from cluster-service-only or live reads
	clusterServiceBasedInternalCluster.SystemData = internalCluster.SystemData
	clusterServiceBasedInternalCluster.Tags = maps.Clone(internalCluster.Tags)
	clusterServiceBasedInternalCluster.ServiceProviderProperties.ProvisioningState = internalCluster.ServiceProviderProperties.ProvisioningState
	clusterServiceBasedInternalCluster.ServiceProviderProperties.ActiveOperationID = internalCluster.ServiceProviderProperties.ActiveOperationID
	clusterServiceBasedInternalCluster.ServiceProviderProperties.ClusterServiceID = internalCluster.ServiceProviderProperties.ClusterServiceID
	clusterServiceBasedInternalCluster.ServiceProviderProperties.CosmosUID = internalCluster.ServiceProviderProperties.CosmosUID
	if clusterServiceBasedInternalCluster.Identity == nil {
		clusterServiceBasedInternalCluster.Identity = &arm.ManagedServiceIdentity{}
	}

	if internalCluster.Identity != nil {
		clusterServiceBasedInternalCluster.Identity.PrincipalID = internalCluster.Identity.PrincipalID
		clusterServiceBasedInternalCluster.Identity.TenantID = internalCluster.Identity.TenantID
		clusterServiceBasedInternalCluster.Identity.Type = internalCluster.Identity.Type
	}

	return clusterServiceBasedInternalCluster, nil
}

// readInternalClusterFromClusterService takes an internal Cluster read from cosmos, retrieves the corresponding cluster-service data,
// merges the states together, and returns the internal representation.
// TODO remove the header it takes and collapse that to some general error handling.
func (f *Frontend) readInternalClusterFromClusterService(ctx context.Context, oldInternalCluster *api.HCPOpenShiftCluster) (*api.HCPOpenShiftCluster, error) {
	oldClusterServiceCluster, err := f.clusterServiceClient.GetCluster(ctx, oldInternalCluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	oldInternalCluster, err = mergeToInternalCluster(oldClusterServiceCluster, oldInternalCluster)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return oldInternalCluster, nil
}

func (f *Frontend) getInternalClusterFromStorage(ctx context.Context, resourceID *azcorearm.ResourceID) (*api.HCPOpenShiftCluster, error) {
	internalCluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil, arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// Replace the key field from Cosmos with the given resourceID,
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
	if !strings.EqualFold(internalCluster.ID.String(), resourceID.String()) {
		return nil, fmt.Errorf("unexpected resourceID: %s", internalCluster.ID.String())
	}
	internalCluster.ID = resourceID

	return f.readInternalClusterFromClusterService(ctx, internalCluster)
}
