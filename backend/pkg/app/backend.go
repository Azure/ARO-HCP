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
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/billing"
	clusterbackups "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	clustercreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/creation"
	credentialrequestcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/creation"
	credentialrequestdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/deletion"
	credentialrequestoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrequest/operations"
	credentialrevocationcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/creation"
	credentialrevocationdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/deletion"
	credentialrevocationoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/credentialrevocation/operations"
	clusterdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/deletion"
	clusteridentity "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/identity"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/legacycredentialrequest"
	clusteroperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/operations"
	clusterplacement "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/placement"
	clusterproperties "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/properties"
	clusterreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/readdesires"
	clusterstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/status"
	clusterupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/update"
	clustervalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/validation"
	clusterversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/version"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/cosmosmigration"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/datadump"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/example"
	externalauthcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/creation"
	externalauthdeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/deletion"
	externalauthoperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/operations"
	externalauthstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/status"
	externalauthupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/externalauth/update"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/metrics"
	"github.com/Azure/ARO-HCP/backend/pkg/controllers/mismatch"
	nodepoolcreation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/creation"
	nodepooldeletion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/deletion"
	nodepooloperations "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/operations"
	nodepoolreaddesires "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/readdesires"
	nodepoolstatus "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/status"
	nodepoolupdate "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/update"
	nodepoolvalidation "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/validation"
	nodepoolversion "github.com/Azure/ARO-HCP/backend/pkg/controllers/nodepool/version"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/controllerutils"
	"github.com/Azure/ARO-HCP/backend/pkg/utils/validationutils"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
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
	AppShortDescriptionName                             string
	AppVersion                                          string
	AzureLocation                                       string
	LeaderElectionLock                                  resourcelock.Interface
	ResourcesDBClient                                   corecosmosstorage.ResourcesDBClient
	BillingDBClient                                     billingcosmosstorage.BillingDBClient
	FleetDBClient                                       fleetcosmosstorage.FleetDBClient
	KubeApplierDBClients                                kubeappliercosmosstorage.KubeApplierDBClients
	ClustersServiceClient                               ocm.ClusterServiceClientSpec
	MetricsRegisterer                                   prometheus.Registerer
	MetricsGatherer                                     prometheus.Gatherer
	MetricsServerListenAddress                          string
	MetricsServerListener                               net.Listener
	HealthzServerListenAddress                          string
	TracerProviderShutdownFunc                          func(context.Context) error
	MaestroSourceEnvironmentIdentifier                  string
	FPAClientBuilder                                    azureclient.FirstPartyApplicationClientBuilder
	BackendIdentityAzureClients                         *azureclient.BackendIdentityAzureClients
	BackendIdentityAzureCachedReaders                   *cachedreader.BackendIdentityAzureCachedReaders
	ExitOnPanic                                         bool
	FPAMIDataplaneClientBuilder                         azureclient.FPAMIDataplaneClientBuilder
	MIDataplaneBasedIdentityAccessTokenRetrieverBuilder azureclient.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder
	BackupConfig                                        *clusterbackups.BackupConfig
	SMIClientBuilder                                    azureclient.ServiceManagedIdentityClientBuilder
	CheckAccessV2ClientBuilder                          azureclient.CheckAccessV2ClientBuilder
	ClusterScopedIdentitiesConfig                       *internalazure.ClusterScopedIdentitiesConfig
	CloudEnvironment                                    *azureconfig.AzureCloudEnvironment
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
	if o.BackupConfig == nil {
		return fmt.Errorf("backup config must be set")
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

	backendInformers := coreinformers.NewBackendInformers(ctx,
		b.options.ResourcesDBClient.ResourcesGlobalListers(),
		b.options.ResourcesDBClient,
		b.options.BillingDBClient.BillingGlobalListers(),
	)

	_, subscriptionLister := backendInformers.Subscriptions()
	activeOperationInformer, activeOperationLister := backendInformers.ActiveOperations()

	operationPhaseHandler := metrics.NewOperationPhaseMetricsHandler(b.options.MetricsRegisterer)
	operationPhaseMetricsController := metrics.NewController(
		"OperationPhaseMetrics", backendInformers.AllOperations(), operationPhaseHandler)

	fleetInformers := fleetinformers.NewFleetInformers(ctx, b.options.FleetDBClient.GlobalListers(), b.options.FleetDBClient)
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()

	// Union kube-applier informers: one aggregator surface that fans out
	// across every management cluster's per-MC kube-applier coreinformers.
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
	_, unionApplyDesireLister := unionKubeApplierInformers.ApplyDesires()

	clusterInformer, clusterLister := backendInformers.Clusters()
	clusterHandler := metrics.NewClusterMetricsHandler(b.options.MetricsRegisterer)
	clusterMetricsController := metrics.NewController(
		"ClusterMetrics", clusterInformer, clusterHandler)

	serviceProviderClusterInformer, _ := backendInformers.ServiceProviderClusters()
	clusterVersionMetricsHandler := metrics.NewClusterVersionMetricsHandler(b.options.MetricsRegisterer, unionReadDesireLister)
	clusterVersionMetricsController := metrics.NewController(
		"ClusterVersionMetrics", serviceProviderClusterInformer, clusterVersionMetricsHandler)

	clusterInfoHandler := metrics.NewClusterInfoMetricsHandler(b.options.MetricsRegisterer)
	clusterInfoMetricsController := metrics.NewController(
		"ClusterInfoMetrics", serviceProviderClusterInformer, clusterInfoHandler)

	_, billingLister := backendInformers.BillingDocs()

	nodePoolInformer, nodePoolLister := backendInformers.NodePools()
	nodePoolHandler := metrics.NewNodePoolMetricsHandler(b.options.MetricsRegisterer)
	nodePoolMetricsController := metrics.NewController(
		"NodePoolMetrics", nodePoolInformer, nodePoolHandler)

	externalAuthInformer, externalAuthLister := backendInformers.ExternalAuths()
	externalAuthHandler := metrics.NewExternalAuthMetricsHandler(b.options.MetricsRegisterer)
	externalAuthMetricsController := metrics.NewController(
		"ExternalAuthMetrics", externalAuthInformer, externalAuthHandler)

	_, controllerLister := backendInformers.Controllers()
	_, serviceProviderClusterLister := backendInformers.ServiceProviderClusters()
	_, serviceProviderNodePoolLister := backendInformers.ServiceProviderNodePools()

	subscriptionNonClusterDataDumpController := datadump.NewSubscriptionNonClusterDataDumpController(b.options.ResourcesDBClient, backendInformers)
	clusterRecursiveDataDumpController := datadump.NewClusterRecursiveDataDumpController(b.options.ResourcesDBClient, b.options.KubeApplierDBClients, managementClusterLister, activeOperationLister, backendInformers, unionKubeApplierInformers)
	csStateDumpController := datadump.NewCSStateDumpController(b.options.ResourcesDBClient, activeOperationLister, backendInformers, unionKubeApplierInformers, b.options.ClustersServiceClient)
	billingDumpController := datadump.NewBillingDumpController(b.options.ResourcesDBClient, b.options.BillingDBClient, activeOperationLister, backendInformers, unionKubeApplierInformers)
	managementClusterDumpController := datadump.NewManagementClusterDataDumpController(b.options.FleetDBClient, managementClusterLister, fleetInformers)
	doNothingController := example.NewDoNothingExampleController(b.options.ResourcesDBClient, subscriptionLister)
	dispatchRequestCredentialController := legacycredentialrequest.NewDispatchRequestCredentialController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationInformer,
	)
	adminCredentialsDispatchRequestCredentialController := credentialrequestoperations.NewDispatchRequestCredentialController(
		b.clock,
		b.options.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	)
	adminCredentialsDispatchRevokeCredentialsController := credentialrevocationoperations.NewDispatchRevokeCredentialsController(
		b.clock,
		b.options.ResourcesDBClient,
		clusterLister,
		activeOperationInformer,
	)
	adminCredentialsOperationRequestCredentialPollController := credentialrequestoperations.NewOperationRequestCredentialPollController(
		b.clock,
		b.options.ResourcesDBClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	adminCredentialsOperationRevokeCredentialsPollController := credentialrevocationoperations.NewOperationRevokeCredentialsPollController(
		b.clock,
		b.options.ResourcesDBClient,
		clusterLister,
		http.DefaultClient,
		activeOperationInformer,
	)
	adminCredentialsIssuanceObserverController := credentialrequestcreation.NewIssuanceObserverController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	adminCredentialsDesiresCreatorController := credentialrequestcreation.NewDesiresCreatorController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
	)
	adminCredentialsPostIssuanceCleanupController := credentialrequestdeletion.NewPostIssuanceCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
	)
	adminCredentialsRevokedGCController := credentialrequestdeletion.NewRevokedGCController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	adminCredentialsClusterDeletionCleanupController := credentialrequestdeletion.NewClusterDeletionCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
	)
	systemAdminCredentialRevocationMarkRequestsController := credentialrevocationcreation.NewRevocationMarkRequestsController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
	)
	systemAdminCredentialRevocationDesiresController := credentialrevocationcreation.NewRevocationDesiresController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
		unionApplyDesireLister,
		unionReadDesireLister,
	)
	systemAdminCredentialRevocationCompletionController := credentialrevocationdeletion.NewRevocationCompletionController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	systemAdminCredentialRevocationDeletionController := credentialrevocationdeletion.NewRevocationDeletionController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
	)

	operationClusterCreateController := clusteroperations.NewOperationClusterCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
		unionReadDesireLister,
	)
	operationClusterUpdateController := clusteroperations.NewOperationClusterUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		unionReadDesireLister,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationClusterDeleteController := clusteroperations.NewOperationClusterDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.BillingDBClient,
		b.options.KubeApplierDBClients,
		unionReadDesireLister,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationNodePoolCreateController := nodepooloperations.NewOperationNodePoolCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationNodePoolUpdateController := nodepooloperations.NewOperationNodePoolUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		unionReadDesireLister,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationNodePoolDeleteController := nodepooloperations.NewOperationNodePoolDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationExternalAuthCreateController := externalauthoperations.NewOperationExternalAuthCreateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationExternalAuthUpdateController := externalauthoperations.NewOperationExternalAuthUpdateController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		unionReadDesireLister,
		http.DefaultClient,
		activeOperationInformer,
		backendInformers,
	)
	operationExternalAuthDeleteController := externalauthoperations.NewOperationExternalAuthDeleteController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)
	operationRequestCredentialController := legacycredentialrequest.NewOperationRequestCredentialController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		http.DefaultClient,
		activeOperationInformer,
	)

	clusterServiceMatchingClusterController := mismatch.NewClusterServiceClusterMatchingController(b.clock, b.options.ResourcesDBClient, subscriptionLister, b.options.ClustersServiceClient)
	alwaysSuccessClusterValidationController := clustervalidation.NewClusterValidationController(
		validationutils.NewAlwaysSuccessValidation(),
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
	)
	deleteOrphanedCosmosResourcesController := mismatch.NewDeleteOrphanedCosmosResourcesController(b.options.ResourcesDBClient, b.options.KubeApplierDBClients, subscriptionLister, managementClusterLister)
	missingResourceIDController := mismatch.NewMissingResourceIDController(b.options.ResourcesDBClient)
	backfillClusterUIDController := controllerutils.NewClusterWatchingController(
		"BackfillClusterUID", b.options.ResourcesDBClient, backendInformers, unionKubeApplierInformers, 60*time.Minute,
		mismatch.NewBackfillClusterUIDController(b.clock, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister))
	orphanedBillingCleanupController := billing.NewOrphanedBillingCleanupController(b.clock, b.options.BillingDBClient, clusterLister, billingLister)
	createBillingDocController := controllerutils.NewClusterWatchingController(
		"CreateBillingDoc", b.options.ResourcesDBClient, backendInformers, unionKubeApplierInformers, 60*time.Second,
		billing.NewCreateBillingDocController(b.clock, b.options.AzureLocation, b.options.ResourcesDBClient, b.options.BillingDBClient, clusterLister, billingLister))
	controlPlaneActiveVersionController := clusterversion.NewControlPlaneActiveVersionController(
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	controlPlaneDesiredVersionController := clusterversion.NewControlPlaneDesiredVersionController(
		b.clock,
		b.options.ResourcesDBClient,
		clusterLister,
		b.options.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		nodePoolLister,
		serviceProviderNodePoolLister,
		backendInformers,
	)
	triggerControlPlaneUpgradeController := clusterversion.NewTriggerControlPlaneUpgradeController(
		b.clock,
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		serviceProviderClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	clusterBaseDomainPrefixSyncController := clusterproperties.NewClusterBaseDomainPrefixSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)
	clusterPropertiesSyncController := clusterproperties.NewClusterPropertiesSyncController(
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	identityMigrationController := clusterproperties.NewIdentityMigrationController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)
	desiredControlPlaneSizeController := clusterproperties.NewDesiredControlPlaneSizeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)
	serviceProviderClusterPropertiesSyncController := clusterproperties.NewServiceProviderClusterPropertiesSyncController(
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)

	backupScheduleController := clusterbackups.NewBackupScheduleController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
		b.options.MaestroSourceEnvironmentIdentifier,
		b.options.BackupConfig,
	)

	// Each aggregator hardcodes its own inertia inside the statusutils
	// package so subsystem-specific tuning lives next to the controller that
	// uses it. The constructors here just supply listers / DB / clock.
	clusterDegradedAggregatorController := clusterstatus.NewClusterDegradedAggregatorController(
		b.options.ResourcesDBClient,
		clusterLister,
		controllerLister,
		backendInformers,
		unionKubeApplierInformers,
		b.clock,
	)
	clusterRequirementsValidAggregatorController := clusterstatus.NewClusterRequirementsValidAggregatorController(
		b.options.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		backendInformers,
	)
	nodePoolDegradedAggregatorController := nodepoolstatus.NewNodePoolDegradedAggregatorController(
		b.options.ResourcesDBClient,
		nodePoolLister,
		controllerLister,
		backendInformers,
		unionKubeApplierInformers,
		b.clock,
	)
	nodePoolRequirementsValidAggregatorController := nodepoolstatus.NewNodePoolRequirementsValidAggregatorController(
		b.options.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		backendInformers,
	)
	externalAuthDegradedAggregatorController := externalauthstatus.NewExternalAuthDegradedAggregatorController(
		b.options.ResourcesDBClient,
		externalAuthLister,
		controllerLister,
		backendInformers,
		b.clock,
	)

	createClusterScopedReadDesiresController := clusterreaddesires.NewCreateClusterScopedReadDesiresController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.KubeApplierDBClients,
		serviceProviderClusterLister,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier,
	)

	createNodePoolScopedReadDesiresController := nodepoolreaddesires.NewCreateNodePoolScopedReadDesiresController(
		activeOperationLister, b.options.ResourcesDBClient, b.options.KubeApplierDBClients,
		serviceProviderClusterLister,
		backendInformers, b.options.MaestroSourceEnvironmentIdentifier,
	)

	cosmosMigrationController := cosmosmigration.NewCosmosMigrationController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		5*time.Minute,
	)
	createServiceProviderClusterController := clustercreation.NewCreateServiceProviderClusterController(
		b.options.ResourcesDBClient,
		clusterLister,
		serviceProviderClusterLister,
		backendInformers,
	)
	createServiceProviderNodePoolController := nodepoolcreation.NewCreateServiceProviderNodePoolController(
		b.options.ResourcesDBClient,
		nodePoolLister,
		serviceProviderNodePoolLister,
		backendInformers,
	)

	cleanOrphanedClusterManagedResourceGroupController := clusterdeletion.NewCleanOrphanedClusterManagedResourceGroupController(
		b.options.AzureLocation,
		activeOperationLister,
		b.options.ResourcesDBClient,
		b.options.FPAClientBuilder,
		backendInformers,
	)

	virtualMachineResourceSKUsCachedReaderController := cachedreader.NewFPAVirtualMachineResourceSKUsCachedReaderController(
		b.options.FPAClientBuilder,
		b.options.AzureLocation,
	)

	azureRPRegistrationValidationController := clustervalidation.NewClusterValidationController(
		validationutils.NewAzureResourceProvidersRegistrationValidation(b.options.FPAClientBuilder),
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
	)

	azureClusterResourceGroupExistenceValidationController := clustervalidation.NewClusterValidationController(
		validationutils.NewAzureClusterResourceGroupExistenceValidation(b.options.FPAClientBuilder),
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
	)

	azureClusterManagedIdentitiesExistenceValidationController := clustervalidation.NewClusterValidationController(
		validationutils.NewAzureClusterManagedIdentitiesExistenceValidation(b.options.SMIClientBuilder),
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
	)
	azureVMSizeSupportsEphemeralOSDiskValidationController := nodepoolvalidation.NewNodePoolValidationController(
		validationutils.NewAzureVMSizeSupportsEphemeralOSDiskValidation(virtualMachineResourceSKUsCachedReaderController),
		b.options.ResourcesDBClient,
		serviceProviderNodePoolLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	azureNodePoolVMQuotaValidationController := nodepoolvalidation.NewNodePoolValidationController(
		validationutils.NewAzureNodePoolVMQuotaValidation(virtualMachineResourceSKUsCachedReaderController, b.options.FPAClientBuilder),
		b.options.ResourcesDBClient,
		serviceProviderNodePoolLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	controlPlaneIdentitiesPermissionsValidationController := clustervalidation.NewClusterValidationController(
		validationutils.NewControlPlaneIdentitiesPermissionsClusterValidation(
			b.options.SMIClientBuilder,
			b.options.ClusterScopedIdentitiesConfig,
			b.options.BackendIdentityAzureCachedReaders,
			b.options.CheckAccessV2ClientBuilder,
			b.options.MIDataplaneBasedIdentityAccessTokenRetrieverBuilder,
			b.options.CloudEnvironment.CheckAccessV2Scope(),
		),
		b.options.ResourcesDBClient,
		serviceProviderClusterLister,
		backendInformers,
	)

	nodePoolVersionController := nodepoolversion.NewNodePoolVersionController(
		b.options.ResourcesDBClient,
		subscriptionLister,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	nodePoolActiveVersionController := nodepoolversion.NewNodePoolActiveVersionController(
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
		unionReadDesireLister,
	)
	triggerNodePoolUpgradeController := nodepoolversion.NewTriggerNodePoolUpgradeController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		serviceProviderNodePoolLister,
		backendInformers,
		unionKubeApplierInformers,
	)
	placementSyncController := clusterplacement.NewManagementClusterPlacementSyncController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		managementClusterLister,
		backendInformers,
		unionKubeApplierInformers,
	)

	nodePoolClusterServiceCreateController := nodepoolcreation.NewNodePoolClusterServiceCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)

	externalAuthClusterServiceCreateController := externalauthcreation.NewExternalAuthClusterServiceCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	nodePoolDeletionClusterServiceDeleteDispatchController := nodepooldeletion.NewNodePoolClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)

	nodePoolClusterServiceIDClearerController := nodepooldeletion.NewNodePoolClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolChildResourcesCleanupController := nodepooldeletion.NewNodePoolChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
		unionKubeApplierInformers,
	)
	nodePoolDeletionController := nodepooldeletion.NewNodePoolDeletionController(
		b.options.ResourcesDBClient,
		backendInformers,
		unionKubeApplierInformers,
	)

	externalAuthDeletionClusterServiceDeleteDispatchController := externalauthdeletion.NewExternalAuthClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	externalAuthClusterServiceIDClearerController := externalauthdeletion.NewExternalAuthClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	externalAuthChildResourcesCleanupController := externalauthdeletion.NewExternalAuthChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		backendInformers,
	)

	externalAuthDeletionController := externalauthdeletion.NewExternalAuthDeletionController(
		b.options.ResourcesDBClient,
		backendInformers,
	)

	clusterPendingClusterServiceIDAssignController := clustercreation.NewClusterPendingClusterServiceIDAssignController(
		b.options.ResourcesDBClient,
		backendInformers,
	)

	clusterClusterServiceCreateController := clustercreation.NewClusterClusterServiceCreateController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	clusterDeletionClusterServiceDeleteDispatchController := clusterdeletion.NewClusterClusterServiceDeleteDispatchController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	clusterClusterServiceIDClearerController := clusterdeletion.NewClusterClusterServiceIDClearerController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	clusterCredentialDeletionMarkerController := clusterdeletion.NewClusterCredentialDeletionMarkerController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
	)

	clusterChildResourcesCleanupController := clusterdeletion.NewClusterChildResourcesCleanupController(
		b.options.ResourcesDBClient,
		b.options.KubeApplierDBClients,
		backendInformers,
	)

	clusterDeletionController := clusterdeletion.NewClusterDeletionController(
		utilsclock.RealClock{},
		b.options.ResourcesDBClient,
		b.options.BillingDBClient,
		backendInformers,
	)

	clusterClusterServiceUpdateDispatchController := clusterupdate.NewClusterClusterServiceUpdateDispatchController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)

	nodePoolClusterServiceUpdateDispatchController := nodepoolupdate.NewNodePoolClusterServiceUpdateDispatchController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		backendInformers,
	)
	externalAuthClusterServiceUpdateDispatchController := externalauthupdate.NewExternalAuthClusterServiceUpdateDispatchController(
		b.options.ResourcesDBClient,
		b.options.ClustersServiceClient,
		activeOperationLister,
		backendInformers,
	)

	fetchDataPlaneOperatorsManagedIdentitiesInfoController := clusteridentity.NewFetchDataPlaneOperatorsManagedIdentitiesInfoController(
		b.clock,
		b.options.ResourcesDBClient,
		backendInformers,
		b.options.SMIClientBuilder,
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
				go adminCredentialsDispatchRequestCredentialController.Run(ctx, 20)
				go adminCredentialsDispatchRevokeCredentialsController.Run(ctx, 20)
				go adminCredentialsOperationRequestCredentialPollController.Run(ctx, 20)
				go adminCredentialsOperationRevokeCredentialsPollController.Run(ctx, 20)
				go adminCredentialsIssuanceObserverController.Run(ctx, 20)
				go adminCredentialsDesiresCreatorController.Run(ctx, 20)
				go adminCredentialsPostIssuanceCleanupController.Run(ctx, 20)
				go adminCredentialsRevokedGCController.Run(ctx, 20)
				go adminCredentialsClusterDeletionCleanupController.Run(ctx, 20)
				go systemAdminCredentialRevocationMarkRequestsController.Run(ctx, 20)
				go systemAdminCredentialRevocationDesiresController.Run(ctx, 20)
				go systemAdminCredentialRevocationCompletionController.Run(ctx, 20)
				go systemAdminCredentialRevocationDeletionController.Run(ctx, 20)
				go clusterPendingClusterServiceIDAssignController.Run(ctx, 20)
				go clusterClusterServiceCreateController.Run(ctx, 20)
				go nodePoolClusterServiceCreateController.Run(ctx, 20)
				go externalAuthClusterServiceCreateController.Run(ctx, 20)
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
				go clusterServiceMatchingClusterController.Run(ctx, 20)
				go alwaysSuccessClusterValidationController.Run(ctx, 20)
				go deleteOrphanedCosmosResourcesController.Run(ctx, 20)
				go missingResourceIDController.Run(ctx, 20)
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
				go clusterRequirementsValidAggregatorController.Run(ctx, 20)
				go nodePoolDegradedAggregatorController.Run(ctx, 20)
				go nodePoolRequirementsValidAggregatorController.Run(ctx, 20)
				go externalAuthDegradedAggregatorController.Run(ctx, 20)
				go desiredControlPlaneSizeController.Run(ctx, 20)
				go serviceProviderClusterPropertiesSyncController.Run(ctx, 20)
				go azureRPRegistrationValidationController.Run(ctx, 20)
				go azureClusterResourceGroupExistenceValidationController.Run(ctx, 20)
				go azureClusterManagedIdentitiesExistenceValidationController.Run(ctx, 20)
				go azureVMSizeSupportsEphemeralOSDiskValidationController.Run(ctx, 20)
				go azureNodePoolVMQuotaValidationController.Run(ctx, 20)
				go controlPlaneIdentitiesPermissionsValidationController.Run(ctx, 20)
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
				go clusterCredentialDeletionMarkerController.Run(ctx, 20)
				go clusterChildResourcesCleanupController.Run(ctx, 20)
				go clusterDeletionController.Run(ctx, 20)
				go clusterClusterServiceUpdateDispatchController.Run(ctx, 20)
				go nodePoolClusterServiceUpdateDispatchController.Run(ctx, 20)
				go externalAuthClusterServiceUpdateDispatchController.Run(ctx, 20)
				go operationPhaseMetricsController.Run(ctx, 1) // threadiness=1 required; see operation_phase_metrics_controller.operationPhaseMetricsHandler field comments
				go clusterMetricsController.Run(ctx, 1)
				go clusterVersionMetricsController.Run(ctx, 1)
				go nodePoolMetricsController.Run(ctx, 1)
				go externalAuthMetricsController.Run(ctx, 1)
				go clusterInfoMetricsController.Run(ctx, 1)
				go placementSyncController.Run(ctx, 20)
				go cosmosMigrationController.Run(ctx, 5)
				go virtualMachineResourceSKUsCachedReaderController.Run(ctx, 20)
				go backupScheduleController.Run(ctx, 20)
				go fetchDataPlaneOperatorsManagedIdentitiesInfoController.Run(ctx, 20)
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
