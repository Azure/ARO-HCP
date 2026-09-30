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
	clusterbackups "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	internalazure "github.com/Azure/ARO-HCP/internal/azure"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/billingcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/corecosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/kubeappliercosmosstorage"
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
	ResourcesDBClient                  corecosmosstorage.ResourcesDBClient
	BillingDBClient                    billingcosmosstorage.BillingDBClient
	FleetDBClient                      fleetcosmosstorage.FleetDBClient
	KubeApplierDBClients               kubeappliercosmosstorage.KubeApplierDBClients
	ClustersServiceClient              ocm.ClusterServiceClientSpec
	MetricsRegisterer                  prometheus.Registerer
	MetricsGatherer                    prometheus.Gatherer
	MetricsServerListenAddress         string
	MetricsServerListener              net.Listener
	HealthzServerListenAddress         string
	TracerProviderShutdownFunc         func(context.Context) error
	MaestroSourceEnvironmentIdentifier string
	FPAClientBuilder                   azureclient.FirstPartyApplicationClientBuilder
	// HasRealFPA indicates the backend runs against a real First Party Application rather than the
	// insecure MI mock. Controllers that create Azure resources only a real FPA can create (e.g.
	// deny assignments) are disabled when this is false (dev/int environments).
	HasRealFPA                                          bool
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

// runBackendControllersUnderLeaderElection runs the backend controllers under
// a leader election loop.
func (b *Backend) runBackendControllersUnderLeaderElection(ctx context.Context, electionChecker *leaderelection.HealthzAdaptor) error {
	logger := utils.LoggerFromContext(ctx)

	controllerContext := b.newControllerContext(ctx)
	controllers, err := instantiateControllers(newControllerRegistry(), controllerContext)
	if err != nil {
		return err
	}

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          b.options.LeaderElectionLock,
		LeaseDuration: sharedleaderelection.RecommendedLeaseDuration,
		RenewDeadline: sharedleaderelection.RecommendedRenewDeadline,
		RetryPeriod:   sharedleaderelection.RecommendedRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				startControllers(ctx, controllers, controllerContext)
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
