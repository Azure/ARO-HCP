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

	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"

	"github.com/Azure/ARO-HCP/frontend/pkg/metrics"
	"github.com/Azure/ARO-HCP/internal/admission"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/api/v20240610preview"
	"github.com/Azure/ARO-HCP/internal/api/v20251223preview"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
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
	// this is the azure location for this instance of the frontend
	azureLocation string

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
	azureLocation string,
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
				ctx = utils.ContextWithLogger(ctx, logger)
				return ctx
			},
		},
		metricsServer: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return utils.ContextWithLogger(context.Background(), logger)
			},
		},
		auditClient: auditClient,
		dbClient:    dbClient,
		done:        make(chan struct{}),
		collector:   metrics.NewSubscriptionCollector(reg, dbClient, azureLocation),
		healthGauge: promauto.With(reg).NewGauge(
			prometheus.GaugeOpts{
				Name: healthGaugeName,
				Help: "Reports the health status of the service (0: not healthy, 1: healthy).",
			},
		),
		azureLocation: azureLocation,
		apiRegistry:   apiRegistry,
	}

	f.server.Handler = f.routes(reg)
	f.metricsServer.Handler = f.metricsRoutes()

	return f
}

func (f *Frontend) Run(ctx context.Context, stop <-chan struct{}) {
	if len(f.azureLocation) == 0 {
		panic("azureLocation must be set")
	}

	// This just digs up the logger passed to NewFrontend.
	logger := utils.LoggerFromContext(f.server.BaseContext(f.listener))

	if stop != nil {
		go func() {
			<-stop
			_ = f.server.Shutdown(ctx)
			_ = f.metricsServer.Shutdown(ctx)
			close(f.done)
		}()
	}

	// before we start the http handler (this should ensure we readiness checks until this is complete), we will do a cosmos
	// data migration to our new storage keys.
	logger.Info("starting cosmos data migration")
	MigrateCosmosOrDie(ctx, f.dbClient, f.clusterServiceClient, f.azureLocation)
	logger.Info("completed cosmos data migration")

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
	_, _ = writer.Write([]byte(f.azureLocation))
}

func dbListOptionsFromRequest(request *http.Request) *database.DBClientListResourceDocsOptions {
	// FIXME We may want to cap pageSizeHint. If we get a large enough
	//       $top argument (and there's enough actual clusters to reach
	//       that), we could potentially hit the 8MB response size limit.

	options := &database.DBClientListResourceDocsOptions{
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
	return options
}

func (f *Frontend) ArmResourceListVersion(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	location := request.PathValue(PathSegmentLocation)

	pagedResponse := arm.NewPagedResponse()

	csIterator := f.clusterServiceClient.ListVersions()
	for csVersion := range csIterator.Items(ctx) {
		versionName := strings.Replace(csVersion.ID(), api.OpenShiftVersionPrefix, "", 1)
		stringResource := "/subscriptions/" + subscriptionID + "/providers/" + api.ProviderNamespace +
			"/locations/" + location + "/" + api.VersionResourceTypeName + "/" + versionName
		resourceID, err := azcorearm.ParseResourceID(stringResource)
		if err != nil {
			return utils.TrackError(err)
		}
		value, err := marshalCSVersion(resourceID, csVersion, versionedInterface)
		if err != nil {
			return utils.TrackError(err)
		}
		pagedResponse.AddValue(value)
	}
	err = csIterator.GetError()

	// Check for iteration error.
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, pagedResponse)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

// GetOpenshiftVersions implements the GET single resource API contract for ARM
// * 200 If the resource exists
// * 404 If the resource does not exist
func (f *Frontend) GetOpenshiftVersions(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	versionName := resourceID.Name
	version, err := f.clusterServiceClient.GetVersion(ctx, versionName)
	if err != nil {
		return utils.TrackError(err)
	}
	responseBody, err := marshalCSVersion(resourceID, version, versionedInterface)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, responseBody)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (f *Frontend) ArmResourceActionRequestAdminCredential(writer http.ResponseWriter, request *http.Request) error {
	const operationRequest = database.OperationRequestRequestCredential

	ctx := request.Context()

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Parent resource is the hcpOpenShiftCluster.
	clusterResourceID := resourceID.Parent

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	cluster, err := f.dbClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	if err := checkForProvisioningStateConflict(ctx, f.dbClient, operationRequest, cluster.ID, cluster.ServiceProviderProperties.ProvisioningState); err != nil {
		return utils.TrackError(err)
	}

	// New credential cannot be requested while credentials are being revoked.

	iterator := f.dbClient.Operations(clusterResourceID.SubscriptionID).ListActiveOperations(&database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRevokeCredentials),
		ExternalID: clusterResourceID,
	})

	for range iterator.Items(ctx) {
		writer.Header().Set("Retry-After", strconv.Itoa(10))
		return arm.NewConflictError(clusterResourceID, "Cannot request credential while credentials are being revoked")
	}

	err = iterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}

	csCredential, err := f.clusterServiceClient.PostBreakGlassCredential(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	csCredentialClusterServiceID, err := api.NewInternalID(csCredential.HREF())
	if err != nil {
		return utils.TrackError(err)
	}

	transaction := f.dbClient.NewTransaction(clusterResourceID.SubscriptionID)

	operationDoc := database.NewOperation(
		operationRequest,
		clusterResourceID,
		csCredentialClusterServiceID,
		f.azureLocation,
		request.Header.Get(arm.HeaderNameHomeTenantID),
		request.Header.Get(arm.HeaderNameClientObjectID),
		request.Header.Get(arm.HeaderNameAsyncNotificationURI),
		correlationData)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, operationDoc.NotificationURI, operationDoc.OperationID))
	_, err = f.dbClient.Operations(clusterResourceID.SubscriptionID).AddCreateToTransaction(ctx, transaction, operationDoc, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	writer.WriteHeader(http.StatusAccepted)
	return nil
}

