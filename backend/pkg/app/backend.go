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

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	utilsclock "k8s.io/utils/clock"

	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billingcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterpropertiescontroller"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadumpcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/managementclustercontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metricscontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/backend/pkg/maestro"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type Backend struct {
	options *BackendOptions
}

type BackendOptions struct {
	AppShortDescriptionName            string
	AppVersion                         string
	AzureLocation                      string
	LeaderElectionLock                 resourcelock.Interface
	ResourcesDBClient                  database.ResourcesDBClient
	BillingDBClient                    database.BillingDBClient
	FleetDBClient                      database.FleetDBClient
	ClustersServiceClient              ocm.ClusterServiceClientSpec
	MetricsRegisterer                  prometheus.Registerer
	MetricsGatherer                    prometheus.Gatherer
	MetricsServerListenAddress         string
	MetricsServerListener              net.Listener
	HealthzServerListenAddress         string
	TracerProviderShutdownFunc         func(context.Context) error
	MaestroSourceEnvironmentIdentifier string
	FPAClientBuilder                   azureclient.FirstPartyApplicationClientBuilder
	BackendIdentityAzureClients        *azureclient.BackendIdentityAzureClients
	ExitOnPanic                        bool
	FPAMIDataplaneClientBuilder        azureclient.FPAMIDataplaneClientBuilder
	SMIClientBuilder                   azureclient.ServiceManagedIdentityClientBuilder
	CheckAccessV2ClientBuilder         azureclient.CheckAccessV2ClientBuilder
	ClusterScopedIdentitiesConfig      *internalazure.ClusterScopedIdentitiesConfig
}

const backendShutdownTimeout = 31 * time.Second

type backendHealthzServer struct {
	listenAddress     string
	metricsRegisterer prometheus.Registerer
	electionChecker   *leaderelection.HealthzAdaptor
}

type backendMetricsServer struct {
	listenAddress     string
	listener          net.Listener // optional pre-created listener for tests
	metricsRegisterer prometheus.Registerer
	metricsGatherer   prometheus.Gatherer
}

func (o *BackendOptions) RunBackend(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("function returned"))

	backend, err := o.NewBackend()
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to construct backend: %w", err))
	}
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- backend.Run(ctx)
		cancel(fmt.Errorf("backend exited"))
	}()

	<-ctx.Done()
	logger.Info("context closed")

	logger.Info("waiting for backend run to finish")
	runErr := <-runErrCh
	if runErr != nil {
		return utils.TrackError(fmt.Errorf("failed to run backend: %w", runErr))
	}

	return nil
}

