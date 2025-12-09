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

	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func (f *Frontend) GetNodePool(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	resultingInternalNodePool, err := f.getInternalNodePoolFromStorage(ctx, resourceID)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(resultingInternalNodePool))
	if err != nil {
		return utils.TrackError(err)
	}
	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}

	return nil
}

func (f *Frontend) ArmResourceListNodePools(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	clusterName := request.PathValue(PathSegmentResourceName)

	internalCluster, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, clusterName)
	if err != nil {
		return utils.TrackError(err)
	}

	pagedResponse := arm.NewPagedResponse()

	nodePoolsByClusterServiceID := make(map[string]*api.HCPOpenShiftClusterNodePool)
	internalNodePoolIterator, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(clusterName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		return utils.TrackError(err)
	}
	for _, nodePool := range internalNodePoolIterator.Items(ctx) {
		nodePoolsByClusterServiceID[nodePool.ServiceProviderProperties.ClusterServiceID.ID()] = nodePool
	}
	err = internalNodePoolIterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}

	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalNodePoolIterator.GetContinuationToken())
	if err != nil {
		return utils.TrackError(err)
	}

	// Build a Cluster Service query that looks for
	// the specific IDs returned by the Cosmos query.
	queryIDs := make([]string, 0, len(nodePoolsByClusterServiceID))
	for key := range nodePoolsByClusterServiceID {
		queryIDs = append(queryIDs, "'"+key+"'")
	}
	query := fmt.Sprintf("id in (%s)", strings.Join(queryIDs, ", "))
	logger.Info(fmt.Sprintf("Searching Cluster Service for %q", query))

	csIterator := f.clusterServiceClient.ListNodePools(internalCluster.ServiceProviderProperties.ClusterServiceID, query)
	for csNodePool := range csIterator.Items(ctx) {
		if internalNodePool, ok := nodePoolsByClusterServiceID[csNodePool.ID()]; ok {
			internalNodePool, err = mergeToInternalNodePool(csNodePool, internalNodePool)
			if err != nil {
				return utils.TrackError(err)
			}
			resultingExternalNodePool := versionedInterface.NewHCPOpenShiftClusterNodePool(internalNodePool)
			jsonBytes, err := arm.MarshalJSON(resultingExternalNodePool)
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

func (f *Frontend) CreateOrUpdateNodePool(writer http.ResponseWriter, request *http.Request) error {
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

	nodePoolCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name)
	oldInternalNodePool, err := nodePoolCosmosClient.Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return utils.TrackError(err)
	}

	updating := oldInternalNodePool != nil
	if updating {
		// re-write oldInternalCluster for as long as cluster-service needs to be consulted for pre-existing state.
		oldInternalNodePool, err = f.readInternalNodePoolFromClusterService(ctx, oldInternalNodePool)
		if err != nil {
			return utils.TrackError(err)
		}
		if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, oldInternalNodePool.ID, oldInternalNodePool.Properties.ProvisioningState); err != nil {
			return utils.TrackError(err)
		}

		switch request.Method {
		case http.MethodPut:
			return f.updateNodePool(writer, request, oldInternalNodePool)
		case http.MethodPatch:
			return f.patchNodePool(writer, request, oldInternalNodePool)
		default:
			return fmt.Errorf("unsupported method %s", request.Method)
		}
	}

	switch request.Method {
	case http.MethodPut:
		return f.createNodePool(writer, request)
	case http.MethodPatch:
		return arm.NewResourceNotFoundError(resourceID)
	default:
		return fmt.Errorf("unsupported method %s", request.Method)
	}
}

