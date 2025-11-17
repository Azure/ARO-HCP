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
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"golang.org/x/sync/errgroup"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/frontend/pkg/metrics"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/conversion"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/validation"
)

type Frontend struct {
	clusterServiceClient ocm.ClusterServiceClientSpec
	listener             net.Listener
	metricsListener      net.Listener
	server               http.Server
	metricsServer        http.Server
	dbClient             database.DBClient
	auditClient          audit.Client
	done                 chan struct{}
	collector            *metrics.SubscriptionCollector
	healthGauge          prometheus.Gauge

	apiRegistry api.APIRegistry
}

func NewFrontend(
	logger *slog.Logger,
	listener net.Listener,
	metricsListener net.Listener,
	reg prometheus.Registerer,
	dbClient database.DBClient,
	csClient ocm.ClusterServiceClientSpec,
	auditClient audit.Client,
) *Frontend {
	// zero side-effect registration path
	apiRegistry := api.NewAPIRegistry()
	api.Must[any](nil, v20240610preview.RegisterVersion(apiRegistry))
	api.Must[any](nil, v20251223preview.RegisterVersion(apiRegistry))

	f := &Frontend{
		clusterServiceClient: csClient,
		listener:             listener,
		metricsListener:      metricsListener,
		server: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				ctx := context.Background()
				ctx = ContextWithLogger(ctx, logger)
				return ctx
			},
		},
		metricsServer: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), logger)
			},
		},
		auditClient: auditClient,
		dbClient:    dbClient,
		done:        make(chan struct{}),
		collector:   metrics.NewSubscriptionCollector(reg, dbClient, arm.GetAzureLocation()),
		healthGauge: promauto.With(reg).NewGauge(
			prometheus.GaugeOpts{
				Name: healthGaugeName,
				Help: "Reports the health status of the service (0: not healthy, 1: healthy).",
			},
		),
		apiRegistry: apiRegistry,
	}

	f.server.Handler = f.routes(reg)
	f.metricsServer.Handler = f.metricsRoutes()

	return f
}

