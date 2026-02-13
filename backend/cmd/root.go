package cmd

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

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"

	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"

	"github.com/Azure/ARO-HCP/backend/pkg/app"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
)

type BackendRootCmdFlags struct {
	Kubeconfig                                      string
	K8sNamespace                                    string
	AzureLocation                                   string
	AzureCosmosDBName                               string
	AzureCosmosDBURL                                string
	ClustersServiceURL                              string
	ClustersServiceTLSInsecure                      bool
	MetricsServerListenAddress                      string
	HealthzServerListenAddress                      string
	AzureRuntimeConfigPath                          string
	AzureFirstPartyApplicationCertificateBundlePath string
	AzureFirstPartyApplicationClientID              string
	LogVerbosity                                    int
	MaestroSourceEnvironmentIdentifier              string
}

func (f *BackendRootCmdFlags) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.Kubeconfig, "kubeconfig", f.Kubeconfig, "Absolute path to the kubeconfig file")
	cmd.Flags().StringVar(&f.K8sNamespace, "namespace", f.K8sNamespace, "Kubernetes namespace")
	cmd.Flags().StringVar(&f.AzureLocation, "location", f.AzureLocation, "Azure location")
	cmd.Flags().StringVar(&f.AzureCosmosDBName, "cosmos-name", f.AzureCosmosDBName, "Cosmos database name")
	cmd.Flags().StringVar(&f.AzureCosmosDBURL, "cosmos-url", f.AzureCosmosDBURL, "Cosmos database URL")
	cmd.Flags().StringVar(&f.ClustersServiceURL, "clusters-service-url", f.ClustersServiceURL, "URL of the OCM API gateway")
	cmd.Flags().BoolVar(&f.ClustersServiceTLSInsecure, "insecure", f.ClustersServiceTLSInsecure, "Skip validating TLS for clusters-service")
	cmd.Flags().StringVar(&f.MetricsServerListenAddress, "metrics-listen-address", f.MetricsServerListenAddress, "Address on which to expose metrics")
	cmd.Flags().StringVar(&f.HealthzServerListenAddress, "healthz-listen-address", f.HealthzServerListenAddress, "Address on which Healthz endpoint will be supported")
	cmd.Flags().StringVar(
		&f.AzureRuntimeConfigPath, "azure-runtime-config-path", f.AzureRuntimeConfigPath,
		"Path to a file containing the Azure runtime configuration in JSON or YAML format following the schema defined "+
			"in backend/api/azure/v1/AzureRuntimeConfig",
	)
	cmd.Flags().StringVar(
		&f.AzureFirstPartyApplicationCertificateBundlePath,
		"azure-first-party-application-certificate-bundle-path", f.AzureFirstPartyApplicationCertificateBundlePath,
		"Path to a file containing an X.509 Certificate based client certificate, consisting of a private key and "+
			"certificate chain, in a PEM or PKCS#12 format for authenticating clients with a first party application identity",
	)
	cmd.Flags().StringVar(
		&f.AzureFirstPartyApplicationClientID,
		"azure-first-party-application-client-id",
		f.AzureFirstPartyApplicationClientID,
		"The client id of the first party application identity",
	)
	cmd.Flags().IntVar(&f.LogVerbosity, "log-verbosity", f.LogVerbosity, "Log verbosity. 0 is the default verbosity level, equivalent to INFO. It must be a value >= 0, where a higher value means more verbose output.")

	cmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	cmd.Flags().StringVar(&f.MaestroSourceEnvironmentIdentifier, "maestro-source-environment-identifier", f.MaestroSourceEnvironmentIdentifier,
		"The envName identifier part used when generating Maestro Source IDs using the backend/pkg/maestro.GenereateMaestroSourceID function",
	)
}

func (f *BackendRootCmdFlags) validate() error {
	if len(f.AzureLocation) == 0 {
		return utils.TrackError(fmt.Errorf("--location is required"))
	}

	if len(f.ClustersServiceURL) == 0 {
		return utils.TrackError(fmt.Errorf("--clusters-service-url is required"))
	}

	if len(f.AzureCosmosDBName) == 0 {
		return utils.TrackError(fmt.Errorf("--cosmos-name is required"))
	}

	if len(f.AzureCosmosDBURL) == 0 {
		return utils.TrackError(fmt.Errorf("--cosmos-url is required"))
	}

	if len(f.K8sNamespace) == 0 {
		return utils.TrackError(fmt.Errorf("--namespace is required"))
	}

	if f.LogVerbosity < 0 {
		return utils.TrackError(fmt.Errorf("--log-verbosity must be a value >= 0"))
	}

	if len(f.MaestroSourceEnvironmentIdentifier) == 0 {
		return utils.TrackError(fmt.Errorf("--maestro-source-environment-identifier is required"))
	}
	if len(f.MaestroSourceEnvironmentIdentifier) > 10 {
		return utils.TrackError(fmt.Errorf("--maestro-source-environment-identifier must be less than 10 characters"))
	}

	return nil
}