func decodeDesiredNodePoolCreate(ctx context.Context) (*api.HCPOpenShiftClusterNodePool, error) {
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

	externalNodePoolFromRequest := versionedInterface.NewHCPOpenShiftClusterNodePool(&api.HCPOpenShiftClusterNodePool{})
	if err := json.Unmarshal(body, &externalNodePoolFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}
	if err := externalNodePoolFromRequest.SetDefaultValues(externalNodePoolFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
	externalNodePoolFromRequest.Normalize(newInternalNodePool)
	// TrackedResource info doesn't appear to come from the external resource information
	conversion.CopyReadOnlyTrackedResourceValues(&newInternalNodePool.TrackedResource, ptr.To(arm.NewTrackedResource(resourceID)))

	// set fields that were not included during the conversion, because the user does not provide them or because the
	// data is determined live on read.
	newInternalNodePool.SystemData = systemData

	return newInternalNodePool, nil
}

func (f *Frontend) createNodePool(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	newInternalNodePool, err := decodeDesiredNodePoolCreate(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Node pool validation checks some fields against the parent cluster
	// so we have to request the cluster from Cluster Service.
	cluster, err := f.getInternalClusterFromStorage(ctx, resourceID.Parent)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateNodePoolCreate(ctx, newInternalNodePool)
	// in addition to static validation, we have validation based on the state of the hcp cluster
	validationErrs = append(validationErrs, admission.AdmitNodePool(newInternalNodePool, cluster)...)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	if err := checkForProvisioningStateConflict(ctx, f.dbClient, database.OperationRequestUpdate, cluster.ID, cluster.ServiceProviderProperties.ProvisioningState); err != nil {
		return utils.TrackError(err)
	}
	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, newInternalNodePool, false)
	if err != nil {
		return utils.TrackError(err)
	}
	csNodePool, err := f.clusterServiceClient.PostNodePool(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}
	newInternalNodePool.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(csNodePool.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	pk := database.NewPartitionKey(newInternalNodePool.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	createNodePoolOperation := database.NewOperationDocument(database.OperationRequestCreate, newInternalNodePool.ID, newInternalNodePool.ServiceProviderProperties.ClusterServiceID, correlationData)
	createNodePoolOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	createNodePoolOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	createNodePoolOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(createNodePoolOperation, nil)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, createNodePoolOperation.NotificationURI, createNodePoolOperation.OperationID))

	// set fields that were not known until the operation doc instance was created.
	// TODO once we we have separate creation/validation of operation documents, this can be done ahead of time.
	newInternalNodePool.ServiceProviderProperties.ActiveOperationID = operationCosmosUID
	newInternalNodePool.Properties.ProvisioningState = createNodePoolOperation.Status

	nodePoolCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name)
	cosmosUID, err := nodePoolCosmosClient.AddCreateToTransaction(ctx, transaction, newInternalNodePool, nil)
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
	resultingUncastInternalNodePool, err := transactionResult.GetItem(cosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalNodePool, ok := resultingUncastInternalNodePool.(*api.HCPOpenShiftClusterNodePool)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalNodePool)
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalNodePool, err = mergeToInternalNodePool(csNodePool, resultingInternalNodePool)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(resultingInternalNodePool))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusCreated, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func decodeDesiredNodePoolReplace(ctx context.Context, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) (*api.HCPOpenShiftClusterNodePool, error) {
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

	// Initialize versionedRequestNodePool to include both
	// non-zero default values and current read-only values.
	// Exact user request
	externalNodePoolFromRequest := versionedInterface.NewHCPOpenShiftClusterNodePool(&api.HCPOpenShiftClusterNodePool{})
	if err := json.Unmarshal(body, &externalNodePoolFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	// Default values
	if err := externalNodePoolFromRequest.SetDefaultValues(externalNodePoolFromRequest); err != nil {
		return nil, utils.TrackError(err)
	}

	newInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
	externalNodePoolFromRequest.Normalize(newInternalNodePool)

	// values a user doesn't have to provide, but are not static defaults (set dynamically during create).  Set these from old value
	if len(newInternalNodePool.Properties.Version.ID) == 0 {
		newInternalNodePool.Properties.Version.ID = oldInternalNodePool.Properties.Version.ID
	}
	if len(newInternalNodePool.Properties.Platform.SubnetID) == 0 {
		newInternalNodePool.Properties.Platform.SubnetID = oldInternalNodePool.Properties.Platform.SubnetID
	}

	// ServiceProviderProperties contains two types of information
	// 1. values that a user cannot change because the external type does not expose the information.
	//    We must overwrite those values with the oldInternalCluster values so the values don't change, because the user's input will always be empty.
	// 2. values that a user cannot change due to validation requirements, but the user *can* specify the values.
	//    We are overwriting these values that we consider to be status values.
	//    We do this because if a user has read a value, then modified it, then replaces it, we don't want to produce
	//    validation errors on status fields that the user isn't trying to modify.
	conversion.CopyReadOnlyNodePoolValues(newInternalNodePool, oldInternalNodePool)
	newInternalNodePool.SystemData = systemData

	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	return newInternalNodePool, nil
}

func (f *Frontend) updateNodePool(writer http.ResponseWriter, request *http.Request, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) error {
	ctx := request.Context()

	newInternalNodePool, err := decodeDesiredNodePoolReplace(ctx, oldInternalNodePool)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateNodePoolInCosmos(ctx, writer, request, http.StatusOK, newInternalNodePool, oldInternalNodePool)
}

func decodeDesiredNodePoolPatch(ctx context.Context, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) (*api.HCPOpenShiftClusterNodePool, error) {
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
	newExternalNodePool := versionedInterface.NewHCPOpenShiftClusterNodePool(oldInternalNodePool)
	if err := api.ApplyRequestBody(http.MethodPatch, body, newExternalNodePool); err != nil {
		return nil, utils.TrackError(err)
	}
	newInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
	newExternalNodePool.Normalize(newInternalNodePool)

	conversion.CopyReadOnlyNodePoolValues(newInternalNodePool, oldInternalNodePool)
	newInternalNodePool.SystemData = systemData

	// Clear the user-assigned identities map since that is reconstructed from Cluster Service data.
	// TODO we'd like to have the instance complete when we go to validate it.  Right now validation fails if we clear this.
	// TODO we probably update validation to require this field is cleared.
	//newInternalCluster.Identity.UserAssignedIdentities = nil

	return newInternalNodePool, nil
}

func (f *Frontend) patchNodePool(writer http.ResponseWriter, request *http.Request, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) error {
	// PATCH requests overlay the request body onto a resource struct
	// that represents an existing resource to be updated.
	ctx := request.Context()

	newInternalNodePool, err := decodeDesiredNodePoolPatch(ctx, oldInternalNodePool)
	if err != nil {
		return utils.TrackError(err)
	}

	return f.updateNodePoolInCosmos(ctx, writer, request, http.StatusAccepted, newInternalNodePool, oldInternalNodePool)
}

func (f *Frontend) updateNodePoolInCosmos(ctx context.Context, writer http.ResponseWriter, request *http.Request, httpStatusCode int, newInternalNodePool, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) error {
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Node pool validation checks some fields against the parent cluster
	// so we have to request the cluster from Cluster Service.
	cluster, err := f.getInternalClusterFromStorage(ctx, oldInternalNodePool.ID.Parent)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateNodePoolUpdate(ctx, newInternalNodePool, oldInternalNodePool)
	// in addition to static validation, we have validation based on the state of the hcp cluster
	validationErrs = append(validationErrs, admission.AdmitNodePool(newInternalNodePool, cluster)...)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, newInternalNodePool, true)
	if err != nil {
		return utils.TrackError(err)
	}

	logger.Info(fmt.Sprintf("updating resource %s", oldInternalNodePool.ID))
	csNodePool, err := f.clusterServiceClient.UpdateNodePool(ctx, oldInternalNodePool.ServiceProviderProperties.ClusterServiceID, csNodePoolBuilder)
	if err != nil {
		return utils.TrackError(err)
	}

	pk := database.NewPartitionKey(oldInternalNodePool.ID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	nodePoolUpdateOperation := database.NewOperationDocument(database.OperationRequestUpdate, newInternalNodePool.ID, newInternalNodePool.ServiceProviderProperties.ClusterServiceID, correlationData)
	nodePoolUpdateOperation.TenantID = request.Header.Get(arm.HeaderNameHomeTenantID)
	nodePoolUpdateOperation.ClientID = request.Header.Get(arm.HeaderNameClientObjectID)
	nodePoolUpdateOperation.NotificationURI = request.Header.Get(arm.HeaderNameAsyncNotificationURI)
	operationCosmosUID := transaction.CreateOperationDoc(nodePoolUpdateOperation, nil)

	f.ExposeOperation(writer, request, operationCosmosUID, transaction)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationCosmosUID)
	patchOperations.SetProvisioningState(nodePoolUpdateOperation.Status)
	patchOperations.SetSystemData(newInternalNodePool.SystemData)

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if newInternalNodePool.Tags != nil {
		patchOperations.SetTags(newInternalNodePool.Tags)
	}

	transaction.PatchResourceDoc(oldInternalNodePool.ServiceProviderProperties.CosmosUID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	// Read back the resource document so the response body is accurate.
	resultingUncastInternalNodePool, err := transactionResult.GetItem(oldInternalNodePool.ServiceProviderProperties.CosmosUID)
	if err != nil {
		return utils.TrackError(err)
	}
	resultingInternalNodePool, ok := resultingUncastInternalNodePool.(*api.HCPOpenShiftClusterNodePool)
	if !ok {
		return fmt.Errorf("unexpected type %T", resultingUncastInternalNodePool)
	}
	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	resultingInternalNodePool, err = mergeToInternalNodePool(csNodePool, resultingInternalNodePool)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBytes, err := arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(resultingInternalNodePool))
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, httpStatusCode, responseBytes)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// the necessary conversions for the API version of the request.
func mergeToInternalNodePool(clusterServiceNode *arohcpv1alpha1.NodePool, internalNodePool *api.HCPOpenShiftClusterNodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	mergedOldClusterServiceNodePool := ocm.ConvertCStoNodePool(internalNodePool.ID, clusterServiceNode)

	// this does not use conversion.CopyReadOnly* because some ServiceProvider properties come from cluster-service-only or live reads
	mergedOldClusterServiceNodePool.SystemData = internalNodePool.SystemData
	mergedOldClusterServiceNodePool.Tags = maps.Clone(internalNodePool.Tags)
	mergedOldClusterServiceNodePool.Properties.ProvisioningState = internalNodePool.Properties.ProvisioningState
	mergedOldClusterServiceNodePool.ServiceProviderProperties.CosmosUID = internalNodePool.ServiceProviderProperties.CosmosUID
	mergedOldClusterServiceNodePool.ServiceProviderProperties.ClusterServiceID = internalNodePool.ServiceProviderProperties.ClusterServiceID
	mergedOldClusterServiceNodePool.ServiceProviderProperties.ActiveOperationID = internalNodePool.ServiceProviderProperties.ActiveOperationID

	return mergedOldClusterServiceNodePool, nil
}

func (f *Frontend) getInternalNodePoolFromStorage(ctx context.Context, resourceID *azcorearm.ResourceID) (*api.HCPOpenShiftClusterNodePool, error) {
	internalNodePool, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		return nil, arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return f.readInternalNodePoolFromClusterService(ctx, internalNodePool)

}

// readInternalNodePoolFromClusterService takes an internal NodePool read from cosmos, retrieves the corresponding cluster-service data,
// merges the states together, and returns the internal representation.
func (f *Frontend) readInternalNodePoolFromClusterService(ctx context.Context, oldInternalNodePool *api.HCPOpenShiftClusterNodePool) (*api.HCPOpenShiftClusterNodePool, error) {
	oldClusterServiceNodePool, err := f.clusterServiceClient.GetNodePool(ctx, oldInternalNodePool.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	// TODO this overwrite will transformed into a "set" function as we transition fields to ownership in cosmos
	oldInternalNodePool, err = mergeToInternalNodePool(oldClusterServiceNodePool, oldInternalNodePool)
	if err != nil {
		return nil, utils.TrackError(err)
	}

	return oldInternalNodePool, nil
}
