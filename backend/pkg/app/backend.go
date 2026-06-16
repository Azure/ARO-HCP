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
	"sync"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	k8sutilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	utilsclock "k8s.io/utils/clock"

	"github.com/Azure/ARO-HCP/backend/pkg/azure/cachedreader"
	azureclient "github.com/Azure/ARO-HCP/backend/pkg/azure/client"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billingcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterdeletion"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/clusterpropertiescontroller"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadumpcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauthdeletion"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/managementclustercontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metricscontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatchcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepoolcreationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepooldeletion"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/operationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/statuscontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/upgradecontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/validationcontrollers/validations"
	"github.com/Azure/ARO-HCP/backend/pkg/informers"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database"
	dbinformers "github.com/Azure/ARO-HCP/internal/database/informers"
	unionkubeapplierinformers "github.com/Azure/ARO-HCP/internal/database/unioninformers/kubeapplier"
	sharedleaderelection "github.com/Azure/ARO-HCP/internal/leaderelection"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
)

type Backend struct {
	clock   utilsclock.PassiveClock
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
	KubeApplierDBClients               database.KubeApplierDBClients
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
	BackendIdentityAzureCachedReaders  *cachedreader.BackendIdentityAzureCachedReaders
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
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("function returned"))

	backend, err := o.NewBackend()
	if err != nil {
		return utils.TrackError(fmt.Errorf("failed to construct backend: %w", err))
	}

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer cancel(fmt.Errorf("backend exited"))
		defer wg.Done()
		defer k8sutilruntime.HandleCrash()
		if err := backend.Run(ctx); err != nil {
			mu.Lock()
			errs = append(errs, utils.TrackError(fmt.Errorf("failed to run backend: %w", err)))
			mu.Unlock()
		}
	}()

	wg.Wait()
	return errors.Join(errs...)
}