func (o *BackendOptions) NewBackend() (*Backend, error) {
	if o == nil {
		return nil, errors.New("backend options must not be nil")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return &Backend{
		options: o,
	}, nil
}

// validate checks BackendOptions for invariants that must hold before Run.
// Any failure here is a programmer error in the calling code (flag wiring or
// test setup), not a user-facing condition — we fail fast before any goroutine,
// tracer, or leader-election resource is allocated.
func (o *BackendOptions) validate() error {
	// Registerer and Gatherer must both be explicitly wired by the caller.
	// The production path sets them in cmd/root.go; tests must inject their
	// own. A single half-configured field would silently expose metrics from
	// one registry while populating another, so we refuse to start.
	if o.MetricsRegisterer == nil || o.MetricsGatherer == nil {
		return fmt.Errorf("metrics registerer and gatherer must both be set (registerer set=%t, gatherer set=%t)",
			o.MetricsRegisterer != nil, o.MetricsGatherer != nil)
	}
	return nil
}

func (b *Backend) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("Running backend")

	logger.Info(fmt.Sprintf(
		"%s (%s) started in %s",
		b.options.AppShortDescriptionName,
		b.options.AppVersion,
		b.options.AzureLocation))

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(fmt.Errorf("run returned"))

		logger.Info("shutting down tracer provider")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), backendShutdownTimeout)
		defer shutdownCancel()
		err := b.options.TracerProviderShutdownFunc(shutdownCtx)
		if err != nil {
			logger.Error(err, "failed to shut down tracer provider")
		} else {
			logger.Info("tracer provider shut down completed")
		}
	}()

	// We set k8s.io/apimachinery/pkg/util/runtime.ReallyCrash to the value of the ExitOnPanic option to
	// control the behavior of k8s.io/apimachinery/pkg/util/runtime.HandleCrash* methods
	k8sutilruntime.ReallyCrash = b.options.ExitOnPanic

	// Create HealthzAdaptor for leader election
	electionChecker := leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	// Launch servers and leader election as independent goroutines.
	// Each goroutine sends its result on errCh when done. On first
	// error or context cancellation, cancel propagates to all.
	goroutines := 1 // leader election always runs
	if b.options.HealthzServerListenAddress != "" {
		goroutines++
	}
	if b.options.MetricsServerListenAddress != "" || b.options.MetricsServerListener != nil {
		goroutines++
	}
	errCh := make(chan error, goroutines)

	if b.options.HealthzServerListenAddress != "" {
		s := &backendHealthzServer{
			listenAddress:     b.options.HealthzServerListenAddress,
			metricsRegisterer: b.options.MetricsRegisterer,
			electionChecker:   electionChecker,
		}
		go func() {
			err := s.Run(ctx)
			if err != nil {
				cancel(fmt.Errorf("healthz server exited: %w", err))
			}
			errCh <- err
		}()
	}

	if b.options.MetricsServerListenAddress != "" || b.options.MetricsServerListener != nil {
		s := &backendMetricsServer{
			listenAddress:     b.options.MetricsServerListenAddress,
			listener:          b.options.MetricsServerListener,
			metricsRegisterer: b.options.MetricsRegisterer,
			metricsGatherer:   b.options.MetricsGatherer,
		}
		go func() {
			err := s.Run(ctx)
			if err != nil {
				cancel(fmt.Errorf("metrics server exited: %w", err))
			}
			errCh <- err
		}()
	}

	go func() {
		err := b.runBackendControllersUnderLeaderElection(ctx, electionChecker)
		// When leader election exits (e.g. lost lease), cancel so Run() unblocks and performs shutdown.
		cancel(fmt.Errorf("backend controllers leader election exited"))
		errCh <- err
	}()

	<-ctx.Done()

	errs := []error{}
	for range goroutines {
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Info("goroutine completed", "message", err.Error())
			errs = append(errs, err)
		}
	}

	logger.Info(fmt.Sprintf("%s (%s) stopped", b.options.AppShortDescriptionName, b.options.AppVersion))

	return errors.Join(errs...)
}

func (s *backendHealthzServer) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	listener, err := net.Listen("tcp", s.listenAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.listenAddress, err)
	}

	backendHealthGauge := promauto.With(s.metricsRegisterer).NewGauge(prometheus.GaugeOpts{Name: "backend_health", Help: "backend_health is 1 when healthy"})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.electionChecker.Check(r); err != nil {
			logger.Error(err, "Readiness probe failed")
			http.Error(w, "lease not renewed", http.StatusServiceUnavailable)
			backendHealthGauge.Set(0.0)
			return
		}
		w.WriteHeader(http.StatusOK)
		backendHealthGauge.Set(1.0)
	})

	addr := listener.Addr().String()
	server := &http.Server{Addr: addr, Handler: mux}
	return runHTTPServer(ctx, "healthz server", addr, server, func() error {
		return server.Serve(listener)
	})
}

func (s *backendMetricsServer) Run(ctx context.Context) error {
	listener := s.listener
	if listener == nil {
		l, err := net.Listen("tcp", s.listenAddress)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", s.listenAddress, err)
		}
		listener = l
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
		s.metricsRegisterer,
		promhttp.HandlerFor(
			prometheus.Gatherers{s.metricsGatherer},
			promhttp.HandlerOpts{},
		),
	))

	addr := listener.Addr().String()
	server := &http.Server{Addr: addr, Handler: mux}
	return runHTTPServer(ctx, "metrics server", addr, server, func() error {
		return server.Serve(listener)
	})
}

func runHTTPServer(ctx context.Context, name string, addr string, server *http.Server, serve func() error) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), backendShutdownTimeout)
			defer shutdownCancel()
			_ = shutdownHTTPServer(shutdownCtx, server, name)
		case <-done:
		}
	}()

	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("%s listening on %s", name, addr))
	err := serve()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// shutdownHTTPServer shuts down an HTTP server, logging its outcome and returning
