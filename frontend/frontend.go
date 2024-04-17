package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	PatternSubscriptions  = "subscriptions/{" + PathSegmentSubscriptionID + "}"
	PatternLocations      = "locations/{" + PageSegmentLocation + "}"
	PatternProviders      = "providers/" + api.ProviderNamespace + "/" + api.ResourceType
	PatternResourceGroups = "resourcegroups/{" + PathSegmentResourceGroupName + "}"
	PatternResourceName   = "{" + PathSegmentResourceName + "}"
	PatternActionName     = "{" + PathSegmentActionName + "}"
)

type Frontend struct {
	logger   *slog.Logger
	listener net.Listener
	server   http.Server
	cache    Cache
	ready    atomic.Value
	done     chan struct{}
	metrics  metrics.Emitter
}

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

func NewFrontend(logger *slog.Logger, listener net.Listener, emitter metrics.Emitter) *Frontend {
	f := &Frontend{
		logger:   logger,
		listener: listener,
		metrics:  emitter,
		server: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return ContextWithLogger(context.Background(), logger)
			},
		},
		cache: *NewCache(),
		done:  make(chan struct{}),
	}

	subscriptionStateMuxValidator := NewSubscriptionStateMuxValidator(&f.cache)

	// Setup metrics middleware
	metricsMiddleware := MetricsMiddleware{Emitter: emitter}

	mux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareLogging,
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData,
		metricsMiddleware.Metrics(),
	)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz", "ready"), f.HealthzReady)
	// TODO: determine where in the auth chain we should allow for this endpoint to be called by ARM
	mux.HandleFunc(MuxPattern(http.MethodPut, PatternSubscriptions), f.ArmSubscriptionAction)

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
		postMuxMiddleware.HandlerFunc(f.ArmResourcePatch))
	mux.Handle(
		MuxPattern(http.MethodDelete, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		MuxPattern(http.MethodPost, PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternResourceName, PatternActionName),
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))

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
	cluster, updating := f.cache.GetCluster(resourceID)

	versionedRequestCluster := versionedInterface.NewHCPOpenShiftCluster(nil)
	versionedCurrentCluster := versionedInterface.NewHCPOpenShiftCluster(cluster)

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
	writer.WriteHeader(http.StatusCreated)
}

func (f *Frontend) ArmResourcePatch(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()

	versionedInterface, err := VersionFromContext(ctx)
	if err != nil {
		f.logger.Error(err.Error())
		arm.WriteInternalServerError(writer)
		return
	}

	f.logger.Info(fmt.Sprintf("%s: ArmResourcePatch", versionedInterface))

	writer.WriteHeader(http.StatusAccepted)
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
	_, found := f.cache.GetCluster(resourceID)
	if !found {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	f.cache.DeleteCluster(resourceID)

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

func (f *Frontend) ArmSubscriptionAction(writer http.ResponseWriter, request *http.Request) {
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

	subId := request.PathValue(PathSegmentSubscriptionID)
	f.cache.SetSubscription(subId, &subscription)

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
}
