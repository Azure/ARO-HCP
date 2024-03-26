package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
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
	ready    atomic.Value
	done     chan struct{}
}

// MuxPattern forms a URL pattern suitable for passing to http.ServeMux.
// Literal path segments must be lowercase because MiddlewareLowercase
// converts the request URL to lowercase before multiplexing.
func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /%s", method, strings.ToLower(path.Join(segments...)))
}

func NewFrontend(logger *slog.Logger, listener net.Listener) *Frontend {
	f := &Frontend{
		logger:   logger,
		listener: listener,
		server: http.Server{
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
			BaseContext: func(net.Listener) context.Context {
				return context.WithValue(context.Background(), ContextKeyLogger, logger)
			},
		},
		done: make(chan struct{}),
	}

	mux := NewMiddlewareMux(
		MiddlewarePanic,
		MiddlewareLogging,
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)
	mux.HandleFunc(MuxPattern(http.MethodGet, "healthz", "ready"), f.HealthzReady)

	// Authenticated routes
	postMuxMiddleware := NewMiddleware(
		MiddlewareLoggingPostMux,
		MiddlewareValidateAPIVersion)
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
	if f.CheckReady() {
		writer.WriteHeader(http.StatusOK)
	} else {
		writer.WriteHeader(http.StatusInternalServerError)
	}
}

func (f *Frontend) ArmResourceListBySubscription(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceListBySubscription", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceListByLocation(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceListByLocation", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceListByResourceGroup(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceListByResourceGroup", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceRead(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceRead", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}

func (f *Frontend) ArmResourceCreateOrUpdate(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceCreateOrUpdate", versionedInterface))

	writer.WriteHeader(http.StatusCreated)
}

func (f *Frontend) ArmResourcePatch(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourcePatch", versionedInterface))

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmResourceDelete(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceDelete", versionedInterface))

	writer.WriteHeader(http.StatusAccepted)
}

func (f *Frontend) ArmResourceAction(writer http.ResponseWriter, request *http.Request) {
	ctx := request.Context()
	logger := ctx.Value(ContextKeyLogger).(*slog.Logger)
	versionedInterface := ctx.Value(ContextKeyVersion).(api.Version)

	logger.Info(fmt.Sprintf("%s: ArmResourceAction", versionedInterface))

	writer.WriteHeader(http.StatusOK)
}
