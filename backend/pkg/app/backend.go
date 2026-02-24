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
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterpropertiescontroller"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	"github.com/Azure/ARO-HCP/internal/database"
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
	CosmosDBClient                     database.DBClient
	ClustersServiceClient              ocm.ClusterServiceClientSpec
	MetricsServerListenAddress         string
	HealthzServerListenAddress         string
	TracerProviderShutdownFunc         func(context.Context) error
	MaestroSourceEnvironmentIdentifier string
}

func (o *BackendOptions) RunBackend(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("function returned"))

	backend := NewBackend(o)
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
		b.options.AppVersion,
		b.options.AzureLocation))

	var healthzServer *http.Server
	var metricsServer *http.Server

	ctx, cancel := context.WithCancelCause(ctx)
	defer func() {
		cancel(fmt.Errorf("run returned"))

		// always attempt a graceful shutdown, a double ctrl+c exits the process
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
		defer shutdownCancel()
		_ = b.shutdownHTTPServer(shutdownCtx, metricsServer, "metrics server")
		_ = b.shutdownHTTPServer(shutdownCtx, healthzServer, "healthz server")

		logger.Info("shutting down tracer provider")
		err := b.options.TracerProviderShutdownFunc(shutdownCtx)
		if err != nil {
			logger.Error(err, "failed to shut down tracer provider")
		} else {
			logger.Info("tracer provider shut down completed")
		}
	}()

	// Create HealthzAdaptor for leader election
	electionChecker := leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)

	// Create channels and wait group for goroutines.
	// The size of the channel is the maximum number of goroutines that are to be
	// executed.
	// The wait group is used to wait for all goroutines to complete. The sync.WaitGroup counter
	// is incremented for each goroutine that is to be executed according to the configuration.
	errCh := make(chan error, 3)
	wg := sync.WaitGroup{}

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

		healthzServer = &http.Server{Addr: b.options.HealthzServerListenAddress}

		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("Healthz server listening on %s", b.options.HealthzServerListenAddress))
			errCh <- healthzServer.ListenAndServe()
		}()
	}

	if b.options.MetricsServerListenAddress != "" {
		http.Handle("/metrics", promhttp.InstrumentMetricHandler(
			prometheus.DefaultRegisterer,
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{},
			),
		))

		metricsServer = &http.Server{Addr: b.options.MetricsServerListenAddress}

		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("metrics server listening on %s", b.options.MetricsServerListenAddress))
			errCh <- metricsServer.ListenAndServe()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		errCh <- b.runBackendControllersUnderLeaderElection(ctx, electionChecker)
	}()

	<-ctx.Done()

	// always attempt a graceful shutdown, a double ctrl+c exits the process
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
	defer shutdownCancel()
	_ = b.shutdownHTTPServer(shutdownCtx, metricsServer, "metrics server")
	_ = b.shutdownHTTPServer(shutdownCtx, healthzServer, "healthz server")

	wg.Wait()
	close(errCh)
	errs := []error{}
	for err := range errCh {
		if err != nil {
			logger.Info("go func completed", "message", err.Error())
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}

	logger.Info(fmt.Sprintf("%s (%s) stopped", b.options.AppShortDescriptionName, b.options.AppVersion))

	return errors.Join(errs...)
}