func (f *Frontend) Run(ctx context.Context, stop <-chan struct{}) {
	// This just digs up the logger passed to NewFrontend.
	logger := LoggerFromContext(f.server.BaseContext(f.listener))

	if stop != nil {
		go func() {
			<-stop
			_ = f.server.Shutdown(ctx)
			_ = f.metricsServer.Shutdown(ctx)
			close(f.done)
		}()
	}

	logger.Info(fmt.Sprintf("listening on %s", f.listener.Addr().String()))
	logger.Info(fmt.Sprintf("metrics listening on %s", f.metricsListener.Addr().String()))

	errs, ctx := errgroup.WithContext(ctx)
	errs.Go(func() error {
		return f.server.Serve(f.listener)
	})
	errs.Go(func() error {
		return f.metricsServer.Serve(f.metricsListener)
	})
	errs.Go(func() error {
		f.collector.Run(logger, stop)
		return nil
	})

	if err := errs.Wait(); !errors.Is(err, http.ErrServerClosed) {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func (f *Frontend) Join() {
	<-f.done
}

func (f *Frontend) NotFound(writer http.ResponseWriter, request *http.Request) {
	arm.WriteError(
		writer, http.StatusNotFound,
		arm.CloudErrorCodeNotFound, "",
		"The requested path could not be found.")
}

func (f *Frontend) Healthz(writer http.ResponseWriter, request *http.Request) {
	writer.WriteHeader(http.StatusOK)
	f.healthGauge.Set(1.0)
}

func (f *Frontend) Location(writer http.ResponseWriter, request *http.Request) {
	// This is strictly for development environments to help discover
	// the frontend's Azure region when port forwarding with kubectl.
	// e.g. LOCATION=$(curl http://localhost:8443/location)
	_, _ = writer.Write([]byte(arm.GetAzureLocation()))
}

func (f *Frontend) ArmResourceList(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	options := database.DBClientListResourceDocsOptions{
		PageSizeHint: api.Ptr(int32(20)),
	}

	// The Resource Provider Contract implies $top is only honored when
	// following a "nextLink" after the initial collection GET request.
	// So only check for it when the URL includes a $skipToken.
	urlQuery := request.URL.Query()
	if urlQuery.Has("$skipToken") {
		options.ContinuationToken = api.Ptr(urlQuery.Get("$skipToken"))
		top, err := strconv.ParseInt(urlQuery.Get("$top"), 10, 32)
		if err == nil && top > 0 {
			options.PageSizeHint = api.Ptr(int32(top))
		}
	}

	// FIXME We may want to cap pageSizeHint. If we get a large enough
	//       $top argument (and there's enough actual clusters to reach
	//       that), we could potentially hit the 8MB response size limit.

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	resourceGroupName := request.PathValue(PathSegmentResourceGroupName)
	resourceName := request.PathValue(PathSegmentResourceName)
	resourceTypeName := path.Base(request.URL.Path)
	location := request.PathValue(PathSegmentLocation)

	var resourceTypeUsesDatabase bool
	var clusterInternalID ocm.InternalID
	var query string

	// Map of resource documents by Cluster Service item ID.
	documentMap := make(map[string]*database.ResourceDocument)

	pagedResponse := arm.NewPagedResponse()

	switch resourceTypeName {
	case strings.ToLower(api.ClusterResourceTypeName):
		options.ResourceType = &api.ClusterResourceType
		resourceTypeUsesDatabase = true
	case strings.ToLower(api.NodePoolResourceTypeName):
		options.ResourceType = &api.NodePoolResourceType
		resourceTypeUsesDatabase = true
	case strings.ToLower(api.ExternalAuthResourceTypeName):
		options.ResourceType = &api.ExternalAuthResourceType
		resourceTypeUsesDatabase = true
	}

	if resourceTypeUsesDatabase {
		var needClusterInternalID bool

		// Even though the bulk of the list content comes from Cluster Service,
		// we start by querying Cosmos DB because its continuation token meets
		// the requirements of a skipToken for ARM pagination. We then query
		// Cluster Service for the exact set of IDs returned by Cosmos.

		prefixParts := []string{"/subscriptions", subscriptionID}
		if resourceGroupName != "" {
			prefixParts = append(prefixParts, "resourceGroups", resourceGroupName)
		}
		if resourceName != "" {
			// This is a nested resource request. Build a resource ID for
			// the parent cluster. We use this below to get the cluster's
			// ResourceDocument from Cosmos DB.
			prefixParts = append(prefixParts, "providers", api.ProviderNamespace, api.ClusterResourceTypeName, resourceName)
			needClusterInternalID = true
		}
		prefix, err := azcorearm.ParseResourceID(path.Join(prefixParts...))
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		dbIterator := f.dbClient.ListResourceDocs(prefix, &options)

		for _, resourceDoc := range dbIterator.Items(ctx) {
			documentMap[resourceDoc.InternalID.ID()] = resourceDoc
		}

		err = dbIterator.GetError()
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		// MiddlewareReferer ensures Referer is present.
		err = pagedResponse.SetNextLink(request.Referer(), dbIterator.GetContinuationToken())
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		// Build a Cluster Service query that looks for
		// the specific IDs returned by the Cosmos query.
		queryIDs := make([]string, 0, len(documentMap))
		for key := range documentMap {
			queryIDs = append(queryIDs, "'"+key+"'")
		}
		query = fmt.Sprintf("id in (%s)", strings.Join(queryIDs, ", "))
		logger.Info(fmt.Sprintf("Searching Cluster Service for %q", query))

		if needClusterInternalID {
			_, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, prefix)
			if err != nil {
				logger.Error(err.Error())
				if database.IsResponseError(err, http.StatusNotFound) {
					arm.WriteResourceNotFoundError(writer, prefix)
				} else {
					arm.WriteInternalServerError(writer)
				}
				return
			}
			clusterInternalID = resourceDoc.InternalID
		}
	}

	switch resourceTypeName {
	case strings.ToLower(api.ClusterResourceTypeName):
		csIterator := f.clusterServiceClient.ListClusters(query)

		for csCluster := range csIterator.Items(ctx) {
			if doc, ok := documentMap[csCluster.ID()]; ok {
				internalCluster, err := database.ResourceDocumentToInternalAPI[api.HCPOpenShiftCluster, database.HCPCluster](doc)
				if err != nil {
					logger.Error(err.Error())
					arm.WriteInternalServerError(writer)
					return
				}

				value, err := marshalCSCluster(csCluster, internalCluster, versionedInterface)
				if err != nil {
					logger.Error(err.Error())
					arm.WriteInternalServerError(writer)
					return
				}
				pagedResponse.AddValue(value)
			}
		}
		err = csIterator.GetError()

	case strings.ToLower(api.NodePoolResourceTypeName):
		csIterator := f.clusterServiceClient.ListNodePools(clusterInternalID, query)

		for csNodePool := range csIterator.Items(ctx) {
			if doc, ok := documentMap[csNodePool.ID()]; ok {
				value, err := marshalCSNodePool(csNodePool, doc, versionedInterface)
				if err != nil {
					logger.Error(err.Error())
					arm.WriteInternalServerError(writer)
					return
				}
				pagedResponse.AddValue(value)
			}
		}
		err = csIterator.GetError()

	case strings.ToLower(api.ExternalAuthResourceTypeName):
		csIterator := f.clusterServiceClient.ListExternalAuths(clusterInternalID, query)

		for csExternalAuth := range csIterator.Items(ctx) {
			if doc, ok := documentMap[csExternalAuth.ID()]; ok {
				value, err := marshalCSExternalAuth(csExternalAuth, doc, versionedInterface)
				if err != nil {
					logger.Error(err.Error())
					arm.WriteInternalServerError(writer)
					return
				}
				pagedResponse.AddValue(value)
			}
		}
		err = csIterator.GetError()

	case strings.ToLower(api.VersionResourceTypeName):
		csIterator := f.clusterServiceClient.ListVersions()

		for csVersion := range csIterator.Items(ctx) {
			versionName := strings.Replace(csVersion.ID(), api.OpenShiftVersionPrefix, "", 1)
			stringResource := "/subscriptions/" + subscriptionID + "/providers/" + api.ProviderNamespace +
				"/locations/" + location + "/" + api.VersionResourceTypeName + "/" + versionName
			resourceID, err := azcorearm.ParseResourceID(stringResource)
			if err != nil {
				logger.Error(err.Error())
				arm.WriteInternalServerError(writer)
				return
			}
			value, err := marshalCSVersion(*resourceID, csVersion, versionedInterface)
			if err != nil {
				logger.Error(err.Error())
				arm.WriteInternalServerError(writer)
				return
			}
			pagedResponse.AddValue(value)
		}
		err = csIterator.GetError()

	default:
		err = fmt.Errorf("unsupported resource type: %s", resourceTypeName)
	}

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

// ArmResourceRead implements the GET single resource API contract for ARM
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) ArmResourceRead(writer http.ResponseWriter, request *http.Request) {
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

	responseBody, cloudError := f.MarshalResource(ctx, resourceID, versionedInterface)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBody)
	if err != nil {
		logger.Error(err.Error())
	}
}

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

	responseBody, cloudError := f.MarshalCluster(ctx, resourceID, versionedInterface)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBody)
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

	resourceItemID, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil && !database.IsResponseError(err, http.StatusNotFound) {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	updating := resourceDoc != nil

	if updating {
		f.updateHCPCluster(writer, request, resourceItemID, resourceDoc)
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

	operationRequest := database.OperationRequestCreate

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
	newInternalCluster := &api.HCPOpenShiftCluster{}
	newExternalCluster.Normalize(newInternalCluster)
	validationErrs := validation.ValidateClusterCreate(ctx, newInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs)

	// prefer new validation.  Have a fallback for old validation.
	if newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	newClusterServiceClusterBuilder, newClusterServiceAutoscalerBuilder, err := ocm.BuildCSCluster(resourceID, request.Header, newInternalCluster, false)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	logger.Info(fmt.Sprintf("creating resource %s", resourceID))
	resultingClusterServiceCluster, err := f.clusterServiceClient.PostCluster(ctx, newClusterServiceClusterBuilder, newClusterServiceAutoscalerBuilder)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
		return
	}

	newCosmosCluster := database.NewResourceDocument(resourceID)
	newCosmosCluster.InternalID, err = ocm.NewInternalID(resultingClusterServiceCluster.HREF())
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	newInternalCluster.ServiceProviderProperties.ClusterServiceID = newCosmosCluster.InternalID.String()

	pk := database.NewPartitionKey(resourceID.SubscriptionID)
	transaction := f.dbClient.NewTransaction(pk)

	operationDoc := database.NewOperationDocument(operationRequest, newCosmosCluster.ResourceID, newCosmosCluster.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	resourceItemID := transaction.CreateResourceDoc(newCosmosCluster, database.FilterHCPClusterState, nil)

	var patchOperations database.ResourceDocumentPatchOperations

	patchOperations.SetActiveOperationID(&operationID)
	patchOperations.SetProvisioningState(operationDoc.Status)

	// TODO some of this becomes extraneous once we build the cosmosCluster from the internalCluster
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

	transaction.PatchResourceDoc(resourceItemID, patchOperations, nil)

	transactionResult, err := transaction.Execute(ctx, &azcosmos.TransactionalBatchOptions{
		EnableContentResponseOnWrite: true,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(resourceItemID)
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

	responseBody, err := marshalCSCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
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

func (f *Frontend) updateHCPCluster(writer http.ResponseWriter, request *http.Request, cosmosID string, oldCosmosCluster *database.ResourceDocument) {
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

	operationRequest := database.OperationRequestUpdate
	var oldExternalCluster api.VersionedHCPOpenShiftCluster
	var newExternalCluster api.VersionedHCPOpenShiftCluster
	var successStatusCode int

	oldClusterServiceCluster, err := f.clusterServiceClient.GetCluster(ctx, oldCosmosCluster.InternalID)
	if err != nil {
		logger.Error(fmt.Sprintf("failed to fetch CS cluster for %s: %v", resourceID, err))
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
		return
	}

	internalOldCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID, oldClusterServiceCluster)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
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
		logger.Error("unexpected method: " + request.Method)
		arm.WriteResourceNotFoundError(writer, resourceID)
		return
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, oldCosmosCluster)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	// TODO we appear to lack a test, but this seems to take an original, apply the patch and unmarshal the result, meaning the above patch step is just incorrect.
	cloudError = api.ApplyRequestBody(request, body, newExternalCluster)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	newInternalCluster := &api.HCPOpenShiftCluster{}
	newExternalCluster.Normalize(newInternalCluster)

	oldInternalCluster := &api.HCPOpenShiftCluster{}
	oldExternalCluster.Normalize(oldInternalCluster)
	validationErrs := validation.ValidateClusterUpdate(ctx, newInternalCluster, oldInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
	newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs)

	// prefer new validation.  Have a fallback for old validation.
	if newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	newClusterServiceClusterBuilder, newClusterServiceAutoscalerBuilder, err := ocm.BuildCSCluster(resourceID, request.Header, newInternalCluster, true)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	logger.Info(fmt.Sprintf("updating resource %s", resourceID))
	_, err = f.clusterServiceClient.UpdateClusterAutoscaler(ctx, oldCosmosCluster.InternalID, newClusterServiceAutoscalerBuilder)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
		return
	}
	resultingClusterServiceCluster, err := f.clusterServiceClient.UpdateCluster(ctx, oldCosmosCluster.InternalID, newClusterServiceClusterBuilder)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteCloudError(writer, ocm.CSErrorToCloudError(err, resourceID, writer.Header()))
		return
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
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Read back the resource document so the response body is accurate.
	resultingCosmosCluster, err := transactionResult.GetResourceDoc(cosmosID)
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

	responseBody, err := marshalCSCluster(resultingClusterServiceCluster, resultingInternalCluster, versionedInterface)
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

// ArmResourceDelete implements the deletion API contract for ARM
// * 200 if a deletion is successful
// * 202 if an asynchronous delete is initiated
// * 204 if a well-formed request attempts to delete a nonexistent resource
func (f *Frontend) ArmResourceDelete(writer http.ResponseWriter, request *http.Request) {
	const operationRequest = database.OperationRequestDelete

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	resourceItemID, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		// For resource not found errors on deletion, ARM requires
		// us to simply return 204 No Content and no response body.
		if database.IsResponseError(err, http.StatusNotFound) {
			writer.WriteHeader(http.StatusNoContent)
		} else {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
		}
		return
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, resourceDoc)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	transaction := f.dbClient.NewTransaction(pk)

	operationID, cloudError := f.DeleteResource(ctx, transaction, resourceItemID, resourceDoc)
	if cloudError != nil {
		// For resource not found errors on deletion, ARM requires
		// us to simply return 204 No Content and no response body.
		if cloudError.StatusCode == http.StatusNotFound {
			writer.WriteHeader(http.StatusNoContent)
		} else {
			arm.WriteCloudError(writer, cloudError)
		}
		return
	}

	f.ExposeOperation(writer, request, operationID, transaction)

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmResourceActionRequestAdminCredential(writer http.ResponseWriter, request *http.Request) {
	const operationRequest = database.OperationRequestRequestCredential

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Parent resource is the hcpOpenShiftCluster.
	resourceID = resourceID.Parent
	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, resourceDoc)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	// New credential cannot be requested while credentials are being revoked.

	iterator := f.dbClient.ListActiveOperationDocs(pk, &database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRevokeCredentials),
		ExternalID: resourceID,
	})

	for range iterator.Items(ctx) {
		writer.Header().Set("Retry-After", strconv.Itoa(10))
		arm.WriteConflictError(
			writer, resourceID,
			"Cannot request credential while credentials are being revoked")
		return
	}

	err = iterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	csCredential, err := f.clusterServiceClient.PostBreakGlassCredential(ctx, resourceDoc.InternalID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	internalID, err := ocm.NewInternalID(csCredential.HREF())
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	transaction := f.dbClient.NewTransaction(pk)

	operationDoc := database.NewOperationDocument(operationRequest, resourceID, internalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmResourceActionRevokeCredentials(writer http.ResponseWriter, request *http.Request) {
	const operationRequest = database.OperationRequestRevokeCredentials

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// Parent resource is the hcpOpenShiftCluster.
	resourceID = resourceID.Parent
	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	_, resourceDoc, err := f.dbClient.GetResourceDoc(ctx, resourceID)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	cloudError := f.CheckForProvisioningStateConflict(ctx, operationRequest, resourceDoc)
	if cloudError != nil {
		arm.WriteCloudError(writer, cloudError)
		return
	}

	// Credential revocation cannot be requested while another revocation is in progress.

	iterator := f.dbClient.ListActiveOperationDocs(pk, &database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRevokeCredentials),
		ExternalID: resourceID,
	})

	for range iterator.Items(ctx) {
		writer.Header().Set("Retry-After", strconv.Itoa(10))
		arm.WriteConflictError(
			writer, resourceID,
			"Credentials are already being revoked")
		return
	}

	err = iterator.GetError()
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	err = f.clusterServiceClient.DeleteBreakGlassCredentials(ctx, resourceDoc.InternalID)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	transaction := f.dbClient.NewTransaction(pk)

	// Just as deleting an ARM resource cancels any other operations on the resource,
	// revoking credentials cancels any credential requests in progress.
	err = f.CancelActiveOperations(ctx, transaction, &database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRequestCredential),
		ExternalID: resourceID,
	})
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	operationDoc := database.NewOperationDocument(operationRequest, resourceID, resourceDoc.InternalID, correlationData)
	operationID := transaction.CreateOperationDoc(operationDoc, nil)

	f.ExposeOperation(writer, request, operationID, transaction)

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmSubscriptionGet(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	subscription, err := f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			arm.WriteResourceNotFoundError(writer, resourceID)
		} else {
			arm.WriteInternalServerError(writer)
		}
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, subscription)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) ArmSubscriptionPut(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	body, err := BodyFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var subscription arm.Subscription
	err = json.Unmarshal(body, &subscription)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInvalidRequestContentError(writer, err)
		return
	}

	validationErrs := validation.ValidateSubscriptionCreate(ctx, &subscription)
	newValidationErr := arm.CloudErrorFromFieldErrors(validationErrs)
	if newValidationErr != nil {
		logger.Error(newValidationErr.Error())
		arm.WriteCloudError(writer, newValidationErr)
		return
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	_, err = f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if database.IsResponseError(err, http.StatusNotFound) {
		err = f.dbClient.CreateSubscriptionDoc(ctx, subscriptionID, &subscription)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		logger.Info(fmt.Sprintf("created document for subscription %s", subscriptionID))
	} else if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	} else {
		updated, err := f.dbClient.UpdateSubscriptionDoc(ctx, subscriptionID, func(updateSubscription *arm.Subscription) bool {
			messages := getSubscriptionDifferences(updateSubscription, &subscription)
			for _, message := range messages {
				logger.Info(message)
			}

			*updateSubscription = subscription

			return len(messages) > 0
		})
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
		if updated {
			logger.Info(fmt.Sprintf("updated document for subscription %s", subscriptionID))
		}
	}

	// Clean up resources if subscription is deleted.
	if subscription.State == arm.SubscriptionStateDeleted {
		cloudError := f.DeleteAllResources(ctx, subscriptionID)
		if cloudError != nil {
			arm.WriteCloudError(writer, cloudError)
			return
		}
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, subscription)
	if err != nil {
		logger.Error(err.Error())
	}
}

