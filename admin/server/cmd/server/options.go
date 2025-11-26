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

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-kusto-go/kusto"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	sdk "github.com/openshift-online/ocm-sdk-go"

	"github.com/Azure/ARO-HCP/admin/server/handlers"
	"github.com/Azure/ARO-HCP/admin/server/handlers/hcp"
	"github.com/Azure/ARO-HCP/admin/server/interrupts"
	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

func DefaultOptions() *RawOptions {
	return &RawOptions{
		Port:       8443,
		HealthPort: 8444,
	}
}

// RawOptions holds input values.
type RawOptions struct {
	Port               int
	HealthPort         int
	Location           string
	ClustersServiceURL string
	CosmosURL          string
	CosmosName         string
	KustoEndpoint      string
}

func (opts *RawOptions) BindOptions(cmd *cobra.Command) error {
	cmd.Flags().IntVar(&opts.Port, "port", opts.Port, "Port to serve content on.")
	cmd.Flags().IntVar(&opts.HealthPort, "health-port", opts.HealthPort, "Port to serve health and readiness on.")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location to serve content on.")
	cmd.Flags().StringVar(&opts.ClustersServiceURL, "clusters-service-url", getEnv("CLUSTERS_SERVICE_URL", opts.ClustersServiceURL), "URL of the Clusters Service.")
	cmd.Flags().StringVar(&opts.CosmosURL, "cosmos-url", getEnv("COSMOS_URL", opts.CosmosURL), "URL of the Cosmos DB.")
	cmd.Flags().StringVar(&opts.CosmosName, "cosmos-name", getEnv("COSMOS_NAME", opts.CosmosName), "Name of the Cosmos DB.")
	cmd.Flags().StringVar(&opts.KustoEndpoint, "kusto-endpoint", getEnv("KUSTO_ENDPOINT", opts.KustoEndpoint), "Endpoint of the Kusto cluster.")
	return nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// validatedOptions is a private wrapper that enforces a call of Validate() before Complete() can be invoked.
type validatedOptions struct {
	*RawOptions
}

type ValidatedOptions struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*validatedOptions
}

// completedOptions is a private wrapper that enforces a call of Complete() before config generation can be invoked.
type completedOptions struct {
	Port                  int
	HealthPort            int
	Location              string
	ClustersServiceClient *ocm.ClusterServiceClientSpec
	DbClient              *database.DBClient
	KustoClient           *kusto.Client
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
	if o.Location == "" {
		return nil, fmt.Errorf("location is required")
	}
	if o.ClustersServiceURL == "" {
		return nil, fmt.Errorf("clusters-service-url is required")
	}
	if o.CosmosURL == "" {
		return nil, fmt.Errorf("cosmos-url is required")
	}
	if o.CosmosName == "" {
		return nil, fmt.Errorf("cosmos-name is required")
	}
	return &ValidatedOptions{
		validatedOptions: &validatedOptions{
			RawOptions: o,
		},
	}, nil
}

func (o *ValidatedOptions) Complete(ctx context.Context) (*Options, error) {
	// Create CS client
	csConnection, err := sdk.NewUnauthenticatedConnectionBuilder().
		URL(o.ClustersServiceURL).
		Insecure(true).
		MetricsSubsystem("adminapi_clusters_service_client").
		MetricsRegisterer(prometheus.DefaultRegisterer).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create Clusters Service client: %w", err)
	}
	csClient := ocm.NewClusterServiceClient(csConnection, "", false, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Clusters Service client: %w", err)
	}

	// Create the database client.
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		o.CosmosURL,
		o.CosmosName,
		azcore.ClientOptions{
			// FIXME Cloud should be determined by other means.
			Cloud: cloud.AzurePublic,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create the CosmosDB client: %w", err)
	}
	dbClient, err := database.NewDBClient(ctx, cosmosDatabaseClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create the database client: %w", err)
	}

	// Create Kusto client
	var kustoClient *kusto.Client
	if o.KustoEndpoint != "" {
		kustoConnectionStringBuilder := kusto.NewConnectionStringBuilder(o.KustoEndpoint).WithDefaultAzureCredential()
		client, err := kusto.New(kustoConnectionStringBuilder)
		if err != nil {
			return nil, fmt.Errorf("failed to create the Kusto client: %w", err)
		}
		kustoClient = client
	}

	return &Options{
		completedOptions: &completedOptions{
			Port:                  o.Port,
			HealthPort:            o.HealthPort,
			Location:              o.Location,
			ClustersServiceClient: &csClient,
			DbClient:              &dbClient,
			KustoClient:           kustoClient,
		},
	}, nil
}

func (opts *Options) Run(ctx context.Context) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	logger.Info("Reporting health.", "port", opts.HealthPort)
	health := NewHealthOnPort(logger, opts.HealthPort)
	health.ServeReady(func() bool {
		// todo: add real readiness checks
		return true
	})

	logger.Info("Running server", "port", opts.Port)

	// Submux for V1 HCP endpoints
	v1HCPMux := middleware.NewHCPResourceServerMux()
	v1HCPMux.Handle("GET", "/helloworld", hcp.HCPHelloWorld())

	// Submux for /admin
	adminMux := http.NewServeMux()
	adminMux.Handle("GET /helloworld", handlers.HelloWorldHandler())
	adminMux.Handle("/v1/hcp/", http.StripPrefix("/v1/hcp", v1HCPMux.Handler()))

	rootMux := http.NewServeMux()
	rootMux.Handle("/admin/", http.StripPrefix("/admin", adminMux))

	s := http.Server{
		Addr:    net.JoinHostPort("", strconv.Itoa(opts.Port)),
		Handler: middleware.WithClientPrincipal(middleware.WithLowercaseURLPathValue(middleware.WithLogger(logger, rootMux))),
	}
	interrupts.ListenAndServe(&s, 5*time.Second)
	interrupts.WaitForGracefulShutdown()
	return nil
}