func (f *Frontend) ArmResourceActionRevokeCredentials(writer http.ResponseWriter, request *http.Request) error {
	const operationRequest = database.OperationRequestRevokeCredentials

	ctx := request.Context()

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// Parent resource is the hcpOpenShiftCluster.
	clusterResourceID := resourceID.Parent

	correlationData, err := CorrelationDataFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	cluster, err := f.dbClient.HCPClusters(clusterResourceID.SubscriptionID, clusterResourceID.ResourceGroupName).Get(ctx, clusterResourceID.Name)
	if err != nil {
		return utils.TrackError(err)
	}

	// CheckForProvisioningStateConflict does not log conflict errors
	// but does log unexpected errors like database failures.
	if err := checkForProvisioningStateConflict(ctx, f.dbClient, operationRequest, cluster.ID, cluster.ServiceProviderProperties.ProvisioningState); err != nil {
		return utils.TrackError(err)
	}

	// Credential revocation cannot be requested while another revocation is in progress.

	iterator := f.dbClient.Operations(clusterResourceID.SubscriptionID).ListActiveOperations(&database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRevokeCredentials),
		ExternalID: clusterResourceID,
	})

	for range iterator.Items(ctx) {
		writer.Header().Set("Retry-After", strconv.Itoa(10))
		return arm.NewConflictError(clusterResourceID, "Credentials are already being revoked")
	}

	err = iterator.GetError()
	if err != nil {
		return utils.TrackError(err)
	}

	err = f.clusterServiceClient.DeleteBreakGlassCredentials(ctx, cluster.ServiceProviderProperties.ClusterServiceID)
	if err != nil {
		return utils.TrackError(err)
	}

	transaction := f.dbClient.NewTransaction(clusterResourceID.SubscriptionID)

	// Just as deleting an ARM resource cancels any other operations on the resource,
	// revoking credentials cancels any credential requests in progress.
	err = f.CancelActiveOperations(ctx, transaction, &database.DBClientListActiveOperationDocsOptions{
		Request:    api.Ptr(database.OperationRequestRequestCredential),
		ExternalID: clusterResourceID,
	})
	if err != nil {
		return utils.TrackError(err)
	}

	operationDoc := database.NewOperation(
		operationRequest,
		clusterResourceID,
		cluster.ServiceProviderProperties.ClusterServiceID,
		f.azureLocation,
		request.Header.Get(arm.HeaderNameHomeTenantID),
		request.Header.Get(arm.HeaderNameClientObjectID),
		request.Header.Get(arm.HeaderNameAsyncNotificationURI),
		correlationData)
	transaction.OnSuccess(addOperationResponseHeaders(writer, request, operationDoc.NotificationURI, operationDoc.OperationID))
	_, err = f.dbClient.Operations(operationDoc.OperationID.SubscriptionID).AddCreateToTransaction(ctx, transaction, operationDoc, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = transaction.Execute(ctx, nil)
	if err != nil {
		return utils.TrackError(err)
	}

	writer.WriteHeader(http.StatusAccepted)
	return nil
}