func (f *Frontend) ArmDeploymentPreflight(writer http.ResponseWriter, request *http.Request) {
	var subscriptionID = request.PathValue(PathSegmentSubscriptionID)
	var resourceGroup = request.PathValue(PathSegmentResourceGroupName)

	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	body, err := BodyFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	// TODO explain why it is safe to decode this directly into an internal type
	deploymentPreflight, cloudError := arm.UnmarshalDeploymentPreflight(body)
	if cloudError != nil {
		logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	preflightErrors := []arm.CloudErrorBody{}

	availableAROHCPVersions := f.apiRegistry.ListVersions()
	for index, raw := range deploymentPreflight.Resources {
		var cloudError *arm.CloudError

		// Check the raw JSON for any Template Language Expressions (TLEs).
		// If any are detected, skip the resource because Cluster Service
		// does not handle TLEs in its input validation.
		detectedTLE, err := arm.DetectTLE(raw)
		if err != nil {
			cloudError = arm.NewInvalidRequestContentError(err)
			// Preflight is best-effort: a malformed resource is not a validation failure.
			logger.Warn(cloudError.Message)
			continue
		}
		if detectedTLE {
			continue
		}

		preflightResource := &arm.DeploymentPreflightResource{}
		err = json.Unmarshal(raw, preflightResource)
		if err != nil {
			cloudError = arm.NewInvalidRequestContentError(err)
			// Preflight is best-effort: a malformed resource is not a validation failure.
			logger.Warn(cloudError.Message)
			continue
		}

		if !availableAROHCPVersions.Has(preflightResource.APIVersion) {
			// Preflight is best-effort: a malformed resource is not a validation failure.
			validationErr := arm.CloudErrorBody{
				Code:    arm.CloudErrorCodeInvalidRequestContent,
				Message: fmt.Sprintf("Unrecognized API version '%s'", preflightResource.APIVersion),
				Target:  "apiVersion",
			}
			logger.Warn(
				fmt.Sprintf("Resource #%d failed preliminary validation (see details)", index+1),
				"details", validationErr)
			continue
		}

		switch strings.ToLower(preflightResource.Type) {
		case strings.ToLower(api.ClusterResourceType.String()):
			// API version is already validated by this point.
			versionedInterface, _ := f.apiRegistry.Lookup(preflightResource.APIVersion)
			versionedCluster := versionedInterface.NewHCPOpenShiftCluster(nil)

			err = preflightResource.Convert(versionedCluster)
			if err != nil {
				// Preflight is best effort: failure to parse a resource is not a validation failure.
				logger.Warn(fmt.Sprintf("Failed to unmarshal %s resource named '%s': %s", preflightResource.Type, preflightResource.Name, err))
				continue
			}

			newInternalCluster := &api.HCPOpenShiftCluster{}
			versionedCluster.Normalize(newInternalCluster)
			validationErrs := validation.ValidateClusterCreate(ctx, newInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
			cloudError = arm.CloudErrorFromFieldErrors(validationErrs)

		case strings.ToLower(api.NodePoolResourceType.String()):
			// API version is already validated by this point.
			versionedInterface, _ := f.apiRegistry.Lookup(preflightResource.APIVersion)
			versionedNodePool := versionedInterface.NewHCPOpenShiftClusterNodePool(nil)

			err = preflightResource.Convert(versionedNodePool)
			if err != nil {
				// Preflight is best effort: failure to parse a resource is not a validation failure.
				logger.Warn(fmt.Sprintf("Failed to unmarshal %s resource named '%s': %s", preflightResource.Type, preflightResource.Name, err))
				continue
			}

			// Perform static validation as if for a node pool creation request.
			newInternalNodePool := &api.HCPOpenShiftClusterNodePool{}
			versionedNodePool.Normalize(newInternalNodePool)
			validationErrs := validation.ValidateNodePoolCreate(ctx, newInternalNodePool)
			cloudError = arm.CloudErrorFromFieldErrors(validationErrs)

		case strings.ToLower(api.ExternalAuthResourceType.String()):
			// API version is already validated by this point.
			versionedInterface, _ := f.apiRegistry.Lookup(preflightResource.APIVersion)
			versionedExternalAuth := versionedInterface.NewHCPOpenShiftClusterExternalAuth(nil)

			err = preflightResource.Convert(versionedExternalAuth)
			if err != nil {
				// Preflight is best effort: failure to parse a resource is not a validation failure.
				logger.Warn(fmt.Sprintf("Failed to unmarshal %s resource named '%s': %s", preflightResource.Type, preflightResource.Name, err))
				continue
			}

			// Perform static validation as if for an external auth creation request.
			newInternalAuth := &api.HCPOpenShiftClusterExternalAuth{}
			versionedExternalAuth.Normalize(newInternalAuth)
			validationErrs := validation.ValidateExternalAuthCreate(ctx, newInternalAuth)
			cloudError = arm.CloudErrorFromFieldErrors(validationErrs)

		default:
			// Disregard foreign resource types.
			continue
		}

		if cloudError != nil {
			var details []arm.CloudErrorBody

			// This avoids double-nesting details when there's multiple errors.
			//
			// To illustrate, instead of:
			//
			// {
			//   "code": "MultipleErrorsOccurred"
			//   "message": "Content validation failed for {{RESOURCE_NAME}}"
			//   "target": "{{RESOURCE_ID}}"
			//   "details": [
			//     {
			//       "code": "MultipleErrorsOccurred"
			//       "message": "Content validation failed on multiple fields"
			//       "details": [
			//         ...field-specific validation errors...
			//       ]
			//     }
			//   ]
			// }
			//
			// we want:
			//
			// {
			//   "code": "MultipleErrorsOccurred"
			//   "message": "Content validation failed for {{RESOURCE_NAME}}"
			//   "target": "{{RESOURCE_ID}}"
			//   "details": [
			//     ...field-specific validation errors...
			//   ]
			// }
			//
			if len(cloudError.Details) > 0 {
				details = cloudError.Details
			} else {
				details = []arm.CloudErrorBody{*cloudError.CloudErrorBody}
			}
			preflightErrors = append(preflightErrors, arm.CloudErrorBody{
				Code:    cloudError.Code,
				Message: fmt.Sprintf("Content validation failed for '%s'", preflightResource.Name),
				Target:  preflightResource.ResourceID(subscriptionID, resourceGroup),
				Details: details,
			})
			continue
		}

		// FIXME Further preflight steps go here.
	}

	arm.WriteDeploymentPreflightResponse(writer, preflightErrors)
}

func (f *Frontend) OperationStatus(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := LoggerFromContext(ctx)

	resourceID, err := ResourceIDFromContext(ctx)
	if err != nil {
		logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	doc, err := f.dbClient.GetOperationDoc(ctx, pk, resourceID.Name)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			writer.WriteHeader(http.StatusNotFound)
		} else {
			writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, resourceID.Name, doc) {
		writer.WriteHeader(http.StatusNotFound)
		return
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, doc.ToStatus())
	if err != nil {
		logger.Error(err.Error())
	}
}

// marshalCSCluster renders a CS Cluster object in JSON format, applying
// the necessary conversions for the API version of the request.
func marshalCSCluster(csCluster *arohcpv1alpha1.Cluster, internalCluster *api.HCPOpenShiftCluster, versionedInterface api.Version) ([]byte, error) {
	resourceID, err := azcorearm.ParseResourceID(internalCluster.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource ID: %w", err)
	}

	clusterServiceBasedInternalCluster, err := ocm.ConvertCStoHCPOpenShiftCluster(resourceID, csCluster)
	if err != nil {
		return nil, err
	}

	clusterServiceBasedInternalCluster.SystemData = internalCluster.SystemData
	clusterServiceBasedInternalCluster.Tags = maps.Clone(internalCluster.Tags)
	clusterServiceBasedInternalCluster.ServiceProviderProperties.ProvisioningState = internalCluster.ServiceProviderProperties.ProvisioningState
	if clusterServiceBasedInternalCluster.Identity == nil {
		clusterServiceBasedInternalCluster.Identity = &arm.ManagedServiceIdentity{}
	}

	if internalCluster.Identity != nil {
		clusterServiceBasedInternalCluster.Identity.PrincipalID = internalCluster.Identity.PrincipalID
		clusterServiceBasedInternalCluster.Identity.TenantID = internalCluster.Identity.TenantID
		clusterServiceBasedInternalCluster.Identity.Type = internalCluster.Identity.Type
	}

	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftCluster(clusterServiceBasedInternalCluster))
}

func getSubscriptionDifferences(oldSub, newSub *arm.Subscription) []string {
	var messages []string

	if oldSub.State != newSub.State {
		messages = append(messages, fmt.Sprintf("Subscription state changed from %s to %s", oldSub.State, newSub.State))
	}

	if oldSub.Properties != nil && newSub.Properties != nil {
		if oldSub.Properties.TenantId != nil && newSub.Properties.TenantId != nil &&
			*oldSub.Properties.TenantId != *newSub.Properties.TenantId {
			messages = append(messages, fmt.Sprintf("Subscription tenantId changed from %s to %s", *oldSub.Properties.TenantId, *newSub.Properties.TenantId))
		}

		if oldSub.Properties.RegisteredFeatures != nil && newSub.Properties.RegisteredFeatures != nil {
			oldFeatures := featuresMap(oldSub.Properties.RegisteredFeatures)
			newFeatures := featuresMap(newSub.Properties.RegisteredFeatures)

			for featureName, oldState := range oldFeatures {
				newState, exists := newFeatures[featureName]
				if !exists {
					messages = append(messages, fmt.Sprintf("Feature %s removed", featureName))
				} else if oldState != newState {
					messages = append(messages, fmt.Sprintf("Feature %s state changed from %s to %s", featureName, oldState, newState))
				}
			}
			for featureName, newState := range newFeatures {
				if _, exists := oldFeatures[featureName]; !exists {
					messages = append(messages, fmt.Sprintf("Feature %s added with state %s", featureName, newState))
				}
			}
		}
	}

	return messages
}

func (f *Frontend) OperationResult(writer http.ResponseWriter, request *http.Request) {
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

	pk := database.NewPartitionKey(resourceID.SubscriptionID)

	doc, err := f.dbClient.GetOperationDoc(ctx, pk, resourceID.Name)
	if err != nil {
		logger.Error(err.Error())
		if database.IsResponseError(err, http.StatusNotFound) {
			writer.WriteHeader(http.StatusNotFound)
		} else {
			writer.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, resourceID.Name, doc) {
		writer.WriteHeader(http.StatusNotFound)
		return
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
	switch doc.Status {
	case arm.ProvisioningStateSucceeded:
		// Handled below.
	case arm.ProvisioningStateFailed, arm.ProvisioningStateCanceled:
		// Should never be reached?
		arm.WriteInternalServerError(writer)
		return
	default:
		// Operation is still in progress.
		f.AddLocationHeader(writer, request, doc.OperationID)
		writer.WriteHeader(http.StatusAccepted)
		return
	}

	// The response henceforth should be exactly as though the operation
	// succeeded synchronously.

	var successStatusCode int

	switch doc.Request {
	case database.OperationRequestCreate:
		successStatusCode = http.StatusCreated
	case database.OperationRequestUpdate:
		successStatusCode = http.StatusOK
	case database.OperationRequestDelete:
		writer.WriteHeader(http.StatusNoContent)
		return
	case database.OperationRequestRequestCredential:
		successStatusCode = http.StatusOK
	case database.OperationRequestRevokeCredentials:
		writer.WriteHeader(http.StatusNoContent)
		return
	default:
		logger.Error(fmt.Sprintf("Unhandled request type: %s", doc.Request))
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	var responseBody []byte
	var cloudError *arm.CloudError

	switch doc.InternalID.Kind() {
	case cmv1.BreakGlassCredentialKind:
		csBreakGlassCredential, err := f.clusterServiceClient.GetBreakGlassCredential(ctx, doc.InternalID)
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

		responseBody, err = versionedInterface.MarshalHCPOpenShiftClusterAdminCredential(ocm.ConvertCStoAdminCredential(csBreakGlassCredential))
		if err != nil {
			logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}

	case arohcpv1alpha1.ClusterKind:
		responseBody, cloudError = f.MarshalCluster(ctx, doc.ExternalID, versionedInterface)
		if cloudError != nil {
			arm.WriteCloudError(writer, cloudError)
			return
		}

	default:
		responseBody, cloudError = f.MarshalResource(ctx, doc.ExternalID, versionedInterface)
		if cloudError != nil {
			arm.WriteCloudError(writer, cloudError)
			return
		}
	}

	_, err = arm.WriteJSONResponse(writer, successStatusCode, responseBody)
	if err != nil {
		logger.Error(err.Error())
	}
}

func featuresMap(features *[]arm.Feature) map[string]string {
	if features == nil {
		return nil
	}
	featureMap := make(map[string]string, len(*features))
	for _, feature := range *features {
		if feature.Name != nil && feature.State != nil {
			featureMap[*feature.Name] = *feature.State
		}
	}
	return featureMap
}

func marshalCSVersion(resourceID azcorearm.ResourceID, version *arohcpv1alpha1.Version, versionedInterface api.Version) ([]byte, error) {
	hcpVersion := ocm.ConvertCStoHCPOpenShiftVersion(resourceID, version)
	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftVersion(hcpVersion))
}
