package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Azure/ARO-HCP/backend/controllers"
	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/listers"
	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/tracing"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	leaderElectionLockName      = "backend-leader"
	leaderElectionLeaseDuration = 15 * time.Second
	leaderElectionRenewDeadline = 10 * time.Second
	leaderElectionRetryPeriod   = 2 * time.Second
)

type Backend struct {
	options *BackendOptions
}

type BackendOptions struct {
	AppShortDescriptionName    string
	AppVersion                 string
	Kubeconfig                 string
	K8sNamespace               string
	AzureLocation              string
	CosmosDBName               string
	CosmosDBURL                string
	ClustersServiceURL         string
	ClustersServiceTLSInsecure bool
	MetricsServerListenAddress string
	HealthzServerListenAddress string
	AzureRuntimeConfigPath     string
	AzureFPACertBundlePath     string
	AzureFPAClientID           string
}

func NewBackend(options *BackendOptions) *Backend {
	return &Backend{
		options: options,
	}
}

func (b *Backend) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Running backend")

	logger.Info(fmt.Sprintf(
		"%s (%s) started in %s",
		b.options.AppShortDescriptionName,
		version.CommitSHA,
		b.options.AzureLocation))

	// Use pod name as the lock identity.
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	kubeconfig, err := newKubeconfig(b.options.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes configuration: %w", err)
	}

	leaderElectionLock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		b.options.K8sNamespace,
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
	otelShutdown, err := tracing.ConfigureOpenTelemetryTracer(
		ctx,
		logger,
		semconv.CloudRegion(b.options.AzureLocation),
		semconv.ServiceNameKey.String("ARO HCP Backend"),
		semconv.ServiceVersionKey.String(version.CommitSHA),
	)
	if err != nil {
		return fmt.Errorf("could not initialize opentelemetry sdk: %w", err)
	}

	otelTracerProvider := otel.GetTracerProvider()

	azureConfig, err := getAzureConfig(ctx, b.options.AzureRuntimeConfigPath, otelTracerProvider)
	if err != nil {
		return fmt.Errorf("error getting azure configuration: %w", err)
	}

	fpaClientBuilder, err := getFPAClientBuilder(ctx, logger, b.options.AzureFPACertBundlePath, b.options.AzureFPAClientID, azureConfig)
	if err != nil {
		return fmt.Errorf("error configuring FPA client builder: %w", err)
	}

	err = callAzureExampleInflight(ctx, fpaClientBuilder)
	if err != nil {
		return err
	}

	// Create the database client.
	cosmosDatabaseClient, err := database.NewCosmosDatabaseClient(
		b.options.CosmosDBURL,
		b.options.CosmosDBName,
		azureConfig.CloudEnvironment.PolicyClientOptions(),
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
		URL(b.options.ClustersServiceURL).
		Insecure(b.options.ClustersServiceTLSInsecure).
		Build()
	if err != nil {
		return fmt.Errorf("failed to create OCM connection: %w", err)
	}

	// Create HealthzAdaptor for leader election
	electionChecker := leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	group, ctx := errgroup.WithContext(ctx)

	// Handle requests directly for /healthz endpoint
	if b.options.HealthzServerListenAddress != "" {
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

		healthzServer := &http.Server{Addr: b.options.HealthzServerListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("Healthz server listening on %s", b.options.HealthzServerListenAddress))
			err := healthzServer.ListenAndServe()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		})
	}

	var srv *http.Server
	if b.options.MetricsServerListenAddress != "" {
		http.Handle("/metrics", promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer,
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{},
			),
		))

		srv = &http.Server{Addr: b.options.MetricsServerListenAddress}

		group.Go(func() error {
			logger.Info(fmt.Sprintf("metrics server listening on %s", b.options.MetricsServerListenAddress))
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

	clusterServiceClient := ocm.NewClusterServiceClientWithTracing(
		ocm.NewClusterServiceClient(
			ocmConnection,
			"",
			false,
			false,
		),
		oldoperationscanner.TracerName,
	)
	subscriptionLister := listers.NewThreadSafeAtomicLister[arm.Subscription]()

	group.Go(func() error {
		var (
			startedLeading                   atomic.Bool
			operationsScanner                = oldoperationscanner.NewOperationsScanner(dbClient, ocmConnection, b.options.AzureLocation, subscriptionLister)
			subscriptionInformerController   = controllers.NewSubscriptionInformerController(dbClient, subscriptionLister)
			doNothingController              = controllers.NewDoNothingExampleController(dbClient, subscriptionLister)
			operationClusterCreateController = controllers.NewOperationClusterCreateController(
				b.options.AzureLocation,
				10*time.Second,
				subscriptionLister,
				dbClient,
				clusterServiceClient,
				http.DefaultClient,
			)
			operationClusterDeleteController = controllers.NewOperationClusterDeleteController(
				b.options.AzureLocation,
				10*time.Second,
				subscriptionLister,
				dbClient,
				clusterServiceClient,
				http.DefaultClient,
			)
		)

		le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock:          leaderElectionLock,
			LeaseDuration: leaderElectionLeaseDuration,
			RenewDeadline: leaderElectionRenewDeadline,
			RetryPeriod:   leaderElectionRetryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					operationsScanner.LeaderGauge.Set(1)
					startedLeading.Store(true)
					go subscriptionInformerController.Run(ctx, 1)
					go operationsScanner.Run(ctx)
					go doNothingController.Run(ctx, 20)
					go operationClusterCreateController.Run(ctx, 20)
					go operationClusterDeleteController.Run(ctx, 20)
				},
				OnStoppedLeading: func() {
					operationsScanner.LeaderGauge.Set(0)
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
		return err
	}

	_ = otelShutdown(ctx)
	logger.Info(fmt.Sprintf("%s (%s) stopped", b.options.AppShortDescriptionName, b.options.AppVersion))

	return nil
}

func newKubeconfig(kubeconfig string) (*rest.Config, error) {
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loader.ExplicitPath = kubeconfig
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, nil).ClientConfig()
}

func callAzureExampleInflight(ctx context.Context, clientBuilder azureclient.FPAClientBuilder) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("calling Azure example inflight method")

	validation := controllers.NewAzureRPRegistrationValidation(clientBuilder)
	// The tenant and subscription values would come when a cluster is processed. Here in main we do not process
	// particular clusters so we do not have that information so for this example we just set the red hat dev account info.
	resourceID := api.Must(azcorearm.ParseResourceID("/subscriptions/1d3378d3-5a3f-4712-85a1-2485495dfc4b/resourceGroups/some-resource-group/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/testcluster"))
	exampleHCPCluster := api.NewDefaultHCPOpenShiftCluster(resourceID, "westus3")
	err := validation.Validate(ctx, exampleHCPCluster)
	if err != nil {
		return fmt.Errorf("resource providers registration validation error")
	}
	return nil
}
