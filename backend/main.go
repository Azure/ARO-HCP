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

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"

	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"

	"golang.org/x/sync/errgroup"

	"sigs.k8s.io/yaml"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/Azure/ARO-HCP/backend/controllers"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/azure/configdto"

	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/fpa"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/version"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
)

const (
	leaderElectionLockName      = "backend-leader"
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

var (
	argKubeconfig              string
	argNamespace               string
	argLocation                string
	argCosmosName              string
	argCosmosURL               string
	argClustersServiceURL      string
	argInsecure                bool
	argMetricsListenAddress    string
	argPortListenAddress       string
	argsAzureRuntimeConfigPath string
	argsAzureFPACertBundlePath string
	argsAzureFPAClientID       string

	processName = filepath.Base(os.Args[0])

	rootCmd = &cobra.Command{
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
		Version:       "unknown", // overridden by build info below
		RunE:          Run,
		SilenceErrors: true, // errors are printed after Execute
	}
)

func init() {
	rootCmd.SetErrPrefix(rootCmd.Short + " error:")

	rootCmd.Flags().StringVar(&argKubeconfig, "kubeconfig", "", "Absolute path to the kubeconfig file")
	rootCmd.Flags().StringVar(&argNamespace, "namespace", os.Getenv("NAMESPACE"), "Kubernetes namespace")
	rootCmd.Flags().StringVar(&argLocation, "location", os.Getenv("LOCATION"), "Azure location")
	rootCmd.Flags().StringVar(&argCosmosName, "cosmos-name", os.Getenv("DB_NAME"), "Cosmos database name")
	rootCmd.Flags().StringVar(&argCosmosURL, "cosmos-url", os.Getenv("DB_URL"), "Cosmos database URL")
	rootCmd.Flags().StringVar(&argClustersServiceURL, "clusters-service-url", "https://api.openshift.com", "URL of the OCM API gateway")
	rootCmd.Flags().StringVar(&argsAzureRuntimeConfigPath, "azure-runtime-config-path", "", "Path to a file containing the Azure runtime configuration")
	rootCmd.Flags().StringVar(
		&argsAzureFPACertBundlePath,
		"azure-first-party-application-certificate-bundle-path",
		"",
		"Path to a file containing an X.509 Certificate based client certificate, consisting of a private key and "+
			"certificate chain, in a PEM or PKCS#12 format for authenticating clients with a first party application identity",
	)
	rootCmd.Flags().StringVar(
		&argsAzureFPAClientID,
		"azure-first-party-application-client-id",
		"",
		"The client id of the first party application identity",
	)
	rootCmd.Flags().BoolVar(&argInsecure, "insecure", false, "Skip validating TLS for clusters-service")
	rootCmd.Flags().StringVar(&argMetricsListenAddress, "metrics-listen-address", ":8081", "Address on which to expose metrics")
	rootCmd.Flags().StringVar(&argPortListenAddress, "healthz-listen-address", ":8083", "Address on which Healthz endpoint will be supported")

	rootCmd.MarkFlagsRequiredTogether("cosmos-name", "cosmos-url")

	rootCmd.Version = version.CommitSHA
}

func loadAzureRuntimeConfig(path string) (configdto.AzureRuntimeConfig, error) {
	if len(path) == 0 {
		return configdto.AzureRuntimeConfig{}, fmt.Errorf("configuration path is required")
	}

	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return configdto.AzureRuntimeConfig{}, fmt.Errorf("error reading file %s: %w", path, rawBytes)
	}

	var config configdto.AzureRuntimeConfig

	err = yaml.Unmarshal(rawBytes, &config)
	if err != nil {
		return configdto.AzureRuntimeConfig{}, fmt.Errorf("error unmarshaling file %s: %w", path, rawBytes)
	}

	err = config.Validate()
	if err != nil {
		return configdto.AzureRuntimeConfig{}, fmt.Errorf("error validating file %s: %w", path, err)
	}

	return config, nil
}

