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
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"

	"github.com/Azure/ARO-HCP/fleet/pkg/manager"
	"github.com/Azure/ARO-HCP/internal/azsdk"
	"github.com/Azure/ARO-HCP/internal/database"
)

const (
	defaultHealthzListenAddress = ":8080"
	defaultMetricsListenAddress = ":8081"
	defaultLeaderElectionID     = "fleet-controller"
)

type RawControllerOptions struct {
	CosmosURL  string
	CosmosName string

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
	cmd.Flags().StringVar(&opts.KubeNamespace, "kube-namespace", opts.KubeNamespace, "Kubernetes namespace for leader election lease")
	cmd.Flags().StringVar(&opts.LeaderElectionID, "leader-election-id", opts.LeaderElectionID, "name of the leader election lease")
	cmd.Flags().StringVar(&opts.HealthzListenAddress, "healthz-listen-address", opts.HealthzListenAddress, "listen address for healthz server")
	cmd.Flags().StringVar(&opts.MetricsListenAddress, "metrics-listen-address", opts.MetricsListenAddress, "listen address for metrics server")

	for _, flag := range []string{
		"cloud-environment",
		"region",
		"cosmos-url",
		"cosmos-name",
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
	if len(o.KubeNamespace) == 0 {
		return nil, fmt.Errorf("--kube-namespace is required")
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
	fleetDBClient      database.FleetDBClient
	leaderElectionLock resourcelock.Interface
	healthzListenAddr  string
	metricsListenAddr  string
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
			fleetDBClient:      fleetDBClient,
			leaderElectionLock: leaderElectionLock,
			healthzListenAddr:  o.HealthzListenAddress,
			metricsListenAddr:  o.MetricsListenAddress,
		},
	}, nil
}

func (o *ControllerOptions) Run(ctx context.Context) error {
	mgr := &manager.Manager{
		FleetDBClient:      o.fleetDBClient,
		LeaderElectionLock: o.leaderElectionLock,
		HealthzListenAddr:  o.healthzListenAddr,
		MetricsListenAddr:  o.metricsListenAddr,
	}
	return mgr.Run(ctx)
}
