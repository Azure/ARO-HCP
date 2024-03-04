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

	"github.com/Azure/ARO-HCP/pkg/api/arm"
)

const (
	// Literal path segments must be lowercase because
	// MiddlewareLowercase converts the request URL to
	// lowercase before multiplexing.
	ResourceProviderNamespace = "microsoft.redhatopenshift"

	SubscriptionPath  = "/subscriptions/{subscriptionId}"
	ResourceGroupPath = SubscriptionPath + "/resourcegroups/{resourceGroupName}"
	ResourceTypePath  = ResourceGroupPath + "/" + ResourceProviderNamespace + "/{resourceType}"
	ResourceNamePath  = ResourceTypePath + "/{resourceName}"
)

type Frontend struct {
	logger   *slog.Logger
	listener net.Listener
	server   http.Server
	done     chan struct{}
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
		MiddlewareBody,
		MiddlewareLowercase,
		MiddlewareSystemData)

	// Unauthenticated routes
	mux.HandleFunc("/", f.NotFound)

	// Authenticated routes
	// FIXME List post-multiplexing middleware like auth validation here.
	postMuxMiddleware := NewMiddleware()
	mux.Handle(
		http.MethodGet+" "+ResourceTypePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourceListByParent))
	mux.Handle(
		http.MethodGet+" "+ResourceNamePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourceRead))
	mux.Handle(
		http.MethodPut+" "+ResourceNamePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourceCreateOrUpdate))
	mux.Handle(
		http.MethodPatch+" "+ResourceNamePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourcePatch))
	mux.Handle(
		http.MethodDelete+" "+ResourceNamePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourceDelete))
	mux.Handle(
		http.MethodPost+" "+ResourceNamePath,
		postMuxMiddleware.HandlerFunc(f.ArmResourceAction))
	f.server.Handler = mux

	return f
}

func (f *Frontend) Run(ctx context.Context, stop <-chan struct{}) {
	if stop != nil {
		go func() {
			<-stop
			f.server.Shutdown(ctx)
		}()
	}

	f.logger.Info(fmt.Sprintf("listening on %s", f.listener.Addr().String()))

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

func (f *Frontend) NotFound(writer http.ResponseWriter, request *http.Request) {
	arm.WriteError(
		writer, http.StatusNotFound,
		arm.CloudErrorCodeNotFound, "",
		"The requested path could not be found.")
}

func (f *Frontend) ArmResourceListByParent(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourceListByParent")
}

func (f *Frontend) ArmResourceRead(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourceRead")
}

func (f *Frontend) ArmResourceCreateOrUpdate(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourceCreateOrUpdate")
}

func (f *Frontend) ArmResourcePatch(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourcePatch")
}

func (f *Frontend) ArmResourceDelete(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourceDelete")
}

func (f *Frontend) ArmResourceAction(writer http.ResponseWriter, request *http.Request) {
	logger := request.Context().Value(ContextKeyLogger).(*slog.Logger)
	logger.Info("ArmResourceAction")
}