func buildAzureConfig(azurRuntimeConfigDTO configdto.AzureRuntimeConfig, tracerProvider trace.TracerProvider) (azureconfig.AzureConfig, error) {
	builder := config.NewAzureCloudEnvironmentBuilder()
	builder.CloudEnvironment(azurRuntimeConfigDTO.CloudEnvironment.String())
	builder.TracerProvider(tracerProvider)
	cloudEnvironment, err := builder.Build()
	if err != nil {
		return azureconfig.AzureConfig{}, fmt.Errorf("error building azure cloud environment configuration: %w", err)
	}

	// TODO should the domain layer types also have validation or do we trust that
	// they are valid because the user-provided parts were validated
	out := config.AzureConfig{
		RuntimeConfig: azureconfig.AzureRuntimeConfig{
			CloudEnvironment: cloudEnvironment,
		},
	}

	return out, err
}

func newKubeconfig(kubeconfig string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loader.ExplicitPath = kubeconfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, nil).ClientConfig()
}

func Run(cmd *cobra.Command, args []string) error {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
	})
	logger := slog.New(handler)
	klog.SetLogger(logr.FromSlogHandler(handler))

	if len(argLocation) == 0 {
		return errors.New("location is required")
	}

	// TODO uncomment these lines once the flags and configs have been rolled out
	// to all ARO-HCP environments.
	// if len(argsAzureRuntimeConfigPath) == 0 {
	// 	return fmt.Errorf("--%s is required", argsAzureRuntimeConfigPath)
	// }
	// if len(argsAzureFPAClientID) == 0 {
	// 	return fmt.Errorf("--%s is required", argsAzureFPAClientID)
	// }
	// if len(argsAzureFPABundlePath) == 0 {
	// 	return fmt.Errorf("--%s is required", argsAzureFPABundlePath)
	// }

	logger.Info(fmt.Sprintf(
		"%s (%s) started in %s",
		cmd.Short,
		version.CommitSHA,
		argLocation))

	// Use pod name as the lock identity.
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	kubeconfig, err := newKubeconfig(argKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes configuration: %w", err)
	}

	leaderElectionLock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		argNamespace,
		leaderElectionLockName,
		resourcelock.ResourceLockConfig{
			Identity: hostname,
		},
		kubeconfig,
		leaderElectionRenewDeadline)
	if err != nil {
		return fmt.Errorf("failed to create leader election lock: %w", err)
	}

	// Initialize the global OpenTelemetry tracer.
	ctx := context.Background()
	otelShutdown, err := tracing.ConfigureOpenTelemetryTracer(
		ctx,
		logger,
		semconv.CloudRegion(argLocation),
		semconv.ServiceNameKey.String("ARO HCP Backend"),
		semconv.ServiceVersionKey.String(version.CommitSHA),
	)
	if err != nil {
		return fmt.Errorf("could not initialize opentelemetry sdk: %w", err)
	}

	otelTracerProvider := otel.GetTracerProvider()

	// TODO azure related code cannot be executed until the cli flags and configs
	// have been rolled out to prod.

	// TODO move this where appropriate
	azureRuntimeConfigDTO, err := loadAzureRuntimeConfig(argsAzureRuntimeConfigPath)
	if err != nil {
		return fmt.Errorf("error loading azure runtime config: %w", err)
	}

	// TODO move this where appropriate
	azureConfig, err := buildAzureConfig(azureRuntimeConfigDTO, otelTracerProvider)
	if err != nil {
		return fmt.Errorf("error building azure configuration: %w", err)
	}

	// Create the database client.
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		argCosmosURL,
		argCosmosName,
		azcore.ClientOptions{
			Cloud:           azureConfig.RuntimeConfig.CloudEnvironment.Configuration(),
			TracingProvider: azureConfig.RuntimeConfig.CloudEnvironment.ARMClientOptions().TracingProvider,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create the CosmosDB client: %w", err)
	}

	dbClient, err := database.NewDBClient(context.Background(), cosmosDatabaseClient)
	if err != nil {
		return fmt.Errorf("failed to create the database client: %w", err)
	}

	// Create OCM connection
	ocmConnection, err := ocmsdk.NewUnauthenticatedConnectionBuilder().
		TransportWrapper(func(r http.RoundTripper) http.RoundTripper {
			return otelhttp.NewTransport(http.DefaultTransport)
		}).
		URL(argClustersServiceURL).
		Insecure(argInsecure).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %w", err)
	}

	// Create HealthzAdaptor for leader election
	electionChecker := leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	group, ctx := errgroup.WithContext(ctx)

	// Handle requests directly for /healthz endpoint
	if argPortListenAddress != "" {
		backendHealthGauge := promauto.With(prometheus.DefaultRegisterer).NewGauge(prometheus.GaugeOpts{Name: "backend_health", Help: "backend_health is 1 when healthy"})

		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {

			if err := electionChecker.Check(r); err != nil {
				http.Error(w, "lease not renewed", http.StatusServiceUnavailable)
				backendHealthGauge.Set(0.0)
				return
			}
			w.WriteHeader(http.StatusOK)
			backendHealthGauge.Set(1.0)
		})

		healthzServer := &http.Server{Addr: argPortListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("Healthz server listening on %s", argPortListenAddress))
			err := healthzServer.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}

	var srv *http.Server
	if argMetricsListenAddress != "" {
		http.Handle("/metrics", promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer,
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{},
			),
		))

		srv = &http.Server{Addr: argMetricsListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("metrics server listening on %s", argMetricsListenAddress))
			err := srv.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}

			return err
		})
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("Caught interrupt signal")
		if srv != nil {
			_ = srv.Close()
		}
	}()

	group.Go(func() error {
		var (
			startedLeading      atomic.Bool
			operationsScanner   = NewOperationsScanner(dbClient, ocmConnection, argLocation)
			doNothingController = controllers.NewDoNothingExampleController(dbClient)
		)

		// TODO the azure related code cannot run until corresponding CLI flags have been rolled out

		// Create FPA TokenCredentials with watching and caching
		certReader, err := fpa.NewWatchingFileCertificateReader(
			ctx,
			// TODO: receive via CLI flag the filesystem path to where the fpa certificate bundle path is located. This
			// bundle contains the certificate and private key for the FPA.
			"TODO",
			30*time.Minute,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create certificate reader: %w", err)
		}

		// We create the FPA token credential retriever here. Then we pass it to the cluster inflights controller,
		// which then is used to instantiate a validation that uses the FPA token credential retriever. And then the
		// validations uses the retriever to retrieve a token credential based on the information associated to the
		// cluster(the tenant of the cluster, the subscription id, ...)
		fpaTokenCredRetriever, err := fpa.NewFirstPartyApplicationTokenCredentialRetriever(
			logger,
			"TODO", // TODO: receive the client ID associated to the FPA via CLI flag
			certReader,
			// Notice how we are passing the policy client options from the Azure cloud environment type.
			azureConfig.RuntimeConfig.CloudEnvironment.PolicyClientOptions(),
		)

		fpaClientBuilder := azureclient.NewFpaClientBuilder(
			fpaTokenCredRetriever, azureConfig.RuntimeConfig.CloudEnvironment.ARMClientOptions(),
		)

		clusterInflightsController := controllers.NewClusterInflightsController(
			dbClient,
			fpaClientBuilder,
		)

		le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock:          leaderElectionLock,
			LeaseDuration: leaderElectionLeaseDuration,
			RenewDeadline: leaderElectionRenewDeadline,
			RetryPeriod:   leaderElectionRetryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					operationsScanner.leaderGauge.Set(1)
					startedLeading.Store(true)
					go operationsScanner.Run(ctx, logger)
					go doNothingController.Run(ctx, 20)
					go clusterInflightsController.Run(ctx, 20)
				},
				OnStoppedLeading: func() {
					operationsScanner.leaderGauge.Set(0)
					if startedLeading.Load() {
						operationsScanner.Join()
					}
				},
			},
			ReleaseOnCancel: true,
			WatchDog:        electionChecker,
			Name:            leaderElectionLockName,
		})
		if err != nil {
			return err
		}

		le.Run(ctx)
		return nil
	})

	if err := group.Wait(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	_ = otelShutdown(ctx)
	logger.Info(fmt.Sprintf("%s (%s) stopped", cmd.Short, cmd.Version))

	return nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		rootCmd.PrintErrln(rootCmd.ErrPrefix(), err.Error())
		os.Exit(1)
	}
}
