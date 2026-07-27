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

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/amwscaling"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/base"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/clustersserviceregistration"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/datadump"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/lifecycle"
	"github.com/Azure/ARO-HCP/fleet/pkg/controllers/maestroregistration"
	"github.com/Azure/ARO-HCP/internal/database"
	"github.com/Azure/ARO-HCP/internal/database/informers"
	sharedleaderelection "github.com/Azure/ARO-HCP/internal/leaderelection"
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
	AMWWorkspaceResourceIDs      []string
	AMWScalingPollInterval       time.Duration
	AzureCredential              azcore.TokenCredential
	AzureClientOptions           *policy.ClientOptions
}

// Run starts the fleet controller manager. It serves /healthz and /metrics,
// then runs the controllers under a leader-election lease.
func (m *Manager) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info("starting", "component", name, "commit", version.CommitSHA)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("Run returned"))

	electionChecker := leaderelection.NewLeaderHealthzAdaptor(healthzAdaptorTimeout)

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

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
		server := &http.Server{Addr: m.HealthzListenAddr, Handler: mux}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("healthz server exited"))
			defer wg.Done()
			defer utilruntime.HandleCrash()
			if err := runHTTPServer(ctx, server, "healthz server"); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	if len(m.MetricsListenAddr) > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
			legacyregistry.Registerer(),
			promhttp.HandlerFor(prometheus.Gatherers{legacyregistry.DefaultGatherer}, promhttp.HandlerOpts{}),
		))
		server := &http.Server{Addr: m.MetricsListenAddr, Handler: mux}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("metrics server exited"))
			defer wg.Done()
			defer utilruntime.HandleCrash()
			if err := runHTTPServer(ctx, server, "metrics server"); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer cancel(fmt.Errorf("leader election exited"))
		defer wg.Done()
		defer utilruntime.HandleCrash()
		if err := m.runControllersUnderLeaderElection(ctx, electionChecker); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}()

	wg.Wait()
	logger.Info("stopped", "component", name, "commit", version.CommitSHA)
	return errors.Join(errs...)
}

func (m *Manager) runControllersUnderLeaderElection(
	ctx context.Context, electionChecker *leaderelection.HealthzAdaptor,
) error {
	logger := utils.LoggerFromContext(ctx)

	fleetInformers := informers.NewFleetInformers(ctx, m.FleetDBClient.GlobalListers(), m.FleetDBClient)

	stampInformer, stampLister := fleetInformers.Stamps()
	managementClusterInformer, managementClusterLister := fleetInformers.ManagementClusters()

	csRegistrationController := clustersserviceregistration.NewClustersServiceRegistrationController(
		managementClusterInformer,
		stampInformer,
		m.FleetDBClient,
		m.ClustersServiceClient,
		stampLister,
		m.Region,
		base.StampWatchingControllerConfig{Cooldown: base.DefaultRegistrationAwareCooldown(managementClusterLister)},
	)

	maestroRegistrationController := maestroregistration.NewMaestroRegistrationController(
		managementClusterInformer,
		stampInformer,
		m.FleetDBClient,
		m.MaestroConsumerClientFactory,
		stampLister,
		base.StampWatchingControllerConfig{Cooldown: base.DefaultRegistrationAwareCooldown(managementClusterLister)},
	)

	lifecycleController := lifecycle.NewManagementClusterLifecycleController(
		managementClusterInformer,
		m.FleetDBClient,
		base.StampWatchingControllerConfig{Cooldown: base.DefaultRegistrationAwareCooldown(managementClusterLister)},
	)

	dataDumpController := datadump.NewStampDataDumpController(
		stampInformer,
		managementClusterInformer,
		stampLister,
		managementClusterLister,
		base.StampWatchingControllerConfig{CooldownPeriod: 4 * time.Minute},
	)

	amwScalingController := amwscaling.NewController(
		m.AMWScalingPollInterval,
		m.AMWWorkspaceResourceIDs,
		m.AzureCredential,
		m.AzureClientOptions,
	)

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          m.LeaderElectionLock,
		LeaseDuration: sharedleaderelection.RecommendedLeaseDuration,
		RenewDeadline: sharedleaderelection.RecommendedRenewDeadline,
		RetryPeriod:   sharedleaderelection.RecommendedRetryPeriod,
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
				go amwScalingController.Run(ctx)
			},
			OnStoppedLeading: func() {
				logger.Info("lost leader election lease")
			},
		},
		ReleaseOnCancel: true,
		WatchDog:        electionChecker,
		Name:            "fleet-controller",
	}

	sharedleaderelection.LogLeaseProperties(logger, leaderElectionConfig)

	leaderElector, err := leaderelection.NewLeaderElector(leaderElectionConfig)
	if err != nil {
		return err
	}
	leaderElector.Run(ctx)
	return nil
}

// runHTTPServer runs the server and shuts it down when ctx is cancelled.
// It returns nil if the server was shut down cleanly (http.ErrServerClosed),
// or the underlying error if ListenAndServe failed for another reason.
func runHTTPServer(ctx context.Context, server *http.Server, name string) error {
	logger := utils.LoggerFromContext(ctx)

	done := make(chan struct{})
	defer close(done)
	go func() {
		defer utilruntime.HandleCrash()
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), httpServerShutdownTime)
			defer shutdownCancel()
			logger.Info("shutting down server", "server", name)
			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Error(err, "failed to shut down server", "server", name)
			} else {
				logger.Info("server shut down completed", "server", name)
			}
		case <-done:
		}
	}()

	logger.Info("server listening", "server", name, "address", server.Addr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