// an error if the shutdown failed. If the provided server is nil, no action is taken.
// name is a descriptive name for the server, used in the logging.
func shutdownHTTPServer(ctx context.Context, server *http.Server, name string) error {
	if server == nil {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)

	logger.Info(fmt.Sprintf("shutting down %s", name))
	err := server.Shutdown(ctx)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to shut down %s", name))
	} else {
		logger.Info(fmt.Sprintf("%s shut down completed", name))
	}

	return err
}

// runBackendControllersUnderLeaderElection runs the backen controllers under
// a leader election loop.
func (b *Backend) runBackendControllersUnderLeaderElection(ctx context.Context, electionChecker *leaderelection.HealthzAdaptor) error {
	backendInformers := informers.NewBackendInformers(ctx,
		b.options.ResourcesDBClient.ResourcesGlobalListers(),
		b.options.BillingDBClient.BillingGlobalListers(),
	)

	_, subscriptionLister := backendInformers.Subscriptions()
	activeOperationInformer, activeOperationLister := backendInformers.ActiveOperations()

	operationPhaseHandler := metricscontrollers.NewOperationPhaseMetricsHandler(b.options.MetricsRegisterer)
	operationPhaseMetricsController := metricscontrollers.NewController(
		"OperationPhaseMetrics", backendInformers.AllOperations(), operationPhaseHandler)

	fleetInformers := dbinformers.NewFleetInformers(ctx, b.options.FleetDBClient.GlobalListers())
	_, stampLister := fleetInformers.Stamps()
	_, managementClusterLister := fleetInformers.ManagementClusters()

	clusterInformer, clusterLister := backendInformers.Clusters()
	clusterHandler := metricscontrollers.NewClusterMetricsHandler(b.options.MetricsRegisterer)
	clusterMetricsController := metricscontrollers.NewController(
		"ClusterMetrics", clusterInformer, clusterHandler)

	_, billingLister := backendInformers.BillingDocs()

	nodePoolInformer, _ := backendInformers.NodePools()
	nodePoolHandler := metricscontrollers.NewNodePoolMetricsHandler(b.options.MetricsRegisterer)
	nodePoolMetricsController := metricscontrollers.NewController(
		"NodePoolMetrics", nodePoolInformer, nodePoolHandler)

	externalAuthInformer, _ := backendInformers.ExternalAuths()
	externalAuthHandler := metricscontrollers.NewExternalAuthMetricsHandler(b.options.MetricsRegisterer)
	externalAuthMetricsController := metricscontrollers.NewController(
		"ExternalAuthMetrics", externalAuthInformer, externalAuthHandler)

	maestroClientBuilder := maestro.NewMaestroClientBuilder()

	subscriptionNonClusterDataDumpController := datadumpcontrollers.NewSubscriptionNonClusterDataDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers)
	clusterRecursiveDataDumpController := datadumpcontrollers.NewClusterRecursiveDataDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers)
	csStateDumpController := datadumpcontrollers.NewCSStateDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers, b.options.ClustersServiceClient)
	billingDumpController := datadumpcontrollers.NewBillingDumpController(b.options.ResourcesDBClient, b.options.BillingDBClient, activeOperationLister, backendInformers)
	managementClusterDumpController := datadumpcontrollers.NewManagementClusterDataDumpController(b.options.FleetDBClient, managementClusterLister, fleetInformers)
	doNothingController := controllers.NewDoNothingExampleController(b.options.ResourcesDBClient, subscriptionLister)
	dispatchRequestCredentialController := operationcontrollers.NewDispatchRequestCredentialController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationInformer,
	)
	dispatchRevokeCredentialsController := operationcontrollers.NewDispatchRevokeCredentialsController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationInformer,
	)
	operationClusterCreateController := operationcontrollers.NewOperationClusterCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationClusterUpdateController := operationcontrollers.NewOperationClusterUpdateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationClusterDeleteController := operationcontrollers.NewOperationClusterDeleteController(
		b.options.ResourcesDBClient,
		b.options.BillingDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolCreateController := operationcontrollers.NewOperationNodePoolCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolUpdateController := operationcontrollers.NewOperationNodePoolUpdateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolDeleteController := operationcontrollers.NewOperationNodePoolDeleteController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthCreateController := operationcontrollers.NewOperationExternalAuthCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthUpdateController := operationcontrollers.NewOperationExternalAuthUpdateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthDeleteController := operationcontrollers.NewOperationExternalAuthDeleteController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationRequestCredentialController := operationcontrollers.NewOperationRequestCredentialController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationRevokeCredentialsController := operationcontrollers.NewOperationRevokeCredentialsController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	clusterServiceMatchingClusterController := mismatchcontrollers.NewClusterServiceClusterMatchingController(b.options.ResourcesDBClient, subscriptionLister, b.options.ClustersServiceClient)
	cosmosMatchingNodePoolController := mismatchcontrollers.NewCosmosNodePoolMatchingController(b.options.ResourcesDBClient, b.options.ClustersServiceClient, backendInformers)
	cosmosMatchingExternalAuthController := mismatchcontrollers.NewCosmosExternalAuthMatchingController(b.options.ResourcesDBClient, b.options.ClustersServiceClient, backendInformers)
	cosmosMatchingClusterController := mismatchcontrollers.NewCosmosClusterMatchingController(utilsclock.RealClock{}, b.options.ResourcesDBClient, b.options.BillingDBClient, b.options.ClustersServiceClient, backendInformers)
	alwaysSuccessClusterValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAlwaysSuccessValidation(),
		activeOperationLister,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	deleteOrphanedCosmosResourcesController := mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(b.options.ResourcesDBClient, subscriptionLister)
	backfillClusterUIDController := controllerutils.NewClusterWatchingController(
		"BackfillClusterUID", b.options.ResourcesDBClient, backendInformers, 60*time.Minute,
		mismatchcontrollers.NewBackfillClusterUIDController(utilsclock.RealClock{}, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister))
	orphanedBillingCleanupController := billingcontrollers.NewOrphanedBillingCleanupController(utilsclock.RealClock{}, b.options.BillingDBClient, clusterLister, billingLister)
	createBillingDocController := controllerutils.NewClusterWatchingController(
		"CreateBillingDoc", b.options.ResourcesDBClient, backendInformers, 60*time.Second,
		billingcontrollers.NewCreateBillingDocController(utilsclock.RealClock{}, b.options.AzureLocation, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister, billingLister))
	controlPlaneActiveVersionController := upgradecontrollers.NewControlPlaneActiveVersionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
	)
	controlPlaneDesiredVersionController := upgradecontrollers.NewControlPlaneDesiredVersionController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		subscriptionLister,
	)
	triggerControlPlaneUpgradeController := upgradecontrollers.NewTriggerControlPlaneUpgradeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	clusterPropertiesSyncController := clusterpropertiescontroller.NewClusterPropertiesSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	identityMigrationController := clusterpropertiescontroller.NewIdentityMigrationController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	maestroCreateClusterScopedReadonlyBundlesController := controllers.NewCreateClusterScopedMaestroReadonlyBundlesController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.ClustersServiceClient,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier, maestroClientBuilder,
	)
	maestroReadAndPersistClusterScopedReadonlyBundlesContentController := controllers.NewReadAndPersistClusterScopedMaestroReadonlyBundlesContentController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.ClustersServiceClient,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier, maestroClientBuilder,
	)

	maestroCreateNodePoolScopedReadonlyBundlesController := controllers.NewCreateNodePoolScopedMaestroReadonlyBundlesController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.ClustersServiceClient,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier, maestroClientBuilder,
	)
	maestroReadAndPersistNodePoolScopedReadonlyBundlesContentController := controllers.NewReadAndPersistNodePoolScopedMaestroReadonlyBundlesContentController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.ClustersServiceClient,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier, maestroClientBuilder,
	)

	maestroDeleteOrphanedReadonlyBundlesController := controllers.NewDeleteOrphanedMaestroReadonlyBundlesController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		maestroClientBuilder,
		b.options.MaestroSourceEnvironmentIdentifier,
	)

	azureRPRegistrationValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureResourceProvidersRegistrationValidation(b.options.FPAClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	azureClusterResourceGroupExistenceValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureClusterResourceGroupExistenceValidation(b.options.FPAClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	azureClusterManagedIdentitiesExistenceValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureClusterManagedIdentitiesExistenceValidation(b.options.SMIClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	nodePoolVersionController := upgradecontrollers.NewNodePoolVersionController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	triggerNodePoolUpgradeController := upgradecontrollers.NewTriggerNodePoolUpgradeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	managementClusterMigrationController := managementclustercontrollers.NewManagementClusterMigrationController(
		b.options.ClustersServiceClient,
		b.options.FleetDBClient,
		stampLister,
		managementClusterLister,
	)
	placementSyncController := managementclustercontrollers.NewManagementClusterPlacementSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		managementClusterLister,
		backendInformers,
	)

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          b.options.LeaderElectionLock,
		LeaseDuration: leaderElectionLeaseDuration,
		RenewDeadline: leaderElectionRenewDeadline,
		RetryPeriod:   leaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// start the SharedInformers
				go backendInformers.RunWithContext(ctx)
				go fleetInformers.RunWithContext(ctx)

				go subscriptionNonClusterDataDumpController.Run(ctx, 20)
				go clusterRecursiveDataDumpController.Run(ctx, 20)
				go csStateDumpController.Run(ctx, 20)
				go billingDumpController.Run(ctx, 20)
				go managementClusterDumpController.Run(ctx, 20)
				go doNothingController.Run(ctx, 20)
				go dispatchRequestCredentialController.Run(ctx, 20)
				go dispatchRevokeCredentialsController.Run(ctx, 20)
				go operationClusterCreateController.Run(ctx, 20)
				go operationClusterUpdateController.Run(ctx, 20)
				go operationClusterDeleteController.Run(ctx, 20)
				go operationNodePoolCreateController.Run(ctx, 20)
				go operationNodePoolUpdateController.Run(ctx, 20)
				go operationNodePoolDeleteController.Run(ctx, 20)
				go operationExternalAuthCreateController.Run(ctx, 20)
				go operationExternalAuthUpdateController.Run(ctx, 20)
				go operationExternalAuthDeleteController.Run(ctx, 20)
				go operationRequestCredentialController.Run(ctx, 20)
				go operationRevokeCredentialsController.Run(ctx, 20)
				go clusterServiceMatchingClusterController.Run(ctx, 20)
				go cosmosMatchingNodePoolController.Run(ctx, 20)
				go cosmosMatchingExternalAuthController.Run(ctx, 20)
				go cosmosMatchingClusterController.Run(ctx, 20)
				go alwaysSuccessClusterValidationController.Run(ctx, 20)
				go deleteOrphanedCosmosResourcesController.Run(ctx, 20)
				go backfillClusterUIDController.Run(ctx, 20)
				go orphanedBillingCleanupController.Run(ctx, 20)
				go createBillingDocController.Run(ctx, 20)
				go controlPlaneActiveVersionController.Run(ctx, 20)
				go controlPlaneDesiredVersionController.Run(ctx, 20)
				go triggerControlPlaneUpgradeController.Run(ctx, 20)
				go clusterPropertiesSyncController.Run(ctx, 20)
				go identityMigrationController.Run(ctx, 20)
				go azureRPRegistrationValidationController.Run(ctx, 20)
				go azureClusterResourceGroupExistenceValidationController.Run(ctx, 20)
				go azureClusterManagedIdentitiesExistenceValidationController.Run(ctx, 20)
				go nodePoolVersionController.Run(ctx, 20)
				go maestroCreateClusterScopedReadonlyBundlesController.Run(ctx, 20)
				go maestroReadAndPersistClusterScopedReadonlyBundlesContentController.Run(ctx, 20)
				go maestroCreateNodePoolScopedReadonlyBundlesController.Run(ctx, 20)
				go maestroReadAndPersistNodePoolScopedReadonlyBundlesContentController.Run(ctx, 20)
				go maestroDeleteOrphanedReadonlyBundlesController.Run(ctx, 20)
				go triggerNodePoolUpgradeController.Run(ctx, 20)
				go operationPhaseMetricsController.Run(ctx, 1)
				go clusterMetricsController.Run(ctx, 1)
				go nodePoolMetricsController.Run(ctx, 1)
				go externalAuthMetricsController.Run(ctx, 1)
				go managementClusterMigrationController.Run(ctx, 1)
				go placementSyncController.Run(ctx, 20)
			},
			OnStoppedLeading: func() {
				// This needs to be defined even though it does nothing.
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
}