// shutdownHTTPServer shuts down an HTTP server, logging its outcome and returning
// an error if the shutdown failed. If the provided server is nil, no action is taken.
// name is a descriptive name for the server, used in the logging.
func (b *Backend) shutdownHTTPServer(ctx context.Context, server *http.Server, name string) error {
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
	backendInformers := informers.NewBackendInformers(ctx, b.options.CosmosDBClient.GlobalListers())

	_, subscriptionLister := backendInformers.Subscriptions()
	activeOperationInformer, activeOperationLister := backendInformers.ActiveOperations()

	startedLeading := atomic.Bool{}
	operationsScanner := oldoperationscanner.NewOperationsScanner(
		b.options.CosmosDBClient, b.options.ClustersServiceClient, b.options.AzureLocation, subscriptionLister)
	dataDumpController := controllerutils.NewClusterWatchingController(
		"DataDump", b.options.CosmosDBClient, backendInformers, 1*time.Minute, controllers.NewDataDumpController(activeOperationLister, b.options.CosmosDBClient))
	doNothingController := controllers.NewDoNothingExampleController(b.options.CosmosDBClient, subscriptionLister)
	operationClusterCreateController := operationcontrollers.NewGenericOperationController(
		"OperationClusterCreate",
		operationcontrollers.NewOperationClusterCreateSynchronizer(
			b.options.AzureLocation,
			b.options.CosmosDBClient,
			b.options.ClustersServiceClient,
			http.DefaultClient,
		),
		10*time.Second,
		activeOperationInformer,
		b.options.CosmosDBClient,
	)
	operationClusterUpdateController := operationcontrollers.NewGenericOperationController(
		"OperationClusterUpdate",
		operationcontrollers.NewOperationClusterUpdateSynchronizer(
			b.options.CosmosDBClient,
			b.options.ClustersServiceClient,
			http.DefaultClient,
		),
		10*time.Second,
		activeOperationInformer,
		b.options.CosmosDBClient,
	)
	operationClusterDeleteController := operationcontrollers.NewGenericOperationController(
		"OperationClusterDelete",
		operationcontrollers.NewOperationClusterDeleteSynchronizer(
			b.options.CosmosDBClient,
			b.options.ClustersServiceClient,
			http.DefaultClient,
		),
		10*time.Second,
		activeOperationInformer,
		b.options.CosmosDBClient,
	)
	operationRequestCredentialController := operationcontrollers.NewGenericOperationController(
		"OperationRequestCredential",
		operationcontrollers.NewOperationRequestCredentialSynchronizer(
			b.options.CosmosDBClient,
			b.options.ClustersServiceClient,
			http.DefaultClient,
		),
		10*time.Second,
		activeOperationInformer,
		b.options.CosmosDBClient,
	)
	operationRevokeCredentialsController := operationcontrollers.NewGenericOperationController(
		"OperationRevokeCredentials",
		operationcontrollers.NewOperationRevokeCredentialsSynchronizer(
			b.options.CosmosDBClient,
			b.options.ClustersServiceClient,
			http.DefaultClient,
		),
		10*time.Second,
		activeOperationInformer,
		b.options.CosmosDBClient,
	)
	clusterServiceMatchingClusterController := mismatchcontrollers.NewClusterServiceClusterMatchingController(b.options.CosmosDBClient, subscriptionLister, b.options.ClustersServiceClient)
	cosmosMatchingNodePoolController := controllerutils.NewClusterWatchingController(
		"CosmosMatchingNodePools", b.options.CosmosDBClient, backendInformers, 60*time.Minute,
		mismatchcontrollers.NewCosmosNodePoolMatchingController(b.options.CosmosDBClient, b.options.ClustersServiceClient))
	cosmosMatchingExternalAuthController := controllerutils.NewClusterWatchingController(
		"CosmosMatchingExternalAuths", b.options.CosmosDBClient, backendInformers, 60*time.Minute,
		mismatchcontrollers.NewCosmosExternalAuthMatchingController(b.options.CosmosDBClient, b.options.ClustersServiceClient))
	cosmosMatchingClusterController := controllerutils.NewClusterWatchingController(
		"CosmosMatchingClusters", b.options.CosmosDBClient, backendInformers, 60*time.Minute,
		mismatchcontrollers.NewCosmosClusterMatchingController(utilsclock.RealClock{}, b.options.CosmosDBClient, b.options.ClustersServiceClient))
	alwaysSuccessClusterValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAlwaysSuccessValidation(),
		activeOperationLister,
		b.options.CosmosDBClient,
		backendInformers,
	)
	deleteOrphanedCosmosResourcesController := mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(b.options.CosmosDBClient, subscriptionLister)
	controlPlaneVersionController := upgradecontrollers.NewControlPlaneVersionController(
		b.options.CosmosDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	triggerControlPlaneUpgradeController := upgradecontrollers.NewTriggerControlPlaneUpgradeController(
		b.options.CosmosDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)
	clusterPropertiesSyncController := clusterpropertiescontroller.NewClusterPropertiesSyncController(
		b.options.CosmosDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          b.options.LeaderElectionLock,
		LeaseDuration: leaderElectionLeaseDuration,
		RenewDeadline: leaderElectionRenewDeadline,
		RetryPeriod:   leaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				operationsScanner.LeaderGauge.Set(1)
				startedLeading.Store(true)

				// start the SharedInformers
				go backendInformers.RunWithContext(ctx)

				go operationsScanner.Run(ctx)
				go dataDumpController.Run(ctx, 20)
				go doNothingController.Run(ctx, 20)
				go operationClusterCreateController.Run(ctx, 20)
				go operationClusterUpdateController.Run(ctx, 20)
				go operationClusterDeleteController.Run(ctx, 20)
				go operationRequestCredentialController.Run(ctx, 20)
				go operationRevokeCredentialsController.Run(ctx, 20)
				go clusterServiceMatchingClusterController.Run(ctx, 20)
				go cosmosMatchingNodePoolController.Run(ctx, 20)
				go cosmosMatchingExternalAuthController.Run(ctx, 20)
				go cosmosMatchingClusterController.Run(ctx, 20)
				go alwaysSuccessClusterValidationController.Run(ctx, 20)
				go deleteOrphanedCosmosResourcesController.Run(ctx, 20)
				go controlPlaneVersionController.Run(ctx, 20)
				go triggerControlPlaneUpgradeController.Run(ctx, 20)
				go clusterPropertiesSyncController.Run(ctx, 20)
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
}
