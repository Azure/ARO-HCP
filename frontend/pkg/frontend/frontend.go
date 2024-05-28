package frontend

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

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
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	sdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/frontend/pkg/database"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

const (
	PatternSubscriptions  = "subscriptions/{" + PathSegmentSubscriptionID + "}"
	PatternLocations      = "locations/{" + PageSegmentLocation + "}"
	PatternProviders      = "providers/" + api.ResourceType
	PatternDeployments    = "deployments/{" + PathSegmentDeploymentName + "}"
	PatternResourceGroups = "resourcegroups/{" + PathSegmentResourceGroupName + "}"
	PatternResourceName   = "{" + PathSegmentResourceName + "}"
	PatternActionName     = "{" + PathSegmentActionName + "}"
)

type Frontend struct {
	conn     *sdk.Connection
	logger   *slog.Logger
	listener net.Listener
	server   http.Server
	cache    Cache // TODO: Delete
	dbClient database.DBClient
	ready    atomic.Value
	done     chan struct{}
	metrics  Emitter
	region   string
}

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

func NewFrontend(logger *slog.Logger, listener net.Listener, emitter Emitter, dbClient database.DBClient, region string, conn *sdk.Connection) *Frontend {
	f := &Frontend{
		conn:     conn,
		logger:   logger,
		listener: listener,
		metrics:  emitter,
		server: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), logger)
			},
		},
		cache:    *NewCache(),
		dbClient: dbClient,
		done:     make(chan struct{}),
		region:   region,
	}

	subscriptionStateMuxValidator := NewSubscriptionStateMuxValidator(&f.cache)

	// Setup metrics middleware
	metricsMiddleware := MetricsMiddleware{cache: &f.cache, Emitter: emitter}

	mux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareLogging,
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData,
		MiddlewareValidateStatic,
		metricsMiddleware.Metrics(),
	)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz", "ready"), f.HealthzReady)
	// TODO: determine where in the auth chain we should allow for this endpoint to be called by ARM
	mux.HandleFunc(MuxPattern(http.MethodGet, PatternSubscriptions), f.ArmSubscriptionGet)
	mux.HandleFunc(MuxPattern(http.MethodPut, PatternSubscriptions), f.ArmSubscriptionPut)

	// Expose Prometheus metrics endpoint
	mux.Handle(MuxPattern(http.MethodGet, "metrics"), promhttp.Handler())

	// Authenticated routes
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceListBySubscription))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternLocations, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceListByLocation))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders),
		postMuxMiddleware.HandlerFunc(f.ArmResourceListByResourceGroup))
	mux.Handle(
		MuxPattern(http.MethodGet, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		MuxPattern(http.MethodPut, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodPatch, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternActionName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))

	// Exclude ARO-HCP API version validation for endpoints defined by ARM.
	postMuxMiddleware = NewMiddleware(
		MiddlewareLoggingPostMux,
		subscriptionStateMuxValidator.MiddlewareValidateSubscriptionState)
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, "providers", api.ProviderNamespace, PatternDeployments, "preflight"),
		postMuxMiddleware.HandlerFunc(f.ArmDeploymentPreflight))

	f.server.Handler = mux

	return f
}

func (f *Frontend) Run(ctx context.Context, stop <-chan struct{}) {
	if stop != nil {
		go func() {
			<-stop
			f.ready.Store(false)
			_ = f.server.Shutdown(ctx)
		}()
	}

	f.logger.Info(fmt.Sprintf("listening on %s", f.listener.Addr().String()))

	f.ready.Store(true)

	err := f.server.Serve(f.listener)
	if err != http.ErrServerClosed {
		f.logger.Error(err.Error())
		os.Exit(1)
	}

	close(f.done)
}

func (f *Frontend) Join() {
	<-f.done
}

func (f *Frontend) CheckReady() bool {
	return f.ready.Load().(bool)
}

func (f *Frontend) NotFound(writer http.ResponseWriter, request *http.Request) {
	arm.WriteError(
		writer, http.StatusNotFound,
		arm.CloudErrorCodeNotFound, "",
		"The requested path could not be found.")
}

func (f *Frontend) HealthzReady(writer http.ResponseWriter, request *http.Request) {
	var healthStatus float64
	if f.CheckReady() {
		writer.WriteHeader(http.StatusOK)
		healthStatus = 1.0
	} else {
		writer.WriteHeader(http.StatusInternalServerError)
		healthStatus = 0.0
	}

	f.metrics.EmitGauge("frontend_health", healthStatus, map[string]string{
		"endpoint": "/healthz/ready",
	})
}