func (f *Frontend) ArmSubscriptionGet(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	subscription, err := f.dbClient.Subscriptions().Get(ctx, subscriptionID)
	if database.IsResponseError(err, http.StatusNotFound) {
		return arm.NewResourceNotFoundError(resourceID)
	}
	if err != nil {
		return utils.TrackError(err)
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, subscription)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (f *Frontend) ArmSubscriptionPut(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	body, err := BodyFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	var requestSubscription arm.Subscription
	err = json.Unmarshal(body, &requestSubscription)
	if err != nil {
		return arm.NewInvalidRequestContentError(err)
	}
	requestSubscription.ResourceID, err = arm.ToSubscriptionResourceID(subscriptionID)
	if err != nil {
		return utils.TrackError(err)
	}

	validationErrs := validation.ValidateSubscriptionCreate(ctx, &requestSubscription)
	if err := arm.CloudErrorFromFieldErrors(validationErrs); err != nil {
		return utils.TrackError(err)
	}

	var resultingSubscription *arm.Subscription
	existingSubscription, err := f.dbClient.Subscriptions().Get(ctx, subscriptionID)
	if database.IsResponseError(err, http.StatusNotFound) {
		resultingSubscription, err = f.dbClient.Subscriptions().Create(ctx, &requestSubscription, nil)
		if err != nil {
			return utils.TrackError(err)
		}
		logger.Info(fmt.Sprintf("created document for subscription %s", subscriptionID))
	} else if err != nil {
		return utils.TrackError(err)
	} else {
		messages := getSubscriptionDifferences(existingSubscription, &requestSubscription)
		for _, message := range messages {
			logger.Info(message)
		}
		if len(messages) > 0 {
			resultingSubscription, err = f.dbClient.Subscriptions().Replace(ctx, &requestSubscription, nil)
			if err != nil {
				return utils.TrackError(err)
			}
			logger.Info(fmt.Sprintf("updated document for subscription %s", subscriptionID))
		} else {
			resultingSubscription = existingSubscription
		}
	}

	// Clean up resources if subscription is deleted.
	if resultingSubscription.State == arm.SubscriptionStateDeleted {
		if err := f.DeleteAllResourcesInSubscription(ctx, subscriptionID); err != nil {
			return utils.TrackError(err)
		}
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, resultingSubscription)
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func (f *Frontend) ArmDeploymentPreflight(writer http.ResponseWriter, request *http.Request) error {
	var subscriptionID = request.PathValue(PathSegmentSubscriptionID)
	var resourceGroup = request.PathValue(PathSegmentResourceGroupName)

	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	subscription, err := f.dbClient.Subscriptions().Get(ctx, subscriptionID)
	if err != nil {
		return err
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	// TODO explain why it is safe to decode this directly into an internal type
	deploymentPreflight, err := arm.UnmarshalDeploymentPreflight(body)
	if err != nil {
		return utils.TrackError(err)
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

			newInternalCluster := versionedCluster.ConvertToInternal()
			// the external type lacks sufficient data to full produce a valid resourceID.  We do that separately here.
			parts := []string{
				"/subscriptions", subscriptionID,
				"resourceGroups", resourceGroup,
				"providers", api.ClusterResourceType.String(), newInternalCluster.Name,
			}
			newInternalCluster.ID, err = azcorearm.ParseResourceID(strings.Join(parts, "/"))
			if err != nil {
				// this indicates something really strange happened, return an error for it.
				return utils.TrackError(err)
			}
			validationErrs := validation.ValidateClusterCreate(ctx, newInternalCluster, api.Must(versionedInterface.ValidationPathRewriter(&api.HCPOpenShiftCluster{})))
			validationErrs = append(validationErrs, admission.AdmitClusterOnCreate(ctx, newInternalCluster, subscription)...)
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
			newInternalNodePool := versionedNodePool.ConvertToInternal()
			// the external type lacks sufficient data to full produce a valid resourceID.  We do that separately here.
			parts := []string{
				"/subscriptions", subscriptionID,
				"resourceGroups", resourceGroup,
				"providers", api.ClusterResourceType.String(), "preflight",
				api.NodePoolResourceType.Types[len(api.NodePoolResourceType.Types)-1], newInternalNodePool.Name,
			}
			newInternalNodePool.ID, err = azcorearm.ParseResourceID(strings.Join(parts, "/"))
			if err != nil {
				// this indicates something really strange happened, return an error for it.
				return utils.TrackError(err)
			}
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
			newInternalAuth := versionedExternalAuth.ConvertToInternal()
			// the external type lacks sufficient data to full produce a valid resourceID.  We do that separately here.
			parts := []string{
				"/subscriptions", subscriptionID,
				"resourceGroups", resourceGroup,
				"providers", api.ClusterResourceType.String(), "preflight",
				api.ExternalAuthResourceType.Types[len(api.NodePoolResourceType.Types)-1], newInternalAuth.Name,
			}
			newInternalAuth.ID, err = azcorearm.ParseResourceID(strings.Join(parts, "/"))
			if err != nil {
				// this indicates something really strange happened, return an error for it.
				return utils.TrackError(err)
			}
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
	return nil
}

func (f *Frontend) OperationStatus(writer http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	logger := utils.LoggerFromContext(ctx)

	resourceID, err := utils.ResourceIDFromContext(ctx)
	if err != nil {
		return utils.TrackError(err)
	}

	operation, err := f.dbClient.Operations(resourceID.SubscriptionID).GetByID(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		// try using the new storage ID
		// we store operations without the location so the type stays as we expect/predict
		operationStorageResourceID := path.Join(
			"/subscriptions", resourceID.SubscriptionID,
			"providers", api.OperationStatusResourceType.String(),
			resourceID.Name,
		)
		operation, err = f.dbClient.Operations(resourceID.SubscriptionID).GetByID(ctx, api.Must(api.ResourceIDStringToCosmosID(operationStorageResourceID)))
	}
	if err != nil {
		return utils.TrackError(err)
	}

	// Validate the identity retrieving the operation result is the
	// same identity that triggered the operation. Return 404 if not.
	if !f.OperationIsVisible(request, operation) {
		logger.Warn("operation result not visible to requester")
		writer.WriteHeader(http.StatusNotFound)
		return nil
	}

	_, err = arm.WriteJSONResponse(writer, http.StatusOK, database.ToStatus(operation))
	if err != nil {
		return utils.TrackError(err)
	}
	return nil
}

func getSubscriptionDifferences(oldSub, newSub *arm.Subscription) []string {
	var messages []string

	if oldSub.State != newSub.State {
		messages = append(messages, fmt.Sprintf("Subscription state changed from %s to %s", oldSub.State, newSub.State))
	}

	if oldSub.Properties == nil {
		oldSub.Properties = &arm.SubscriptionProperties{}
	}
	if newSub.Properties == nil {
		newSub.Properties = &arm.SubscriptionProperties{}
	}

	var oldTenantId, newTenantId string

	if oldSub.Properties.TenantId != nil {
		oldTenantId = *oldSub.Properties.TenantId
	}
	if newSub.Properties.TenantId != nil {
		newTenantId = *newSub.Properties.TenantId
	}

	if oldTenantId != newTenantId {
		messages = append(messages, fmt.Sprintf("Subscription tenantId changed from %s to %s", oldTenantId, newTenantId))
	}

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

	return messages
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

	operation, err := f.dbClient.Operations(resourceID.SubscriptionID).GetByID(ctx, resourceID.Name)
	if database.IsResponseError(err, http.StatusNotFound) {
		// try using the new storage ID
		// we store operations without the location so the type stays as we expect/predict
		operationStorageResourceID := path.Join(
			"/subscriptions", resourceID.SubscriptionID,
			"providers", api.OperationStatusResourceType.String(),
			resourceID.Name,
		)
		operation, err = f.dbClient.Operations(resourceID.SubscriptionID).GetByID(ctx, api.Must(api.ResourceIDStringToCosmosID(operationStorageResourceID)))
	}
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

	case operation.InternalID.Kind() == arohcpv1alpha1.ClusterKind:
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

func featuresMap(features *[]arm.Feature) map[string]string {
	featureMap := make(map[string]string)
	if features != nil {
		for _, feature := range *features {
			if feature.Name != nil && feature.State != nil {
				featureMap[*feature.Name] = *feature.State
			}
		}
	}
	return featureMap
}

func marshalCSVersion(resourceID *azcorearm.ResourceID, version *arohcpv1alpha1.Version, versionedInterface api.Version) ([]byte, error) {
	hcpVersion := ocm.ConvertCStoHCPOpenShiftVersion(resourceID, version)
	return arm.MarshalJSON(versionedInterface.NewHCPOpenShiftVersion(hcpVersion))
}
