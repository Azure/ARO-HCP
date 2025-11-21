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
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	sdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/admin/server/handlers"
	"github.com/Azure/ARO-HCP/admin/server/interrupts"
	"github.com/Azure/ARO-HCP/admin/server/inventory"
	"github.com/Azure/ARO-HCP/admin/server/middleware"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-Tools/pkg/cmdutils"
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
}

func (opts *RawOptions) BindOptions(cmd *cobra.Command) error {
	cmd.Flags().IntVar(&opts.Port, "port", opts.Port, "Port to serve content on.")
	cmd.Flags().IntVar(&opts.HealthPort, "health-port", opts.HealthPort, "Port to serve health and readiness on.")
	cmd.Flags().StringVar(&opts.Location, "location", opts.Location, "Location to serve content on.")
	cmd.Flags().StringVar(&opts.ClustersServiceURL, "clusters-service-url", opts.ClustersServiceURL, "URL of the Clusters Service.")
	cmd.Flags().StringVar(&opts.CosmosURL, "cosmos-url", opts.CosmosURL, "URL of the Cosmos DB.")
	cmd.Flags().StringVar(&opts.CosmosName, "cosmos-name", opts.CosmosName, "Name of the Cosmos DB.")
	return nil
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
	ClustersServiceClient ocm.ClusterServiceClientSpec
	DbClient              database.DBClient
	MgmtClusterInventory  *inventory.MgmtClusterInventory
}

type Options struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedOptions
}

func (o *RawOptions) Validate() (*ValidatedOptions, error) {
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

	// Get Azure credentials
	azureCredential, err := cmdutils.GetAzureTokenCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain Azure credentials: %w", err)
	}

	// Create management cluster discovery
	mgmtClusterInventory := inventory.NewMgmtClusterInventory(
		inventory.NewGraphMgmtClusterInventoryBackend(o.Location, azureCredential),
	)
	return &Options{
		completedOptions: &completedOptions{
			Port:                  o.Port,
			HealthPort:            o.HealthPort,
			Location:              o.Location,
			ClustersServiceClient: csClient,
			DbClient:              dbClient,
			MgmtClusterInventory:  mgmtClusterInventory,
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
	mux := http.NewServeMux()
	mux.Handle(MuxPattern(http.MethodGet, "helloworld"), handlers.HelloWorldHandler())
	mux.Handle(
		MuxPatternHCP(http.MethodGet, "kubeconfig"),
		middleware.WithHCPResourceID(handlers.GetHCPKubeconfig(opts.ClustersServiceClient, opts.DbClient, opts.MgmtClusterInventory)),
	)

	s := http.Server{
		Addr:    net.JoinHostPort("", strconv.Itoa(opts.Port)),
		Handler: middleware.WithLowercaseURLPathValue(middleware.WithLogger(logger, mux)),
	}
	interrupts.ListenAndServe(&s, 5*time.Second)
	interrupts.WaitForGracefulShutdown()
	return nil
}

const (
	WildcardSubscriptionID    = "{" + middleware.PathSegmentSubscriptionID + "}"
	WildcardResourceGroupName = "{" + middleware.PathSegmentResourceGroupName + "}"
	WildcardResourceName      = "{" + middleware.PathSegmentResourceName + "}"

	PatternSubscriptions  = "subscriptions/" + WildcardSubscriptionID
	PatternResourceGroups = "resourcegroups/" + WildcardResourceGroupName
	PatternProviders      = "providers/" + api.ProviderNamespace
	PatternClusters       = api.ClusterResourceTypeName + "/" + WildcardResourceName
)

func MuxPatternHCP(method string, segments ...string) string {
	segments = append([]string{PatternSubscriptions, PatternResourceGroups, PatternProviders, PatternClusters}, segments...)
	return MuxPattern(method, segments...)
}

func MuxPattern(method string, segments ...string) string {
	return fmt.Sprintf("%s /admin/%s", method, strings.ToLower(path.Join(segments...)))
}
