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
	"time"

	_ "k8s.io/component-base/metrics/prometheus/clientgo"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	kuberuntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/Azure/ARO-HCP/internal/database/informers"
	sharedleaderelection "github.com/Azure/ARO-HCP/internal/leaderelection"
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/apply_desire"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/read_desire_manager"
)

// Run is the binary's main loop. It serves /healthz and /metrics, then runs
// the controllers under a leader-election lease. Run returns when ctx is
// cancelled (signal handler) or when leader election exits.
func (o *Options) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("%s (%s) starting on management cluster %q",
		AppShortDescriptionName, version.CommitSHA, o.ManagementCluster.String()))

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("Run returned"))

	kuberuntime.ReallyCrash = o.ExitOnPanic

	electionChecker := leaderelection.NewLeaderHealthzAdaptor(20 * time.Second)

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

	if o.HealthzServerListenAddress != "" {
		healthGauge := promauto.With(o.metricsRegisterer()).NewGauge(prometheus.GaugeOpts{
			Name: "kube_applier_health", Help: "kube_applier_health is 1 when healthy",
		})
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			if err := electionChecker.Check(r); err != nil {
				logger.Error(err, "readiness probe failed")
				http.Error(w, "lease not renewed", http.StatusServiceUnavailable)
				healthGauge.Set(0)
				return
			}
			w.WriteHeader(http.StatusOK)
			healthGauge.Set(1)
		})
		server := &http.Server{Addr: o.HealthzServerListenAddress, Handler: mux}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("healthz server exited"))
			defer wg.Done()
			defer kuberuntime.HandleCrash()
			if err := runHTTPServer(ctx, server, "healthz server"); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}

	if o.MetricsServerListenAddress != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
			o.metricsRegisterer(),
			promhttp.HandlerFor(prometheus.Gatherers{o.metricsGatherer()}, promhttp.HandlerOpts{}),
		))
		server := &http.Server{Addr: o.MetricsServerListenAddress, Handler: mux}
		wg.Add(1)
		go func() {
			defer cancel(fmt.Errorf("metrics server exited"))
			defer wg.Done()
			defer kuberuntime.HandleCrash()
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
		defer kuberuntime.HandleCrash()
		if err := o.runControllersUnderLeaderElection(ctx, electionChecker); err != nil {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}
	}()

	wg.Wait()
	logger.Info(fmt.Sprintf("%s (%s) stopped", AppShortDescriptionName, version.CommitSHA))
	return errors.Join(errs...)
}

// runControllersUnderLeaderElection wires the two controllers and runs them
// inside the leader-election callback. Informers are started inside the
// callback too: a non-leader replica should not be reading Cosmos.
func (o *Options) runControllersUnderLeaderElection(
	ctx context.Context, electionChecker *leaderelection.HealthzAdaptor,
) error {
	logger := utils.LoggerFromContext(ctx)

	// In the per-management-cluster container model, KubeApplierDBClient is already
	// scoped to this pod's MC; Listers() lists exactly that container's *Desires.
	listers := o.KubeApplierDBClient.Listers()

	applyInformer := informers.NewApplyDesireInformer(listers.ApplyDesires())
	readInformer := informers.NewReadDesireInformer(listers.ReadDesires())

	collector := newDesireCollector(
		applyInformer.GetStore(),
		readInformer.GetStore(),
		o.metricsRegisterer(),
	)

	applyCtl, err := apply_desire.NewApplyDesireController(applyInformer, o.DynamicClient, o.KubeApplierDBClient, apply_desire.Config{})
	if err != nil {
		return fmt.Errorf("apply controller: %w", err)
	}
	readMgr, err := read_desire_manager.NewReadDesireInformerManagingController(readInformer, o.DynamicClient, o.KubeApplierDBClient, read_desire_manager.Config{})
	if err != nil {
		return fmt.Errorf("read manager: %w", err)
	}

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          o.LeaderElectionLock,
		LeaseDuration: sharedleaderelection.RecommendedLeaseDuration,
		RenewDeadline: sharedleaderelection.RecommendedRenewDeadline,
		RetryPeriod:   sharedleaderelection.RecommendedRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("acquired leader election lease; starting informers and controllers")
				go applyInformer.RunWithContext(ctx)
				go readInformer.RunWithContext(ctx)

				if !cache.WaitForCacheSync(ctx.Done(),
					applyInformer.HasSynced, readInformer.HasSynced) {
					logger.Info("informer caches did not sync; aborting controller startup")
					return
				}

				go collector.Run(ctx)
				go applyCtl.Run(ctx, threadsApply)
				go readMgr.Run(ctx, threadsReadManager)
			},
			OnStoppedLeading: func() {
				logger.Info("lost leader election lease")
			},
		},
		ReleaseOnCancel: true,
		WatchDog:        electionChecker,
		Name:            "kube-applier",
	}

	sharedleaderelection.LogLeaseProperties(logger, leaderElectionConfig)

	le, err := leaderelection.NewLeaderElector(leaderElectionConfig)
	if err != nil {
		return err
	}

	le.Run(ctx)
	return nil
}

func (o *Options) metricsRegisterer() prometheus.Registerer {
	if o.MetricsRegisterer != nil {
		return o.MetricsRegisterer
	}
	return legacyregistry.Registerer()
}

func (o *Options) metricsGatherer() prometheus.Gatherer {
	if o.MetricsGatherer != nil {
		return o.MetricsGatherer
	}
	return legacyregistry.DefaultGatherer
}

// runHTTPServer runs the server and shuts it down when ctx is cancelled.
// It returns nil if the server was shut down cleanly (http.ErrServerClosed),
// or the underlying error if ListenAndServe failed for another reason.
func runHTTPServer(ctx context.Context, server *http.Server, name string) error {
	logger := utils.LoggerFromContext(ctx)

	done := make(chan struct{})
	defer close(done)
	go func() {
		defer kuberuntime.HandleCrash()
		select {
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
			defer shutdownCancel()
			logger.Info(fmt.Sprintf("shutting down %s", name))
			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Error(err, fmt.Sprintf("failed to shut down %s", name))
			} else {
				logger.Info(fmt.Sprintf("%s shut down completed", name))
			}
		case <-done:
		}
	}()

	logger.Info(fmt.Sprintf("%s listening on %s", name, server.Addr))
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
