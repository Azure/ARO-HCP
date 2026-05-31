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

package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	maestroopenapi "github.com/openshift-online/maestro/pkg/api/openapi"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/maestroregistration"
	"github.com/Azure/ARO-HCP/fleet/pkg/manager"
	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

const (
	defaultHealthzListenAddress = ":8080"
	defaultMetricsListenAddress = ":8081"
	defaultLeaderElectionID     = "fleet-controller"
)

type RawControllerOptions struct {
	CosmosURL  string
	CosmosName string

	ClustersServiceURL         string
	ClustersServiceTLSInsecure bool

	MaestroTLSInsecure bool

	CloudEnvironment string
	Region           string

	KubeNamespace        string
	LeaderElectionID     string
	HealthzListenAddress string
	MetricsListenAddress string
}

func DefaultControllerOptions() *RawControllerOptions {
	return &RawControllerOptions{
		HealthzListenAddress: defaultHealthzListenAddress,
		MetricsListenAddress: defaultMetricsListenAddress,
		LeaderElectionID:     defaultLeaderElectionID,
	}
}

func BindControllerOptions(opts *RawControllerOptions, cmd *cobra.Command) error {
	cmd.Flags().StringVar(&opts.CosmosURL, "cosmos-url", opts.CosmosURL, "CosmosDB endpoint URL")
	cmd.Flags().StringVar(&opts.CosmosName, "cosmos-name", opts.CosmosName, "CosmosDB database name")
	cmd.Flags().StringVar(&opts.CloudEnvironment, "cloud-environment", opts.CloudEnvironment, "Azure cloud environment (AzurePublicCloud, AzureChinaCloud, AzureUSGovernmentCloud)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Azure region")
	cmd.Flags().StringVar(&opts.ClustersServiceURL, "clusters-service-url", opts.ClustersServiceURL, "URL of the ClustersService API")
	cmd.Flags().BoolVar(&opts.ClustersServiceTLSInsecure, "clusters-service-tls-insecure", opts.ClustersServiceTLSInsecure, "skip TLS verification for ClustersService")
	cmd.Flags().BoolVar(&opts.MaestroTLSInsecure, "maestro-tls-insecure", opts.MaestroTLSInsecure, "skip TLS verification for Maestro")
	cmd.Flags().StringVar(&opts.KubeNamespace, "kube-namespace", opts.KubeNamespace, "Kubernetes namespace for leader election lease")
	cmd.Flags().StringVar(&opts.LeaderElectionID, "leader-election-id", opts.LeaderElectionID, "name of the leader election lease")
	cmd.Flags().StringVar(&opts.HealthzListenAddress, "healthz-listen-address", opts.HealthzListenAddress, "listen address for healthz server")
	cmd.Flags().StringVar(&opts.MetricsListenAddress, "metrics-listen-address", opts.MetricsListenAddress, "listen address for metrics server")

	for _, flag := range []string{
		"cloud-environment",
		"region",
		"cosmos-url",
		"cosmos-name",
		"clusters-service-url",
		"kube-namespace",
	} {
		if err := cmd.MarkFlagRequired(flag); err != nil {
			return err
		}
	}

	return nil
}

type validatedControllerOptions struct {
	*RawControllerOptions
	cloudConfiguration cloud.Configuration
}

type ValidatedControllerOptions struct {
	*validatedControllerOptions
}

func (o *RawControllerOptions) Validate(ctx context.Context) (*ValidatedControllerOptions, error) {
	if len(o.CosmosURL) == 0 {
		return nil, fmt.Errorf("--cosmos-url is required")
	}
	if len(o.CosmosName) == 0 {
		return nil, fmt.Errorf("--cosmos-name is required")
	}
	if len(o.Region) == 0 {
		return nil, fmt.Errorf("--region is required")
	}
	if len(o.ClustersServiceURL) == 0 {
		return nil, fmt.Errorf("--clusters-service-url is required")
	}
	if len(o.KubeNamespace) == 0 {
		return nil, fmt.Errorf("--kube-namespace is required")
	}
	if len(o.LeaderElectionID) == 0 {
		return nil, fmt.Errorf("--leader-election-id is required")
	}
	cloudConfig, err := azsdk.CloudConfigurationFromName(o.CloudEnvironment)
	if err != nil {
		return nil, fmt.Errorf("--cloud-environment: %w", err)
	}

	return &ValidatedControllerOptions{
		validatedControllerOptions: &validatedControllerOptions{
			RawControllerOptions: o,
			cloudConfiguration:   cloudConfig,
		},
	}, nil
}

