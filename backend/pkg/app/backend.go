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
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"

	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/oldoperationscanner"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
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
	AppShortDescriptionName    string
	AppVersion                 string
	AzureLocation              string
	LeaderElectionLock         resourcelock.Interface
	CosmosDBClient             database.DBClient
	ClustersServiceClient      ocm.ClusterServiceClientSpec
	MetricsServerListenAddress string
	HealthzServerListenAddress string
	TracerProviderShutdownFunc func(context.Context) error
}

func (o *BackendOptions) RunBackend(ctx context.Context) error {
	backend := NewBackend(o)
	err := backend.Run(ctx)
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to run backend: %w", err))
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

	backendInformers := informers.NewBackendInformers(ctx, b.options.CosmosDBClient.GlobalListers())

	_, subscriptionLister := backendInformers.Subscriptions()
	activeOperationInformer, activeOperationLister := backendInformers.ActiveOperations()
	clusterInformer, _ := backendInformers.Clusters()

	group.Go(func() error {
		var (
			startedLeading    atomic.Bool
			operationsScanner = oldoperationscanner.NewOperationsScanner(
				b.options.CosmosDBClient, b.options.ClustersServiceClient, b.options.AzureLocation, subscriptionLister)
			dataDumpController = controllerutils.NewClusterWatchingController(
				"DataDump", b.options.CosmosDBClient, clusterInformer, 1*time.Minute, controllers.NewDataDumpController(activeOperationLister, b.options.CosmosDBClient))
			doNothingController              = controllers.NewDoNothingExampleController(b.options.CosmosDBClient, subscriptionLister)
			operationClusterCreateController = operationcontrollers.NewGenericOperationController(
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
			operationClusterUpdateController = operationcontrollers.NewGenericOperationController(
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
			operationClusterDeleteController = operationcontrollers.NewGenericOperationController(
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
			operationRequestCredentialController = operationcontrollers.NewGenericOperationController(
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
			operationRevokeCredentialsController = operationcontrollers.NewGenericOperationController(
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
			clusterServiceMatchingClusterController = mismatchcontrollers.NewClusterServiceClusterMatchingController(b.options.CosmosDBClient, subscriptionLister, b.options.ClustersServiceClient)
			cosmosMatchingNodePoolController        = controllerutils.NewClusterWatchingController(
				"CosmosMatchingNodePools", b.options.CosmosDBClient, clusterInformer, 60*time.Minute,
				mismatchcontrollers.NewCosmosNodePoolMatchingController(b.options.CosmosDBClient, b.options.ClustersServiceClient))
			cosmosMatchingExternalAuthController = controllerutils.NewClusterWatchingController(
				"CosmosMatchingExternalAuths", b.options.CosmosDBClient, clusterInformer, 60*time.Minute,
				mismatchcontrollers.NewCosmosExternalAuthMatchingController(b.options.CosmosDBClient, b.options.ClustersServiceClient))
			cosmosMatchingClusterController = controllerutils.NewClusterWatchingController(
				"CosmosMatchingClusters", b.options.CosmosDBClient, clusterInformer, 60*time.Minute,
				mismatchcontrollers.NewCosmosClusterMatchingController(utilsclock.RealClock{}, b.options.CosmosDBClient, b.options.ClustersServiceClient))
			alwaysSuccessClusterValidationController = validationcontrollers.NewClusterValidationController(
				validations.NewAlwaysSuccessValidation(),
				activeOperationLister,
				b.options.CosmosDBClient,
				clusterInformer,
			)
			deleteOrphanedCosmosResourcesController = mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(b.options.CosmosDBClient, subscriptionLister)
			controlPlaneVersionController           = upgradecontrollers.NewControlPlaneVersionController(
				b.options.CosmosDBClient,
				b.options.ClustersServiceClient,
				activeOperationLister,
				clusterInformer,
			)
			triggerControlPlaneUpgradeController = upgradecontrollers.NewTriggerControlPlaneUpgradeController(
				b.options.CosmosDBClient,
				b.options.ClustersServiceClient,
				activeOperationLister,
				clusterInformer,
			)
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
		logger.Error(err, "backend exiting with error")
		os.Exit(1)
	}

	_ = b.options.TracerProviderShutdownFunc(ctx)
	logger.Info(fmt.Sprintf("%s (%s) stopped", b.options.AppShortDescriptionName, b.options.AppVersion))

	return nil
}