func (f *Frontend) ArmResourceListBySubscription(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceListBySubscription", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceListByLocation(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceListByLocation", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceListByResourceGroup(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceListByResourceGroup", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceRead(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceRead", versionedInterface))

	// URL path is already lowercased by middleware.
	resourceID := request.URL.Path
	cluster, found := f.cache.GetCluster(resourceID)
	if !found {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	versionedResource := versionedInterface.NewHCPOpenShiftCluster(cluster)
	resp, err := json.Marshal(versionedResource)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}
	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceCreateOrUpdate(writer http.ResponseWriter, request *http.Request) {
	var err error

	// This handles both PUT and PATCH requests. The only notable
	// difference is PATCH requests will not create a new cluster.

	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceCreateOrUpdate", versionedInterface))

	// URL path is already lowercased by middleware.
	resourceID := request.URL.Path
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	cluster, updating := f.cache.GetCluster(resourceID)
	versionedCurrentCluster := versionedInterface.NewHCPOpenShiftCluster(cluster)

	var versionedRequestCluster api.VersionedHCPOpenShiftCluster
	switch request.Method {
	case http.MethodPut:
		versionedRequestCluster = versionedInterface.NewHCPOpenShiftCluster(nil)
	case http.MethodPatch:
		if cluster == nil {
			// PATCH request will not create a new cluster.
			originalPath, _ := OriginalPathFromContext(ctx)
			f.logger.Error("Resource not found")
			arm.WriteError(
				writer, http.StatusNotFound, arm.CloudErrorCodeNotFound,
				originalPath, "Resource not found")
			return
		}
		versionedRequestCluster = versionedInterface.NewHCPOpenShiftCluster(cluster)
	}

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	if err = json.Unmarshal(body, versionedRequestCluster); err != nil {
		f.logger.Error(err.Error())
		arm.WriteCloudError(writer, arm.NewUnmarshalCloudError(err))
		return
	}

	if cloudError := versionedRequestCluster.ValidateStatic(versionedCurrentCluster, updating, request.Method); cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	cluster = api.NewDefaultHCPOpenShiftCluster()
	versionedRequestCluster.Normalize(cluster)
	f.cache.SetCluster(resourceID, cluster)

	var doc *database.HCPOpenShiftClusterDocument
	doc, err = f.dbClient.GetClusterDoc(ctx, resourceID, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("existing document not found for cluster - creating one for %s", resourceID))
			doc = &database.HCPOpenShiftClusterDocument{
				ID:           uuid.New().String(),
				Key:          resourceID,
				ClusterID:    NewUID(),
				PartitionKey: subscriptionID,
			}
		} else {
			f.logger.Error(fmt.Sprintf("failed to fetch document for %s: %v", resourceID, err))
			arm.WriteInternalServerError(writer)
			return
		}
	}

	err = f.dbClient.SetClusterDoc(ctx, doc)
	if err != nil {
		f.logger.Error(fmt.Sprintf("failed to create document for resource %s: %v", resourceID, err))
	}
	f.logger.Info(fmt.Sprintf("document created for %s", resourceID))

	resp, err := json.Marshal(versionedRequestCluster)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}

	switch request.Method {
	case http.MethodPut:
		writer.WriteHeader(http.StatusCreated)
	case http.MethodPatch:
		writer.WriteHeader(http.StatusAccepted)
	}
}

