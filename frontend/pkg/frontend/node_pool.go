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
	"maps"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"

	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

func (f *Frontend) GetNodePool(writer http.ResponseWriter, request *http.Request) {
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

	internalObj, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	}
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	clusterServiceObj, err := f.clusterServiceClient.GetNodePool(ctx, internalObj.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, nil))
		return
	}

	responseBody, err := mergeToExternalNodePool(clusterServiceObj, internalObj, versionedInterface)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBody)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) ArmResourceListNodePools(writer http.ResponseWriter, request *http.Request) {
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
	resourceName := request.PathValue(PathSegmentResourceName)

	internalCluster, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).Get(ctx, resourceName)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pagedResponse := arm.NewPagedResponse()

	nodePoolsByClusterServiceID := make(map[string]*api.HCPOpenShiftClusterNodePool)
	internalNodePoolIterator, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).NodePools(resourceName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	for _, nodePool := range internalNodePoolIterator.Items(ctx) {
		nodePoolsByClusterServiceID[nodePool.ServiceProviderProperties.ClusterServiceID.String()] = nodePool
	}
	err = internalNodePoolIterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalNodePoolIterator.GetContinuationToken())
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
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
		if internalNodePool, ok := nodePoolsByClusterServiceID[csNodePool.HREF()]; ok {
			value, err := mergeToExternalNodePool(csNodePool, internalNodePool, versionedInterface)
			if err != nil {
				logger.Error(err.Error())
				arm.WriteInternalServerError(writer)
				return
			}
			pagedResponse.AddValue(value)
		}
	}
	err = csIterator.GetError()
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

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	nodePoolCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).NodePools(resourceID.Parent.Name)
	internalOldNodePool, err := nodePoolCosmosClient.Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var updating = (internalOldNodePool != nil)
	var operationRequest database.OperationRequest

	var externalOldNodePool api.VersionedHCPOpenShiftClusterNodePool
	var externalNewNodePool api.VersionedHCPOpenShiftClusterNodePool
	var successStatusCode int

	if updating {
		{ // scope to ensure temporary variables don't escape
			csNodePool, err := f.clusterServiceClient.GetNodePool(ctx, internalOldNodePool.ServiceProviderProperties.ClusterServiceID)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to fetch CS node pool for %s: %v", resourceID, err))
				arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
				return
			}

			mergedOldNodePool := ocm.ConvertCStoNodePool(resourceID, csNodePool)

			// Do not set the TrackedResource.Tags field here. We need
			// the Tags map to remain nil so we can see if the request
			// body included a new set of resource tags.

			mergedOldNodePool.SystemData = internalOldNodePool.SystemData
			mergedOldNodePool.Properties.ProvisioningState = internalOldNodePool.Properties.ProvisioningState
			mergedOldNodePool.ServiceProviderProperties.CosmosUID = internalOldNodePool.ServiceProviderProperties.CosmosUID
			mergedOldNodePool.ServiceProviderProperties.ClusterServiceID = internalOldNodePool.ServiceProviderProperties.ClusterServiceID

			// internalOldNodePool gets overwritten (for now), by the content from cluster-service which is authoritative for now.
			internalOldNodePool = mergedOldNodePool
		}

		operationRequest = database.OperationRequestUpdate

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			// Initialize versionedRequestNodePool to include both
			// non-zero default values and current read-only values.
			reqNodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID)

			// Some optional create-only fields have dynamic default
			// values that are determined downstream of this phase of
			// request processing. To ensure idempotency, add these
			// values to the target struct for the incoming request.
			reqNodePool.Properties.Version.ID = internalOldNodePool.Properties.Version.ID
			reqNodePool.Properties.Platform.SubnetID = internalOldNodePool.Properties.Platform.SubnetID

			externalOldNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(internalOldNodePool)
			externalNewNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(reqNodePool)

			// read-only values are an internal concern since they're the source, so we convert.
			// this could be faster done purely externally, but this allows a single set of rules for copying read only fields.
			newTemporaryInternal := &api.HCPOpenShiftClusterNodePool{}
			externalNewNodePool.Normalize(newTemporaryInternal)
			conversion.CopyReadOnlyNodePoolValues(newTemporaryInternal, internalOldNodePool)
			externalNewNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(newTemporaryInternal)

			successStatusCode = http.StatusOK
		case http.MethodPatch:
			externalOldNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(internalOldNodePool)
			externalNewNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(internalOldNodePool)
			successStatusCode = http.StatusAccepted
		}

		// CheckForProvisioningStateConflict does not log conflict errors
		// but does log unexpected errors like database failures.
		cloudError := checkForProvisioningStateConflict(ctx, f.dbClient, operationRequest, internalOldNodePool.ID, internalOldNodePool.Properties.ProvisioningState)
		if cloudError != nil {
			arm.WriteCloudError(writer, cloudError)
			return
		}

	} else {
		operationRequest = database.OperationRequestCreate

		switch request.Method {
		case http.MethodPut:
			// Initialize top-level resource fields from the request path.
			// If the request body specifies these fields, validation should
			// accept them as long as they match (case-insensitively) values
			// from the request path.
			hcpNodePool := api.NewDefaultHCPOpenShiftClusterNodePool(resourceID)

			externalOldNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			externalNewNodePool = versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool)
			successStatusCode = http.StatusCreated
		case http.MethodPatch:
			// PATCH requests never create a new resource.
			logger.Error("Resource not found")
			arm.WriteResourceNotFoundError(writer, resourceID)
			return
		}
	}

	cloudError := api.ApplyRequestBody(request, body, externalNewNodePool)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	// Node pool validation checks some fields against the parent cluster
	// so we have to request the cluster from Cluster Service.

	cluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Parent.Name)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID.Parent)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	csCluster, err := f.clusterServiceClient.GetCluster(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID.Parent, writer.Header()))
		return
	}

	hcpCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID.Parent, csCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	newInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
	externalNewNodePool.Normalize(newInternalNodePool)

	var validationErrs field.ErrorList
	if updating {
		oldInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
		externalOldNodePool.Normalize(oldInternalNodePool)
		validationErrs = validation.ValidateNodePoolUpdate(ctx, newInternalNodePool, oldInternalNodePool)
		// in addition to static validation, we have validation based on the state of the hcp cluster
		validationErrs = append(validationErrs, admission.AdmitNodePool(newInternalNodePool, hcpCluster)...)

	} else {
		validationErrs = validation.ValidateNodePoolCreate(ctx, newInternalNodePool)
		// in addition to static validation, we have validation based on the state of the hcp cluster
		validationErrs = append(validationErrs, admission.AdmitNodePool(newInternalNodePool, hcpCluster)...)

	}
	newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs)

	// prefer new validation.  Have a fallback for old validation.
	if newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	csNodePoolBuilder, err := ocm.BuildCSNodePool(ctx, newInternalNodePool, updating)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var csNodePool *arohcpv1alpha1.NodePool

	if updating {
		logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		csNodePool, err = f.clusterServiceClient.UpdateNodePool(ctx, internalOldNodePool.ServiceProviderProperties.ClusterServiceID, csNodePoolBuilder)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
			return
		}
	} else {
		logger.Info(fmt.Sprintf("creating resource %s", resourceID))
		cluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Parent.Name)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		csNodePool, err = f.clusterServiceClient.PostNodePool(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csNodePoolBuilder)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
			return
		}

		newInternalNodePool.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(csNodePool.HREF())
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}

	transaction := f.dbClient.NewTransaction(pk)

	operationDoc := database.NewOperationDocument(operationRequest, newInternalNodePool.ID, newInternalNodePool.ServiceProviderProperties.ClusterServiceID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	cosmosUID := ""
	if !updating {
		cosmosUID, err = nodePoolCosmosClient.AddCreateToTransaction(ctx, transaction, newInternalNodePool, nil)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		cosmosUID = internalOldNodePool.ServiceProviderProperties.CosmosUID
	}

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		patchOperations.SetSystemData(systemData)
	}

	// Here the difference between a nil map and an empty map is significant.
	// If the Tags map is nil, that means it was omitted from the request body,
	// so we leave any existing tags alone. If the Tags map is non-nil, even if
	// empty, that means it was specified in the request body and should fully
	// replace any existing tags.
	if newInternalNodePool.Tags != nil {
		patchOperations.SetTags(newInternalNodePool.Tags)
	}

	transaction.PatchResourceDoc(cosmosUID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosObj, err := transactionResult.GetResourceDoc(cosmosUID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	resultingInternalObj, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftClusterNodePool, database.NodePool](resultingCosmosObj)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	responseBody, err := mergeToExternalNodePool(csNodePool, resultingInternalObj, versionedInterface)
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
func mergeToExternalNodePool(csNodePool *arohcpv1alpha1.NodePool, internalNodePool *api.HCPOpenShiftClusterNodePool, versionedInterface api.Version) ([]byte, error) {
	hcpNodePool := ocm.ConvertCStoNodePool(internalNodePool.ID, csNodePool)
	hcpNodePool.SystemData = internalNodePool.SystemData
	hcpNodePool.Tags = maps.Clone(internalNodePool.Tags)
	hcpNodePool.Properties.ProvisioningState = internalNodePool.Properties.ProvisioningState

	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterNodePool(hcpNodePool))
}
