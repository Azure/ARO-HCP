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

package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/clustersserviceregistration"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/datadump"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/lifecycle"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/maestroregistration"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	"github.com/Azure/ARO-HCP/internal/ocm"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
)

const (
	name = "ARO HCP fleet controller"

	healthzAdaptorTimeout  = 20 * time.Second
	httpServerShutdownTime = 31 * time.Second
)

// Manager is the fleet controller manager. It runs informers, leader election,
// and the fleet controllers.
type Manager struct {
	FleetDBClient                database.FleetDBClient
	ClustersServiceClient        ocm.ClusterServiceClientSpec
	MaestroConsumerClientFactory maestroregistration.MaestroConsumerClientFactory
	LeaderElectionLock           resourcelock.Interface
	Region                       string
	HealthzListenAddr            string
	MetricsListenAddr            string
}

// Run starts the fleet controller manager. It serves /healthz and /metrics,
// then runs the controllers under a leader-election lease.
func (m *Manager) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting", "component", name, "commit", version.CommitSHA)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("Run returned"))

	electionChecker := leaderelection.NewLeaderHealthzAdaptor(healthzAdaptorTimeout)

	var healthzServer, metricsServer *http.Server

	errCh := make(chan error, 3)
	wg := sync.WaitGroup{}

	if len(m.HealthzListenAddr) > 0 {
		healthGauge := promauto.With(legacyregistry.Registerer()).NewGauge(prometheus.GaugeOpts{
			Name: "fleet_controller_health", Help: "fleet_controller_health is 1 when healthy",
		})
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := electionChecker.Check(r); err != nil {
				logger.V(1).Info("readiness probe failed", "error", err)
				http.Error(w, "lease not renewed", http.StatusServiceUnavailable)
				healthGauge.Set(0)
				return
			}
			w.WriteHeader(http.StatusOK)
			healthGauge.Set(1)
		})
		healthzServer = &http.Server{Addr: m.HealthzListenAddr, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("healthz server listening", "address", m.HealthzListenAddr)
			err := healthzServer.ListenAndServe()
			if err != nil {
				cancel(fmt.Errorf("healthz server exited: %w", err))
			}
			errCh <- err
		}()
	}

	if len(m.MetricsListenAddr) > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
			legacyregistry.Registerer(),
			promhttp.HandlerFor(prometheus.Gatherers{legacyregistry.DefaultGatherer}, promhttp.HandlerOpts{}),
		))
		metricsServer = &http.Server{Addr: m.MetricsListenAddr, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("metrics server listening", "address", m.MetricsListenAddr)
			err := metricsServer.ListenAndServe()
			if err != nil {
				cancel(fmt.Errorf("metrics server exited: %w", err))
			}
			errCh <- err
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := m.runControllersUnderLeaderElection(ctx, electionChecker)
		cancel(fmt.Errorf("leader election exited"))
		errCh <- err
	}()

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), httpServerShutdownTime)
	defer shutdownCancel()
	_ = shutdownHTTPServer(shutdownCtx, metricsServer, "metrics server")
	_ = shutdownHTTPServer(shutdownCtx, healthzServer, "healthz server")

	wg.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	logger.Info("stopped", "component", name, "commit", version.CommitSHA)
	return errors.Join(errs...)
}

func (m *Manager) runControllersUnderLeaderElection(
	ctx context.Context, electionChecker *leaderelection.HealthzAdaptor,
) error {
	logger := utils.LoggerFromContext(ctx)

	fleetInformers := informers.NewFleetInformers(ctx, m.FleetDBClient.GlobalListers())

	stampInformer, stampLister := fleetInformers.Stamps()
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()

	csRegistrationController := clustersserviceregistration.NewClustersServiceRegistrationController(
		managementClusterInformer,
		stampInformer,
		m.FleetDBClient,
		m.ClustersServiceClient,
		stampLister,
		m.Region,
		base.StampWatchingControllerConfig{},
	)

	maestroRegistrationController := maestroregistration.NewMaestroRegistrationController(
		managementClusterInformer,
		stampInformer,
		m.FleetDBClient,
		m.MaestroConsumerClientFactory,
		stampLister,
		base.StampWatchingControllerConfig{},
	)

	lifecycleController := lifecycle.NewManagementClusterLifecycleController(
		managementClusterInformer,
		m.FleetDBClient,
		base.StampWatchingControllerConfig{},
	)

	dataDumpController := datadump.NewStampDataDumpController(
		stampInformer,
		managementClusterInformer,
		stampLister,
		managementClusterLister,
		base.StampWatchingControllerConfig{CooldownPeriod: 4 * time.Minute},
	)

	leaderElector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          m.LeaderElectionLock,
		LeaseDuration: LeaderElectionLeaseDuration,
		RenewDeadline: LeaderElectionRenewDeadline,
		RetryPeriod:   LeaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("acquired leader election lease; starting informers")

				go fleetInformers.RunWithContext(ctx)

				if !cache.WaitForCacheSync(ctx.Done(), stampInformer.HasSynced, managementClusterInformer.HasSynced) {
					logger.Info("informer caches did not sync; aborting controller startup")
					return
				}

				logger.Info("informer caches synced; starting controllers")
				go csRegistrationController.Run(ctx, 4)
				go maestroRegistrationController.Run(ctx, 4)
				go lifecycleController.Run(ctx, 1)
				go dataDumpController.Run(ctx, 1)
			},
			OnStoppedLeading: func() {
				logger.Info("lost leader election lease")
			},
		},
		ReleaseOnCancel: true,
		WatchDog:        electionChecker,
		Name:            "fleet-controller",
	})
	if err != nil {
		return err
	}
	leaderElector.Run(ctx)
	return nil
}

func shutdownHTTPServer(ctx context.Context, server *http.Server, serverName string) error {
	if server == nil {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info("shutting down server", "server", serverName)
	if err := server.Shutdown(ctx); err != nil {
		logger.Error(err, "failed to shut down server", "server", serverName)
		return err
	}
	logger.Info("server shut down completed", "server", serverName)
	return nil
}
