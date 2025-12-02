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

	"k8s.io/apimachinery/pkg/util/validation/field"

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

	internalObj, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name).Get(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		return arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		return err
	}

	clusterServiceObj, err := f.clusterServiceClient.GetExternalAuth(ctx, internalObj.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return ocm.CSErrorToCloudError(err, resourceID, nil)
	}

	responseBody, err := mergeToExternalExternalAuth(clusterServiceObj, internalObj, versionedInterface)
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBody)
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
	internalExternalAuthIteraotr, err := f.dbClient.HCPClusters(subscriptionID, resourceGroupName).ExternalAuth(resourceName).List(ctx, dbListOptionsFromRequest(request))
	if err != nil {
		return err
	}
	for _, externalAuth := range internalExternalAuthIteraotr.Items(ctx) {
		externalAuthsByClusterServiceID[externalAuth.ServiceProviderProperties.ClusterServiceID.ID()] = externalAuth
	}
	err = internalExternalAuthIteraotr.GetError()
	if err != nil {
		return err
	}

	// MiddlewareReferer ensures Referer is present.
	err = pagedResponse.SetNextLink(request.Referer(), internalExternalAuthIteraotr.GetContinuationToken())
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
			value, err := mergeToExternalExternalAuth(csExternalAuth, internalExternalAuth, versionedInterface)
			if err != nil {
				return err
			}
			pagedResponse.AddValue(value)
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

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	externalAuthCosmosClient := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).ExternalAuth(resourceID.Parent.Name)

	internalOldAuth, err := externalAuthCosmosClient.Get(ctx, resourceID.Name)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		return err
	}

	var updating = (internalOldAuth != nil)
	var operationRequest database.OperationRequest

	var externalOldAuth api.VersionedHCPOpenShiftClusterExternalAuth
	var externalNewAuth api.VersionedHCPOpenShiftClusterExternalAuth
	var successStatusCode int

	if updating {
		{ // scope to ensure this temporary values don't escape
			oldClusterServiceAuth, err := f.clusterServiceClient.GetExternalAuth(ctx, internalOldAuth.ServiceProviderProperties.ClusterServiceID)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to fetch CS external auth for %s: %v", resourceID, err))
				return ocm.CSErrorToCloudError(err, resourceID, writer.Header())
			}

			mergedOldAuth, err := ocm.ConvertCStoExternalAuth(resourceID, oldClusterServiceAuth)
			if err != nil {
				return err
			}
			mergedOldAuth.SystemData = internalOldAuth.SystemData
			mergedOldAuth.Properties.ProvisioningState = internalOldAuth.Properties.ProvisioningState
			mergedOldAuth.ServiceProviderProperties.CosmosUID = internalOldAuth.ServiceProviderProperties.CosmosUID
			mergedOldAuth.ServiceProviderProperties.ClusterServiceID = internalOldAuth.ServiceProviderProperties.ClusterServiceID

			// internalOldAuth gets overwritten (for now), by the content from cluster-service which is authoritative for now.
			internalOldAuth = mergedOldAuth
		}

		operationRequest = database.OperationRequestUpdate

		// This is slightly repetitive for the sake of clarify on PUT vs PATCH.
		switch request.Method {
		case http.MethodPut:
			// Initialize versionedRequestExternalAuth to include both
			// non-zero default values and current read-only values.

			externalOldAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(internalOldAuth)
			externalNewAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(nil)

			// read-only values are an internal concern since they're the source, so we convert.
			// this could be faster done purely externally, but this allows a single set of rules for copying read only fields.
			newTemporaryInternal := &api.HCPOpenShiftClusterExternalAuth{}
			externalNewAuth.Normalize(newTemporaryInternal)
			conversion.CopyReadOnlyExternalAuthValues(newTemporaryInternal, internalOldAuth)
			externalNewAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(newTemporaryInternal)

			successStatusCode = http.StatusOK
		case http.MethodPatch:
			externalOldAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(internalOldAuth)
			externalNewAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(internalOldAuth)
			successStatusCode = http.StatusAccepted
		default:
			return fmt.Errorf("unsupported method %s", request.Method)
		}

		// CheckForProvisioningStateConflict does not log conflict errors
		// but does log unexpected errors like database failures.
		if err := checkForProvisioningStateConflict(ctx, f.dbClient, operationRequest, internalOldAuth.ID, internalOldAuth.Properties.ProvisioningState); err != nil {
			return err
		}

	} else {
		operationRequest = database.OperationRequestCreate

		switch request.Method {
		case http.MethodPut:
			// Initialize top-level resource fields from the request path.
			// If the request body specifies these fields, validation should
			// accept them as long as they match (case-insensitively) values
			// from the request path.
			defaultInternalAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)

			externalOldAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(defaultInternalAuth)
			externalNewAuth = versionedInterface.NewHCPOpenShiftClusterExternalAuth(defaultInternalAuth)
			successStatusCode = http.StatusCreated
		case http.MethodPatch:
			return arm.NewResourceNotFoundError(resourceID)
		default:
			return fmt.Errorf("unsupported method %s", request.Method)
		}
	}

	if err := api.ApplyRequestBody(request.Method, body, externalNewAuth); err != nil {
		return err
	}

	internalNewAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)
	externalNewAuth.Normalize(internalNewAuth)

	var validationErrs field.ErrorList
	if updating {
		oldInternalAuth := api.NewDefaultHCPOpenShiftClusterExternalAuth(resourceID)
		externalOldAuth.Normalize(oldInternalAuth)
		validationErrs = validation.ValidateExternalAuthUpdate(ctx, internalNewAuth, oldInternalAuth)

	} else {
		validationErrs = validation.ValidateExternalAuthCreate(ctx, internalNewAuth)

	}
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return err
	}

	csExternalAuthBuilder, err := ocm.BuildCSExternalAuth(ctx, internalNewAuth, updating)
	if err != nil {
		return err
	}

	var csExternalAuth *arohcpv1alpha1.ExternalAuth

	if updating {
		logger.Info(fmt.Sprintf("updating resource %s", resourceID))
		csExternalAuth, err = f.clusterServiceClient.UpdateExternalAuth(ctx, internalNewAuth.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
		if err != nil {
			return ocm.CSErrorToCloudError(err, resourceID, writer.Header())
		}
	} else {
		logger.Info(fmt.Sprintf("creating resource %s", resourceID))
		cluster, err := f.dbClient.HCPClusters(resourceID.SubscriptionID, resourceID.ResourceGroupName).Get(ctx, resourceID.Parent.Name)
		if err != nil {
			return err
		}

		csExternalAuth, err = f.clusterServiceClient.PostExternalAuth(ctx, cluster.ServiceProviderProperties.ClusterServiceID, csExternalAuthBuilder)
		if err != nil {
			return err
		}

		internalNewAuth.ServiceProviderProperties.ClusterServiceID, err = api.NewInternalID(csExternalAuth.HREF())
		if err != nil {
			return err
		}
	}

	transaction := f.dbClient.NewTransaction(pk)

	operationDoc := database.NewOperationDocument(operationRequest, internalNewAuth.ID, internalNewAuth.ServiceProviderProperties.ClusterServiceID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	cosmosUID := ""
	if !updating {
		cosmosUID, err = externalAuthCosmosClient.AddCreateToTransaction(ctx, transaction, internalNewAuth, nil)
		if err != nil {
			return err
		}
	} else {
		cosmosUID = internalOldAuth.ServiceProviderProperties.CosmosUID
	}

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// Record the latest system data values from ARM, if present.
	if systemData != nil {
		patchOperations.SetSystemData(systemData)
	}

	transaction.PatchResourceDoc(cosmosUID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		return err
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosObj, err := transactionResult.GetResourceDoc(cosmosUID)
	if err != nil {
		return err
	}
	internalObj, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftClusterExternalAuth, database.ExternalAuth](resultingCosmosObj)
	if err != nil {
		return err
	}

	responseBody, err := mergeToExternalExternalAuth(csExternalAuth, internalObj, versionedInterface)
	if err != nil {
		return err
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBody)
	if err != nil {
		return err
	}
	return nil
}

// the necessary conversions for the API version of the request.
func mergeToExternalExternalAuth(csEternalAuth *arohcpv1alpha1.ExternalAuth, internalObj *api.HCPOpenShiftClusterExternalAuth, versionedInterface api.Version) ([]byte, error) {
	hcpExternalAuth, err := ocm.ConvertCStoExternalAuth(internalObj.ID, csEternalAuth)
	if err != nil {
		return nil, err
	}

	hcpExternalAuth.SystemData = internalObj.SystemData
	hcpExternalAuth.Properties.ProvisioningState = internalObj.Properties.ProvisioningState

	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftClusterExternalAuth(hcpExternalAuth))
}