type controllerOptions struct {
	fleetDBClient                database.FleetDBClient
	clustersServiceClient        ocm.ClusterServiceClientSpec
	maestroConsumerClientFactory maestroregistration.MaestroConsumerClientFactory
	leaderElectionLock           resourcelock.Interface
	region                       string
	healthzListenAddr            string
	metricsListenAddr            string
}

type ControllerOptions struct {
	*controllerOptions
}

func (o *ValidatedControllerOptions) Complete(ctx context.Context) (*ControllerOptions, error) {
	clientOpts := azsdk.NewClientOptions(azsdk.ComponentFleet)
	clientOpts.Cloud = o.cloudConfiguration

	dbClient, err := database.NewCosmosDatabaseClient(o.CosmosURL, o.CosmosName, clientOpts)
	if err != nil {
		return nil, err
	}

	fleetDBClient, err := database.NewFleetDBClient(dbClient)
	if err != nil {
		return nil, err
	}

	clustersServiceClient, err := newClustersServiceClient(o.ClustersServiceURL, o.ClustersServiceTLSInsecure)
	if err != nil {
		return nil, err
	}

	maestroConsumerClientFactory := newMaestroConsumerClientFactory(o.MaestroTLSInsecure)

	kubeconfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster kubeconfig: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	leaderElectionLock, err := manager.NewLeaderElectionLock(hostname, kubeconfig, o.KubeNamespace, o.LeaderElectionID)
	if err != nil {
		return nil, err
	}

	return &ControllerOptions{
		controllerOptions: &controllerOptions{
			fleetDBClient:                fleetDBClient,
			clustersServiceClient:        clustersServiceClient,
			maestroConsumerClientFactory: maestroConsumerClientFactory,
			leaderElectionLock:           leaderElectionLock,
			region:                       o.Region,
			healthzListenAddr:            o.HealthzListenAddress,
			metricsListenAddr:            o.MetricsListenAddress,
		},
	}, nil
}

func (o *ControllerOptions) Run(ctx context.Context) error {
	mgr := &manager.Manager{
		FleetDBClient:                o.fleetDBClient,
		ClustersServiceClient:        o.clustersServiceClient,
		MaestroConsumerClientFactory: o.maestroConsumerClientFactory,
		LeaderElectionLock:           o.leaderElectionLock,
		Region:                       o.region,
		HealthzListenAddr:            o.healthzListenAddr,
		MetricsListenAddr:            o.metricsListenAddr,
	}
	return mgr.Run(ctx)
}

type maestroConsumerClientFactory struct {
	httpClient *http.Client
}

func newMaestroConsumerClientFactory(tlsInsecure bool) maestroregistration.MaestroConsumerClientFactory {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: tlsInsecure,
	}
	return &maestroConsumerClientFactory{
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: otelhttp.NewTransport(transport),
		},
	}
}

func (f *maestroConsumerClientFactory) NewMaestroConsumerClient(maestroURL string) maestroregistration.MaestroConsumerClient {
	maestroConfig := &maestroopenapi.Configuration{
		Servers: maestroopenapi.ServerConfigurations{{
			URL: maestroURL,
		}},
		HTTPClient: f.httpClient,
	}
	apiClient := maestroopenapi.NewAPIClient(maestroConfig)
	return maestroregistration.NewMaestroConsumerClient(apiClient)
}

func newClustersServiceClient(url string, tlsInsecure bool) (ocm.ClusterServiceClientSpec, error) {
	conn, err := ocmsdk.NewUnauthenticatedConnectionBuilder().
		TransportWrapper(func(r http.RoundTripper) http.RoundTripper {
			return otelhttp.NewTransport(r)
		}).
		URL(url).
		Insecure(tlsInsecure).
		Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create OCM connection: %w", err)
	}
	return ocm.NewClusterServiceClient(conn), nil
}
