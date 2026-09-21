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

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	otelaudit "github.com/microsoft/go-otel-audit/audit"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"

	sdk "github.com/openshift-online/ocm-sdk-go"

	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	auditclient "github.com/Azure/ARO-HCP/internal/audit/otelaudit"
	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/signal"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
)

type FrontendOpts struct {
	auditLogQueueSize        int
	auditConnectSocket       bool
	auditServiceTreeID       string
	parsedAuditServiceTreeID uuid.UUID

	clustersServiceURL string
	insecure           bool

	location    string
	metricsPort int
	port        int

	cosmosName string
	cosmosURL  string

	exitOnPanic  bool
	logVerbosity int
}

func NewRootCmd() *cobra.Command {
	opts := NewFrontendOpts()
	rootCmd := &cobra.Command{
		Use:     "aro-hcp-frontend",
		Version: version.CommitSHA,
		Args:    cobra.NoArgs,
		Short:   "Serve the ARO HCP Frontend",
		Long: `Serve the ARO HCP Frontend

	This command runs the ARO HCP Frontend. It communicates with Clusters Service and a CosmosDB

	# Run ARO HCP Frontend locally to connect to a local Clusters Service at http://localhost:8000
	./aro-hcp-frontend --cosmos-name ${DB_NAME} --cosmos-url ${DB_URL} --location ${LOCATION} \
		--clusters-service-url "http://localhost:8000"
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run()
		},
	}

	rootCmd.Flags().IntVar(&opts.auditLogQueueSize, "audit-log-queue-size", 2048, "Log Queue size for audit logging client")
	rootCmd.Flags().BoolVar(&opts.auditConnectSocket, "audit-connect-socket", os.Getenv("AUDIT_CONNECT_SOCKET") == "true", "Connect to mdsd audit socket instead")
	rootCmd.Flags().StringVar(&opts.auditServiceTreeID, "audit-service-tree-id", os.Getenv("AUDIT_SERVICE_TREE_ID"), "Service Tree UUID for audit logging; zero UUID is allowed only when forwarding is disabled")

	rootCmd.Flags().StringVar(&opts.cosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&opts.cosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database URL")
	rootCmd.Flags().StringVar(&opts.location, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().IntVar(&opts.port, "port", 8443, "port to listen on")
	rootCmd.Flags().IntVar(&opts.metricsPort, "metrics-port", 8081, "port to serve metrics on")

	rootCmd.Flags().StringVar(&opts.clustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway.")
	rootCmd.Flags().BoolVar(&opts.insecure, "insecure", false, "Skip validating TLS for clusters-service.")

	rootCmd.Flags().BoolVar(&opts.exitOnPanic, "exit-on-panic", opts.exitOnPanic,
		"If set, frontend will exit the process if a panic occurs. As of now it only controls the setting of k8s.io/apimachinery/pkg/util/runtime.ReallyCrash",
	)
	rootCmd.Flags().IntVar(&opts.logVerbosity, "log-verbosity", opts.logVerbosity,
		"Log verbosity, as a go-logr/logr log level. 0 is the default verbosity level (INFO). It must be a value >= 0, where a higher value means more verbose output.",
	)
	rootCmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	return rootCmd
}

func NewFrontendOpts() *FrontendOpts {
	return &FrontendOpts{
		exitOnPanic: true,
	}
}

func (opts *FrontendOpts) Validate() error {
	if len(opts.location) == 0 {
		return utils.TrackError(fmt.Errorf("--location is required"))
	}

	if opts.logVerbosity < 0 {
		return utils.TrackError(fmt.Errorf("--log-verbosity must be a value >= 0"))
	}

	serviceTreeID, err := uuid.Parse(opts.auditServiceTreeID)
	if err != nil {
		return utils.TrackError(fmt.Errorf("--audit-service-tree-id must be a UUID: %w", err))
	}
	if opts.auditConnectSocket && serviceTreeID == uuid.Nil {
		return utils.TrackError(fmt.Errorf("--audit-service-tree-id must be nonzero when audit forwarding is enabled"))
	}
	opts.parsedAuditServiceTreeID = serviceTreeID

	return nil
}

func (opts *FrontendOpts) Run() error {
	err := opts.Validate()
	if err != nil {
		return utils.TrackError(fmt.Errorf("flags validation failed: %w", err))
	}

	ctx := signal.SetupSignalContext()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("function returned"))

	// Create a logr.Logger and add it to context for use throughout the application.
	// We use slog.Level(opts.LogVerbosity * -1) to convert the verbosity level to a slog.Level.
	// A value of 0 is equivalent to INFO. Higher values mean more verbose output.
	handlerOptions := &slog.HandlerOptions{Level: slog.Level(opts.logVerbosity * -1), AddSource: true}
	slogJSONHandler := slog.NewJSONHandler(os.Stderr, handlerOptions)
	logger := logr.FromSlogHandler(slogJSONHandler)
	ctx = utils.ContextWithLogger(ctx, logger)

	logger.Info(fmt.Sprintf(
		"%s (%s) started in %s",
		frontend.ProgramName,
		version.CommitSHA,
		opts.location))

	// Create an slog logger for external dependencies that require it
	slogLogger := slog.New(logr.ToSlogHandler(logger))

	// Create audit log client.
	auditClient, err := auditclient.NewOtelAuditClient(
		ctx,
		opts.auditConnectSocket,
		opts.parsedAuditServiceTreeID,
		legacyregistry.Registerer(),
		otelaudit.WithLogger(slogLogger),
		otelaudit.WithQueueSize(opts.auditLogQueueSize),
	)
	if err != nil {
		return fmt.Errorf("failed to create audit client: %w", err)
	}

	// Initialize the global OpenTelemetry tracer.
	otelShutdown, err := tracing.ConfigureOpenTelemetryTracer(
		ctx,
		logger,
		semconv.CloudRegion(opts.location),
		semconv.ServiceNameKey.String(frontend.ProgramName),
		semconv.ServiceVersionKey.String(version.CommitSHA),
	)
	if err != nil {
		return fmt.Errorf("could not initialize opentelemetry sdk: %w", err)
	}

	// Create the database client.
	clientOpts := azsdk.NewClientOptions(azsdk.ComponentFrontend)
	// FIXME Cloud should be determined by other means.
	clientOpts.Cloud = cloud.AzurePublic
	clientOpts.PerCallPolicies = []policy.Policy{frontend.PolicyFunc(frontend.CorrelationIDPolicy)}
	clientOpts.TracingProvider = azotel.NewTracingProvider(otel.GetTracerProvider(), nil)
	cosmosDatabaseClient, err := corecosmosstorage.NewCosmosDatabaseClient(
		opts.cosmosURL,
		opts.cosmosName,
		clientOpts,
	)
	if err != nil {
		return fmt.Errorf("failed to create the CosmosDB client: %w", err)
	}

	resourcesDBClient, err := corecosmosstorage.NewResourcesDBClient(cosmosDatabaseClient)
	if err != nil {
		return fmt.Errorf("failed to create the resources database client: %w", err)
	}

	listener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.port))
	if err != nil {
		return err
	}

	metricsListener, err := net.Listen("tcp4", fmt.Sprintf(":%d", opts.metricsPort))
	if err != nil {
		return err
	}

	// Initialize the Clusters Service Client.
	conn, err := sdk.NewUnauthenticatedConnectionBuilder().
		TransportWrapper(func(r http.RoundTripper) http.RoundTripper {
			return otelhttp.NewTransport(
				frontend.RequestIDPropagator(r),
			)
		}).
		URL(opts.clustersServiceURL).
		Insecure(opts.insecure).
		MetricsSubsystem("frontend_clusters_service_client").
		MetricsRegisterer(legacyregistry.Registerer()).
		Build()
	if err != nil {
		return err
	}

	csClient := ocm.NewClusterServiceClientWithTracing(
		ocm.NewClusterServiceClient(conn),
		utils.TracerName,
	)

	f := frontend.NewFrontend(
		logger, listener, metricsListener,
		legacyregistry.Registerer(), legacyregistry.DefaultGatherer,
		resourcesDBClient, csClient, auditClient, opts.location, opts.exitOnPanic,
	)

	runErrCh := make(chan error, 1)
	go func() {
		defer utilruntime.HandleCrash()
		runErrCh <- f.Run(ctx)
		cancel(fmt.Errorf("frontend exited"))
	}()

	<-ctx.Done()
	logger.Info("context closed")

	_ = otelShutdown(ctx)
	logger.Info(fmt.Sprintf("%s (%s) stopped", frontend.ProgramName, version.CommitSHA))

	logger.Info("waiting for run to finish")
	runErr := <-runErrCh
	return runErr
}
