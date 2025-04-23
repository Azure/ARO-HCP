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
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"
	sdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"

	"github.com/Azure/ARO-HCP/frontend/pkg/frontend"
	"github.com/Azure/ARO-HCP/frontend/pkg/util"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type FrontendOpts struct {
	clustersServiceURL            string
	clusterServiceProvisionShard  string
	clusterServiceNoopProvision   bool
	clusterServiceNoopDeprovision bool
	insecure                      bool

	location    string
	metricsPort int
	port        int

	cosmosName string
	cosmosURL  string
}

func NewRootCmd() *cobra.Command {
	opts := &FrontendOpts{}
	rootCmd := &cobra.Command{
		Use:     "aro-hcp-frontend",
		Version: util.Version(),
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

	rootCmd.Flags().StringVar(&opts.cosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&opts.cosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database URL")
	rootCmd.Flags().StringVar(&opts.location, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().IntVar(&opts.port, "port", 8443, "port to listen on")
	rootCmd.Flags().IntVar(&opts.metricsPort, "metrics-port", 8081, "port to serve metrics on")

	rootCmd.Flags().StringVar(&opts.clustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway.")
	rootCmd.Flags().BoolVar(&opts.insecure, "insecure", false, "Skip validating TLS for clusters-service.")
	rootCmd.Flags().StringVar(&opts.clusterServiceProvisionShard, "cluster-service-provision-shard", "", "Manually specify provision shard for all requests to cluster service")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopProvision, "cluster-service-noop-provision", false, "Skip cluster service provisioning steps for development purposes")
	rootCmd.Flags().BoolVar(&opts.clusterServiceNoopDeprovision, "cluster-service-noop-deprovision", false, "Skip cluster service deprovisioning steps for development purposes")

	rootCmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	return rootCmd
}

type policyFunc func(*policy.Request) (*http.Response, error)

func (pf policyFunc) Do(req *policy.Request) (*http.Response, error) {
	return pf(req)
}

// Verify that policyFunc implements the policy.Policy interface.
var _ policy.Policy = policyFunc(nil)

// correlationIDPolicy adds the ARM correlation request ID to the request's
// HTTP headers if the ID is found in the context.
func correlationIDPolicy(req *policy.Request) (*http.Response, error) {
	cd, err := frontend.CorrelationDataFromContext(req.Raw().Context())
	// The incoming request may not contain a correlation request ID (e.g.
	// requests to /healthz).
	if err == nil && cd.CorrelationRequestID != "" {
		req.Raw().Header.Set(arm.HeaderNameCorrelationRequestID, cd.CorrelationRequestID)
	}

	return req.Next()
}

func (opts *FrontendOpts) Run() error {
	ctx := context.Background()

	logger := util.DefaultLogger()
	logger.Info(fmt.Sprintf("%s (%s) started", frontend.ProgramName, util.Version()))

	// Initialize the global OpenTelemetry tracer.
	otelShutdown, err := frontend.ConfigureOpenTelemetryTracer(ctx, logger, semconv.CloudRegion(opts.location))
	if err != nil {
		return fmt.Errorf("could not initialize opentelemetry sdk: %w", err)
	}

	// Create the database client.
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		opts.cosmosURL,
		opts.cosmosName,
		azcore.ClientOptions{
			// FIXME Cloud should be determined by other means.
			Cloud:           cloud.AzurePublic,
			PerCallPolicies: []policy.Policy{policyFunc(correlationIDPolicy)},
			TracingProvider: azotel.NewTracingProvider(otel.GetTracerProvider(), nil),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create the CosmosDB client: %w", err)
	}

	dbClient, err := database.NewDBClient(ctx, cosmosDatabaseClient)
	if err != nil {
		return fmt.Errorf("failed to create the database client: %w", err)
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
		MetricsRegisterer(prometheus.DefaultRegisterer).
		Build()
	if err != nil {
		return err
	}

	csClient := ocm.ClusterServiceClient{
		Conn:                       conn,
		ProvisionerNoOpProvision:   opts.clusterServiceNoopDeprovision,
		ProvisionerNoOpDeprovision: opts.clusterServiceNoopDeprovision,
	}

	if opts.clusterServiceProvisionShard != "" {
		csClient.ProvisionShardID = api.Ptr(opts.clusterServiceProvisionShard)
	}

	if len(opts.location) == 0 {
		return errors.New("location is required")
	}
	logger.Info(fmt.Sprintf("Application running in %s", opts.location))

	f := frontend.NewFrontend(logger, listener, metricsListener, prometheus.DefaultRegisterer, dbClient, opts.location, &csClient)

	stop := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	go f.Run(ctx, stop)

	sig := <-signalChannel
	logger.Info(fmt.Sprintf("caught %s signal", sig))
	close(stop)

	f.Join()
	_ = otelShutdown(ctx)
	logger.Info(fmt.Sprintf("%s (%s) stopped", frontend.ProgramName, util.Version()))

	return nil
}