func (o *BackendOptions) NewBackend() (*Backend, error) {
	if o == nil {
		return nil, errors.New("backend options must not be nil")
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	return &Backend{
		clock:   utilsclock.RealClock{},
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

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

	if b.options.HealthzServerListenAddress != "" {
		s := &backendHealthzServer{
			listenAddress:     b.options.HealthzServerListenAddress,
			metricsRegisterer: b.options.MetricsRegisterer,
			electionChecker:   electionChecker,
		}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("healthz server exited"))
			defer wg.Done()
			defer k8sutilruntime.HandleCrash()
			if err := s.Run(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	if b.options.MetricsServerListenAddress != "" || b.options.MetricsServerListener != nil {
		s := &backendMetricsServer{
			listenAddress:     b.options.MetricsServerListenAddress,
			listener:          b.options.MetricsServerListener,
			metricsRegisterer: b.options.MetricsRegisterer,
			metricsGatherer:   b.options.MetricsGatherer,
		}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("metrics server exited"))
			defer wg.Done()
			defer k8sutilruntime.HandleCrash()
			if err := s.Run(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer cancel(fmt.Errorf("backend controllers leader election exited"))
		defer wg.Done()
		defer k8sutilruntime.HandleCrash()
		if err := b.runBackendControllersUnderLeaderElection(ctx, electionChecker); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}()

	wg.Wait()

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
		defer k8sutilruntime.HandleCrash()
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
	logger := utils.LoggerFromContext(ctx)

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
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()

	// Union kube-applier informers: one aggregator surface that fans out
	// across every management cluster's per-MC kube-applier informers.
	// The controller watches the fleet management-cluster informer/lister
	// and adds/removes per-MC sub-informers as MCs come and go. Pass nil
	// for the relist duration to use the package defaults.
	unionKubeApplierInformersController := unionkubeapplierinformers.NewUnionKubeApplierInformersController(
		managementClusterInformer,
		managementClusterLister,
		unionkubeapplierinformers.NewKubeApplierInformerFactory(b.options.KubeApplierDBClients, nil),
	)
	unionKubeApplierInformers := unionKubeApplierInformersController.Union()
	_, unionReadDesireLister := unionKubeApplierInformers.ReadDesires()

	clusterInformer, clusterLister := backendInformers.Clusters()
	clusterHandler := metricscontrollers.NewClusterMetricsHandler(b.options.MetricsRegisterer)
	clusterMetricsController := metricscontrollers.NewController(
		"ClusterMetrics", clusterInformer, clusterHandler)

	serviceProviderClusterInformer, _ := backendInformers.ServiceProviderClusters()
	clusterVersionMetricsHandler := metricscontrollers.NewClusterVersionMetricsHandler(b.options.MetricsRegisterer, unionReadDesireLister)
	clusterVersionMetricsController := metricscontrollers.NewController(
		"ClusterVersionMetrics", serviceProviderClusterInformer, clusterVersionMetricsHandler)

	_, billingLister := backendInformers.BillingDocs()

	nodePoolInformer, nodePoolLister := backendInformers.NodePools()
	nodePoolHandler := metricscontrollers.NewNodePoolMetricsHandler(b.options.MetricsRegisterer)
	nodePoolMetricsController := metricscontrollers.NewController(
		"NodePoolMetrics", nodePoolInformer, nodePoolHandler)

	externalAuthInformer, externalAuthLister := backendInformers.ExternalAuths()
	externalAuthHandler := metricscontrollers.NewExternalAuthMetricsHandler(b.options.MetricsRegisterer)
	externalAuthMetricsController := metricscontrollers.NewController(
		"ExternalAuthMetrics", externalAuthInformer, externalAuthHandler)

	_, controllerLister := backendInformers.Controllers()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, serviceProviderNodePoolLister := backendInformers.ServiceProviderNodePools()

	subscriptionNonClusterDataDumpController := datadumpcontrollers.NewSubscriptionNonClusterDataDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers)
	clusterRecursiveDataDumpController := datadumpcontrollers.NewClusterRecursiveDataDumpController(b.options.ResourcesDBClient, b.options.KubeApplierDBClients, managementClusterLister, activeOperationLister, backendInformers, unionKubeApplierInformers)
	csStateDumpController := datadumpcontrollers.NewCSStateDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers, unionKubeApplierInformers, b.options.ClustersServiceClient)
	billingDumpController := datadumpcontrollers.NewBillingDumpController(b.options.ResourcesDBClient, b.options.BillingDBClient, activeOperationLister, backendInformers, unionKubeApplierInformers)
	managementClusterDumpController := datadumpcontrollers.NewManagementClusterDataDumpController(b.options.FleetDBClient, managementClusterLister, fleetInformers)
	doNothingController := controllers.NewDoNothingExampleController(b.options.ResourcesDBClient, subscriptionLister)
	dispatchRequestCredentialController := operationcontrollers.NewDispatchRequestCredentialController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationInformer,
	)
	dispatchRevokeCredentialsController := operationcontrollers.NewDispatchRevokeCredentialsController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationInformer,
	)

	operationClusterCreateController := operationcontrollers.NewOperationClusterCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
		unionReadDesireLister,
	)
	operationClusterUpdateController := operationcontrollers.NewOperationClusterUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationClusterDeleteController := operationcontrollers.NewOperationClusterDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.BillingDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolCreateController := operationcontrollers.NewOperationNodePoolCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationNodePoolUpdateController := operationcontrollers.NewOperationNodePoolUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolDeleteController := operationcontrollers.NewOperationNodePoolDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthCreateController := operationcontrollers.NewOperationExternalAuthCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthUpdateController := operationcontrollers.NewOperationExternalAuthUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthDeleteController := operationcontrollers.NewOperationExternalAuthDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationRequestCredentialController := operationcontrollers.NewOperationRequestCredentialController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationRevokeCredentialsController := operationcontrollers.NewOperationRevokeCredentialsController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	clusterServiceMatchingClusterController := mismatchcontrollers.NewClusterServiceClusterMatchingController(b.options.ResourcesDBClient, subscriptionLister, b.options.ClustersServiceClient)
	cosmosMatchingNodePoolController := mismatchcontrollers.NewCosmosNodePoolMatchingController(b.options.ResourcesDBClient, b.options.ClustersServiceClient, backendInformers, unionKubeApplierInformers)
	cosmosMatchingExternalAuthController := mismatchcontrollers.NewCosmosExternalAuthMatchingController(b.options.ResourcesDBClient, b.options.ClustersServiceClient, backendInformers, unionKubeApplierInformers)
	cosmosMatchingClusterController := mismatchcontrollers.NewCosmosClusterMatchingController(b.clock, b.options.ResourcesDBClient, b.options.BillingDBClient, b.options.ClustersServiceClient, backendInformers, unionKubeApplierInformers)
	alwaysSuccessClusterValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAlwaysSuccessValidation(),
		activeOperationLister,
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	deleteOrphanedCosmosResourcesController := mismatchcontrollers.NewDeleteOrphanedCosmosResourcesController(b.options.ResourcesDBClient, b.options.KubeApplierDBClients, subscriptionLister, managementClusterLister)
	backfillClusterUIDController := controllerutils.NewClusterWatchingController(
		"BackfillClusterUID", b.options.ResourcesDBClient, backendInformers, unionKubeApplierInformers, 60*time.Minute,
		mismatchcontrollers.NewBackfillClusterUIDController(b.clock, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister))
	orphanedBillingCleanupController := billingcontrollers.NewOrphanedBillingCleanupController(b.clock, b.options.BillingDBClient, clusterLister, billingLister)
	createBillingDocController := controllerutils.NewClusterWatchingController(
		"CreateBillingDoc", b.options.ResourcesDBClient, backendInformers, unionKubeApplierInformers, 60*time.Second,
		billingcontrollers.NewCreateBillingDocController(b.clock, b.options.AzureLocation, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister, billingLister))
	controlPlaneActiveVersionController := upgradecontrollers.NewControlPlaneActiveVersionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	controlPlaneDesiredVersionController := upgradecontrollers.NewControlPlaneDesiredVersionController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		serviceProviderNodePoolLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
		subscriptionLister,
	)
	triggerControlPlaneUpgradeController := upgradecontrollers.NewTriggerControlPlaneUpgradeController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	clusterBaseDomainPrefixSyncController := clusterpropertiescontroller.NewClusterBaseDomainPrefixSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	clusterPropertiesSyncController := clusterpropertiescontroller.NewClusterPropertiesSyncController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	identityMigrationController := clusterpropertiescontroller.NewIdentityMigrationController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	desiredControlPlaneSizeController := clusterpropertiescontroller.NewDesiredControlPlaneSizeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	// Each aggregator hardcodes its own inertia inside the statuscontrollers
	// package so subsystem-specific tuning lives next to the controller that
	// uses it. The constructors here just supply listers / DB / clock.
	clusterDegradedAggregatorController := statuscontrollers.NewClusterDegradedAggregatorController(
		b.options.ResourcesDBClient,
		clusterLister,
		controllerLister,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
		b.clock,
	)
	nodePoolDegradedAggregatorController := statuscontrollers.NewNodePoolDegradedAggregatorController(
		b.options.ResourcesDBClient,
		nodePoolLister,
		controllerLister,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
		b.clock,
	)
	externalAuthDegradedAggregatorController := statuscontrollers.NewExternalAuthDegradedAggregatorController(
		b.options.ResourcesDBClient,
		externalAuthLister,
		controllerLister,
		activeOperationLister,
		backendInformers,
		b.clock,
	)

	createClusterScopedReadDesiresController := controllers.NewCreateClusterScopedReadDesiresController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.KubeApplierDBClients,
		serviceProviderClusterLister,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier,
	)

	createNodePoolScopedReadDesiresController := controllers.NewCreateNodePoolScopedReadDesiresController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.KubeApplierDBClients,
		serviceProviderClusterLister,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier,
	)

	cosmosMigrationController := controllers.NewCosmosMigrationController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		5*time.Minute,
	)
	createServiceProviderClusterController := controllers.NewCreateServiceProviderClusterController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		clusterLister,
		serviceProviderClusterLister,
		backendInformers,
	)
	createServiceProviderNodePoolController := controllers.NewCreateServiceProviderNodePoolController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		nodePoolLister,
		serviceProviderNodePoolLister,
		backendInformers,
	)

	cleanOrphanedClusterManagedResourceGroupController := controllers.NewCleanOrphanedClusterManagedResourceGroupController(
		b.options.AzureLocation,
		activeOperationLister,
		b.options.ResourcesDBClient,
		b.options.FPAClientBuilder,
		backendInformers,
	)

	azureRPRegistrationValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureResourceProvidersRegistrationValidation(b.options.FPAClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	azureClusterResourceGroupExistenceValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureClusterResourceGroupExistenceValidation(b.options.FPAClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	azureClusterManagedIdentitiesExistenceValidationController := validationcontrollers.NewClusterValidationController(
		validations.NewAzureClusterManagedIdentitiesExistenceValidation(b.options.SMIClientBuilder),
		activeOperationLister,
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolVersionController := upgradecontrollers.NewNodePoolVersionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		subscriptionLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	nodePoolActiveVersionController := upgradecontrollers.NewNodePoolActiveVersionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	triggerNodePoolUpgradeController := upgradecontrollers.NewTriggerNodePoolUpgradeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		serviceProviderNodePoolLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	placementSyncController := managementclustercontrollers.NewManagementClusterPlacementSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		managementClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	nodePoolClusterServiceCreateController := nodepoolcreationcontrollers.NewNodePoolClusterServiceCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	nodePoolDeletionClusterServiceDeleteDispatchController := nodepooldeletion.NewNodePoolClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolClusterServiceIDClearerController := nodepooldeletion.NewNodePoolClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolChildResourcesCleanupController := nodepooldeletion.NewNodePoolChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolDeletionController := nodepooldeletion.NewNodePoolDeletionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	externalAuthDeletionClusterServiceDeleteDispatchController := externalauthdeletion.NewExternalAuthClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	externalAuthClusterServiceIDClearerController := externalauthdeletion.NewExternalAuthClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	externalAuthChildResourcesCleanupController := externalauthdeletion.NewExternalAuthChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
	)

	externalAuthDeletionController := externalauthdeletion.NewExternalAuthDeletionController(
		b.options.ResourcesDBClient,
		activeOperationLister,
		backendInformers,
	)

	clusterDeletionClusterServiceDeleteDispatchController := clusterdeletion.NewClusterClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	clusterClusterServiceIDClearerController := clusterdeletion.NewClusterClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	clusterChildResourcesCleanupController := clusterdeletion.NewClusterChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		activeOperationLister,
		backendInformers,
	)

	clusterDeletionController := clusterdeletion.NewClusterDeletionController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.BillingDBClient,
		activeOperationLister,
		backendInformers,
	)

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          b.options.LeaderElectionLock,
		LeaseDuration: sharedleaderelection.RecommendedLeaseDuration,
		RenewDeadline: sharedleaderelection.RecommendedRenewDeadline,
		RetryPeriod:   sharedleaderelection.RecommendedRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// start the SharedInformers
				go backendInformers.RunWithContext(ctx)
				go fleetInformers.RunWithContext(ctx)

				// start the union kube-applier informers controller +
				// any consumers of its union surface. The controller
				// reacts to management-cluster informer events, so it
				// must start after the fleet informers above.
				go unionKubeApplierInformersController.Run(ctx, 1)

				go subscriptionNonClusterDataDumpController.Run(ctx, 20)
				go clusterRecursiveDataDumpController.Run(ctx, 20)
				go csStateDumpController.Run(ctx, 20)
				go billingDumpController.Run(ctx, 20)
				go managementClusterDumpController.Run(ctx, 20)
				go doNothingController.Run(ctx, 20)
				go dispatchRequestCredentialController.Run(ctx, 20)
				go dispatchRevokeCredentialsController.Run(ctx, 20)
				go nodePoolClusterServiceCreateController.Run(ctx, 20)
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
				go clusterBaseDomainPrefixSyncController.Run(ctx, 20)
				go clusterPropertiesSyncController.Run(ctx, 20)
				go identityMigrationController.Run(ctx, 20)
				go clusterDegradedAggregatorController.Run(ctx, 20)
				go nodePoolDegradedAggregatorController.Run(ctx, 20)
				go externalAuthDegradedAggregatorController.Run(ctx, 20)
				go desiredControlPlaneSizeController.Run(ctx, 20)
				go azureRPRegistrationValidationController.Run(ctx, 20)
				go azureClusterResourceGroupExistenceValidationController.Run(ctx, 20)
				go azureClusterManagedIdentitiesExistenceValidationController.Run(ctx, 20)
				go nodePoolVersionController.Run(ctx, 20)
				go nodePoolActiveVersionController.Run(ctx, 20)
				go createClusterScopedReadDesiresController.Run(ctx, 20)
				go createNodePoolScopedReadDesiresController.Run(ctx, 20)
				go createServiceProviderClusterController.Run(ctx, 20)
				go createServiceProviderNodePoolController.Run(ctx, 20)
				go cleanOrphanedClusterManagedResourceGroupController.Run(ctx, 20)
				go triggerNodePoolUpgradeController.Run(ctx, 20)
				go nodePoolDeletionClusterServiceDeleteDispatchController.Run(ctx, 20)
				go nodePoolClusterServiceIDClearerController.Run(ctx, 20)
				go nodePoolChildResourcesCleanupController.Run(ctx, 20)
				go nodePoolDeletionController.Run(ctx, 20)
				go externalAuthDeletionClusterServiceDeleteDispatchController.Run(ctx, 20)
				go externalAuthClusterServiceIDClearerController.Run(ctx, 20)
				go externalAuthChildResourcesCleanupController.Run(ctx, 20)
				go externalAuthDeletionController.Run(ctx, 20)
				go clusterDeletionClusterServiceDeleteDispatchController.Run(ctx, 20)
				go clusterClusterServiceIDClearerController.Run(ctx, 20)
				go clusterChildResourcesCleanupController.Run(ctx, 20)
				go clusterDeletionController.Run(ctx, 20)
				go operationPhaseMetricsController.Run(ctx, 1)
				go clusterMetricsController.Run(ctx, 1)
				go clusterVersionMetricsController.Run(ctx, 1)
				go nodePoolMetricsController.Run(ctx, 1)
				go externalAuthMetricsController.Run(ctx, 1)
				go placementSyncController.Run(ctx, 20)
				go cosmosMigrationController.Run(ctx, 5)
			},
			OnStoppedLeading: func() {
				// This needs to be defined even though it does nothing.
			},
		},
		ReleaseOnCancel: true,
		WatchDog:        electionChecker,
		Name:            leaderElectionLockName,
	}

	sharedleaderelection.LogLeaseProperties(logger, leaderElectionConfig)

	le, err := leaderelection.NewLeaderElector(leaderElectionConfig)
	if err != nil {
		return err
	}

	le.Run(ctx)
	return nil
}