func (f *BackendRootCmdFlags) ToBackendOptions(ctx context.Context, cmd *cobra.Command) (*app.BackendOptions, error) {
	logger := utils.LoggerFromContext(ctx)

	err := f.validate()
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to validate flags: %w", err))
	}

	kubeconfig, err := app.NewKubeconfig(f.Kubeconfig)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create Kubernetes configuration: %w", err))
	}
	// Use pod name as the lock identity.
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}
	leaderElectionLock, err := app.NewLeaderElectionLock(hostname, kubeconfig, f.K8sNamespace)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create leader election lock: %w", err))
	}

	// Initialize the global OpenTelemetry tracer.
	otelShutdown, err := tracing.ConfigureOpenTelemetryTracer(
		ctx,
		logger,
		semconv.CloudRegion(f.AzureLocation),
		semconv.ServiceNameKey.String(cmd.Short),
		semconv.ServiceVersionKey.String(cmd.Version),
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("could not initialize opentelemetry sdk: %w", err))
	}

	cosmosDBClient, err := app.NewCosmosDBClient(
		ctx, f.AzureCosmosDBURL, f.AzureCosmosDBName,
		azcore.ClientOptions{
			// FIXME Cloud should be determined by other means.
			Cloud:           cloud.AzurePublic,
			TracingProvider: azotel.NewTracingProvider(otel.GetTracerProvider(), nil),
		},
	)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create cosmos db client: %w", err))
	}

	clustersServiceClient, err := app.NewClustersServiceClient(ctx, f.ClustersServiceURL, f.ClustersServiceTLSInsecure)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("failed to create clusters service client: %w", err))
	}

	backendOptions := &app.BackendOptions{
		AppShortDescriptionName:            cmd.Short,
		AppVersion:                         cmd.Version,
		AzureLocation:                      f.AzureLocation,
		LeaderElectionLock:                 leaderElectionLock,
		CosmosDBClient:                     cosmosDBClient,
		ClustersServiceClient:              clustersServiceClient,
		MetricsServerListenAddress:         f.MetricsServerListenAddress,
		HealthzServerListenAddress:         f.HealthzServerListenAddress,
		TracerProviderShutdownFunc:         otelShutdown,
		MaestroSourceEnvironmentIdentifier: f.MaestroSourceEnvironmentIdentifier,
	}

	return backendOptions, nil
}

func NewBackendRootCmdFlags() *BackendRootCmdFlags {
	flags := &BackendRootCmdFlags{
		Kubeconfig:                 "",
		K8sNamespace:               os.Getenv("NAMESPACE"),
		AzureLocation:              os.Getenv("LOCATION"),
		AzureCosmosDBName:          os.Getenv("DB_NAME"),
		AzureCosmosDBURL:           os.Getenv("DB_URL"),
		ClustersServiceURL:         "https://api.openshift.com",
		ClustersServiceTLSInsecure: false,
		MetricsServerListenAddress: ":8081",
		HealthzServerListenAddress: ":8083",
		AzureRuntimeConfigPath:     "",
		AzureFirstPartyApplicationCertificateBundlePath: "",
		AzureFirstPartyApplicationClientID:              "",
		LogVerbosity:                                    0,
		MaestroSourceEnvironmentIdentifier:              "",
	}

	return flags
}

func NewCmdRoot() *cobra.Command {
	processName := filepath.Base(os.Args[0])

	flags := NewBackendRootCmdFlags()

	cmd := &cobra.Command{
		Use:   processName,
		Args:  cobra.NoArgs,
		Short: "ARO HCP Backend",
		Long: fmt.Sprintf(`ARO HCP Backend

	The command runs the ARO HCP Backend. It executes background processing that
	communicates with Clusters Service and CosmosDB.

	# Run ARO HCP Backend locally to connect to a local Clusters Service at http://localhost:8000
	%s --cosmos-name ${DB_NAME} --cosmos-url ${DB_URL} --location ${LOCATION} \
		--clusters-service-url "http://localhost:8000"
`, processName),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := RunRootCmd(cmd, flags)
			if err != nil {
				return utils.TrackError(fmt.Errorf("failed to run: %w", err))
			}
			return nil
		},
		SilenceErrors: true, // errors are printed after Execute
	}

	cmd.SetErrPrefix(cmd.Short + " error:")

	cmd.Version = version.CommitSHA

	flags.AddFlags(cmd)

	return cmd
}

func RunRootCmd(cmd *cobra.Command, flags *BackendRootCmdFlags) error {
	err := flags.validate()
	if err != nil {
		return utils.TrackError(fmt.Errorf("flags validation failed: %w", err))
	}
	// Create a logr.Logger and add it to context for use throughout the application.
	// We use slog.Level(flags.LogVerbosity * -1) to convert the verbosity level to a slog.Level.
	// A value of 0 is equivalent to INFO. Higher values mean more verbose output.
	handlerOptions := &slog.HandlerOptions{Level: slog.Level(flags.LogVerbosity * -1), AddSource: true}
	// Temporary hardcode the log level to -4 to see increased klog logging
	// verbosity.
	handlerOptions.Level = slog.Level(-4)

	// TODO move signal-aware context creation from backend/pkg/app/backend.go here,
	// and redo context handling similar to frontend/cmd/cmd.go.
	ctx := context.Background()
	slogJSONHandler := slog.NewJSONHandler(os.Stdout, handlerOptions)
	logger := logr.FromSlogHandler(slogJSONHandler)
	ctx = utils.ContextWithLogger(ctx, logger)

	// We set our logger to be used on klog log calls
	klog.SetLogger(logger)

	backendOptions, err := flags.ToBackendOptions(ctx, cmd)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to convert flags to backend options: %w", err))
	}

	err = backendOptions.RunBackend(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to run backend: %w", err))
	}

	return nil
}
