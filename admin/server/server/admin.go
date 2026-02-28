// Copyright 2026 Microsoft Corporation
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

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/utils/set"

	"github.com/Azure/azure-kusto-go/kusto"

	"github.com/Azure/ARO-HCP/admin/server/handlers"
	"github.com/Azure/ARO-HCP/admin/server/handlers/cosmosdump"
	"github.com/Azure/ARO-HCP/admin/server/handlers/hcp"
	breakglasshandlers "github.com/Azure/ARO-HCP/admin/server/handlers/hcp/breakglass"
	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/audit"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/errorutils"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/clientset/versioned/typed/sessiongate/v1alpha1"
	sessiongatelisterv1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/generated/listers/sessiongate/v1alpha1"
)

type AdminAPI struct {
	clustersServiceClient  ocm.ClusterServiceClientSpec
	dbClient               database.DBClient
	kustoClient            *kusto.Client
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever

	location string

	listener        net.Listener
	metricsListener net.Listener
	server          http.Server
	metricsServer   http.Server
}

func NewAdminAPI(
	logger logr.Logger,
	location string,
	listener net.Listener,
	metricsListener net.Listener,
	dbClient database.DBClient,
	clustersServiceClient ocm.ClusterServiceClientSpec,
	kustoClient *kusto.Client,
	fpaCredentialRetriever fpa.FirstPartyApplicationTokenCredentialRetriever,
	auditClient audit.Client,
	sessionClient sessiongatev1alpha1.SessionInterface,
	sessionLister sessiongatelisterv1alpha1.SessionNamespaceLister,
	minSessionTTL time.Duration,
	maxSessionTTL time.Duration,
	allowedBreakglassGroups set.Set[string],
) *AdminAPI {
	// Pre-mux middleware (runs on all admin routes before pattern matching)
	middlewareMux := middleware.NewMiddlewareMux(
		middleware.MiddlewareLogger,
		middleware.MiddlewareLowercase,
		middleware.NewMiddlewareAudit(auditClient).HandleRequest,
		middleware.MiddlewareClientPrincipal,
	)

	// HCP resource routes
	hcpMiddleware := middleware.NewMiddleware(
		middleware.MiddlewareHCPResourceID,
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("GET", "/helloworld"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(hcp.NewHCPHelloWorldHandler(dbClient, clustersServiceClient).ServeHTTP)),
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("GET", "/hellworld/lbs"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(hcp.NewHCPDemoListLoadbalancersHandler(dbClient, clustersServiceClient, fpaCredentialRetriever).ServeHTTP)),
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("POST", "/breakglass"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(breakglasshandlers.NewHCPBreakglassSessionCreationHandler(dbClient, clustersServiceClient, sessionClient, allowedBreakglassGroups, minSessionTTL, maxSessionTTL).ServeHTTP)),
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("GET", "/breakglass/{sessionName}/kubeconfig"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(breakglasshandlers.NewHCPBreakglassSessionKubeconfigHandler(sessionLister, sessionClient).ServeHTTP)),
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("GET", "/cosmosdump"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(cosmosdump.NewCosmosDumpHandler(dbClient).ServeHTTP)),
	)
	middlewareMux.Handle(
		middleware.V1HCPResourcePattern("GET", "/serialconsole"),
		hcpMiddleware.HandlerFunc(errorutils.ReportError(hcp.NewHCPSerialConsoleHandler(dbClient, clustersServiceClient, fpaCredentialRetriever).ServeHTTP)),
	)

	// Non-HCP admin routes
	middlewareMux.Handle("GET /admin/helloworld", handlers.HelloWorldHandler())

	// Top-level mux (healthz bypasses all middleware)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /healthz/ready", healthzReadyHandler)
	apiMux.HandleFunc("GET /healthz/live", healthzLiveHandler)
	apiMux.HandleFunc("/", middlewareMux.ServeHTTP)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.Handler())
	// keeping these handlers on the metrics mux/listener during the migration to the api mux/listener
	// remove once we can shift the deployment health checks to the other port
	metricsMux.HandleFunc("GET /healthz/ready", healthzReadyHandler)
	metricsMux.HandleFunc("GET /healthz/live", healthzLiveHandler)

	return &AdminAPI{
		location:        location,
		listener:        listener,
		metricsListener: metricsListener,
		server: http.Server{
			BaseContext: func(net.Listener) context.Context {
				ctx := context.Background()
				ctx = utils.ContextWithLogger(ctx, logger)
				return ctx
			},
			Handler: apiMux,
		},
		metricsServer: http.Server{
			BaseContext: func(net.Listener) context.Context {
				return utils.ContextWithLogger(context.Background(), logger)
			},
			Handler: metricsMux,
		},
		dbClient:               dbClient,
		clustersServiceClient:  clustersServiceClient,
		kustoClient:            kustoClient,
		fpaCredentialRetriever: fpaCredentialRetriever,
	}
}

func (a *AdminAPI) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(fmt.Errorf("run returned"))

		// always attempt a graceful shutdown, a double ctrl+c exits the process
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
		defer shutdownCancel()
		_ = a.server.Shutdown(shutdownCtx)
		_ = a.metricsServer.Shutdown(shutdownCtx)
	}()

	if len(a.location) == 0 {
		panic("location must be set")
	}

	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("listening on %s", a.listener.Addr().String()))
	logger.Info(fmt.Sprintf("metrics listening on %s", a.metricsListener.Addr().String()))

	errCh := make(chan error, 2)
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- a.server.Serve(a.listener)
	}()
	go func() {
		defer wg.Done()
		errCh <- a.metricsServer.Serve(a.metricsListener)
	}()

	<-ctx.Done()

	// always attempt a graceful shutdown, a double ctrl+c exits the process
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
	defer shutdownCancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "failed to shutdown http server")
	}
	if err := a.metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error(err, "failed to shutdown metrics server")
	}

	wg.Wait()
	close(errCh)
	errs := []error{}
	for err := range errCh {
		if err != nil {
			logger.Info("go func completed", "message", err.Error())
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func healthzReadyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func healthzLiveHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("live"))
}