func (f *Frontend) ArmResourceDelete(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceDelete", versionedInterface))

	// URL path is already lowercased by middleware.
	resourceID := request.URL.Path
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	_, found := f.cache.GetCluster(resourceID)
	if !found {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	f.cache.DeleteCluster(resourceID)

	err = f.dbClient.DeleteClusterDoc(ctx, resourceID, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("cluster document cannot be deleted -- document not found for %s", resourceID))
			writer.WriteHeader(http.StatusNoContent)
			return
		} else {
			f.logger.Error(err.Error())
			arm.WriteInternalServerError(writer)
			return
		}
	}
	f.logger.Info(fmt.Sprintf("document deleted for resource %s", resourceID))

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmResourceAction(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourceAction", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmSubscriptionGet(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	subscriptionID := request.PathValue(PathSegmentSubscriptionID)

	doc, err := f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Error(fmt.Sprintf("document not found for subscription %s", subscriptionID))
			writer.WriteHeader(http.StatusNotFound)
			return
		} else {
			f.logger.Error(err.Error())
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	resp, err := json.Marshal(&doc)
	if err != nil {
		f.logger.Error(err.Error())
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmSubscriptionPut(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	var subscription arm.Subscription
	err = json.Unmarshal(body, &subscription)
	if err != nil {
		f.logger.Error(err.Error())
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	subscriptionID := request.PathValue(PathSegmentSubscriptionID)
	f.cache.SetSubscription(subscriptionID, &subscription)

	var doc *database.SubscriptionDocument
	doc, err = f.dbClient.GetSubscriptionDoc(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			f.logger.Info(fmt.Sprintf("existing document not found for subscription - creating one for %s", subscriptionID))
			doc = &database.SubscriptionDocument{
				ID:           uuid.New().String(),
				PartitionKey: subscriptionID,
				Subscription: &subscription,
			}
		} else {
			f.logger.Error("failed to fetch document for %s: %v", subscriptionID, err)
			arm.WriteInternalServerError(writer)
			return
		}
	} else {
		f.logger.Info(fmt.Sprintf("existing document found for subscription - will update document for subscription %s", subscriptionID))
		doc.Subscription = &subscription

		messages := getSubscriptionDifferences(doc.Subscription, &subscription)
		for _, message := range messages {
			f.logger.Info(message)
		}
	}

	err = f.dbClient.SetSubscriptionDoc(ctx, doc)
	if err != nil {
		f.logger.Error("failed to create document for subscription %s: %v", subscriptionID, err)
	}

	f.metrics.EmitGauge("subscription_lifecycle", 1, map[string]string{
		"region":         f.region,
		"subscriptionid": subscriptionID,
		"state":          string(subscription.State),
	})

	resp, err := json.Marshal(subscription)
	if err != nil {
		f.logger.Error(err.Error())
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, err = writer.Write(resp)
	if err != nil {
		f.logger.Error(err.Error())
	}

	writer.WriteHeader(http.StatusCreated)
}

func (f *Frontend) ArmDeploymentPreflight(writer http.ResponseWriter, request *http.Request) {
	var subscriptionID string = request.PathValue(PathSegmentSubscriptionID)
	var resourceGroup string = request.PathValue(PathSegmentResourceGroupName)
	var apiVersion string = request.URL.Query().Get("api-version")

	ctx := request.Context()

	f.logger.Info(fmt.Sprintf("%s: ArmDeploymentPreflight", apiVersion))

	body, err := BodyFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	deploymentPreflight, cloudError := arm.UnmarshalDeploymentPreflight(body)
	if cloudError != nil {
		f.logger.Error(cloudError.Error())
		arm.WriteCloudError(writer, cloudError)
		return
	}

	validate := api.NewValidator()
	preflightErrors := []arm.CloudErrorBody{}

	for index, raw := range deploymentPreflight.Resources {
		resource := &arm.DeploymentPreflightResource{}
		err = json.Unmarshal(raw, resource)
		if err != nil {
			cloudError = arm.NewUnmarshalCloudError(err)
			// Preflight is best-effort: a malformed resource is not a validation failure.
			f.logger.Warn(cloudError.Message)
		}

		// This is just "preliminary" validation to ensure all the base resource
		// fields are present and the API version is valid.
		resourceErrors := api.ValidateRequest(validate, request.Method, resource)
		if len(resourceErrors) > 0 {
			// Preflight is best-effort: a malformed resource is not a validation failure.
			f.logger.Warn(
				fmt.Sprintf("Resource #%d failed preliminary validation (see details)", index+1),
				"details", resourceErrors)
			continue
		}

		// API version is already validated by this point.
		versionedInterface, _ := api.Lookup(resource.APIVersion)
		versionedCluster := versionedInterface.NewHCPOpenShiftCluster(nil)

		err = json.Unmarshal(raw, versionedCluster)
		if err != nil {
			// Preflight is best effort: failure to parse a resource is not a validation failure.
			f.logger.Warn(fmt.Sprintf("Failed to unmarshal %s resource named '%s': %s", resource.Type, resource.Name, err))
			continue
		}

		// Perform static validation as if for a cluster creation request.
		cloudError := versionedCluster.ValidateStatic(versionedCluster, false, http.MethodPut)
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
			if len(cloudError.CloudErrorBody.Details) > 0 {
				details = cloudError.CloudErrorBody.Details
			} else {
				details = []arm.CloudErrorBody{*cloudError.CloudErrorBody}
			}
			preflightErrors = append(preflightErrors, arm.CloudErrorBody{
				Code:    cloudError.Code,
				Message: fmt.Sprintf("Content validation failed for '%s'", resource.Name),
				Target:  resource.ResourceID(subscriptionID, resourceGroup),
				Details: details,
			})
			continue
		}

		// FIXME Further preflight steps go here.
	}

	arm.WriteDeploymentPreflightResponse(writer, preflightErrors)
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
