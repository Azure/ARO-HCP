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
	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/apply_desire"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/delete_desire"
	"github.com/Azure/ARO-HCP/kube-applier/pkg/controllers/read_desire_manager"
)

// Run is the binary's main loop. It serves /healthz and /metrics, then runs
// the controllers under a leader-election lease. Run returns when ctx is
// cancelled (signal handler) or when leader election exits.
func (o *Options) Run(ctx context.Context) error {
	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("%s (%s) starting on management cluster %q",
		AppShortDescriptionName, version.CommitSHA, o.ManagementCluster))

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(fmt.Errorf("Run returned"))

	kuberuntime.ReallyCrash = o.ExitOnPanic

	electionChecker := leaderelection.NewLeaderHealthzAdaptor(20 * time.Second)

	var healthzServer, metricsServer *http.Server
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 31*time.Second)
		defer shutdownCancel()
		_ = shutdownHTTPServer(shutdownCtx, metricsServer, "metrics server")
		_ = shutdownHTTPServer(shutdownCtx, healthzServer, "healthz server")
	}()

	errCh := make(chan error, 3)
	wg := sync.WaitGroup{}

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
		healthzServer = &http.Server{Addr: o.HealthzServerListenAddress, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("healthz server listening on %s", o.HealthzServerListenAddress))
			errCh <- healthzServer.ListenAndServe()
		}()
	}

	if o.MetricsServerListenAddress != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.InstrumentMetricHandler(
			o.metricsRegisterer(),
			promhttp.HandlerFor(prometheus.Gatherers{o.metricsGatherer()}, promhttp.HandlerOpts{}),
		))
		metricsServer = &http.Server{Addr: o.MetricsServerListenAddress, Handler: mux}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("metrics server listening on %s", o.MetricsServerListenAddress))
			errCh <- metricsServer.ListenAndServe()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := o.runControllersUnderLeaderElection(ctx, electionChecker)
		cancel(fmt.Errorf("leader election exited"))
		errCh <- err
	}()

	<-ctx.Done()
	wg.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	logger.Info(fmt.Sprintf("%s (%s) stopped", AppShortDescriptionName, version.CommitSHA))
	return errors.Join(errs...)
}

// runControllersUnderLeaderElection wires the three controllers and runs them
// inside the leader-election callback. Informers are started inside the
// callback too: a non-leader replica should not be reading Cosmos.
func (o *Options) runControllersUnderLeaderElection(
	ctx context.Context, electionChecker *leaderelection.HealthzAdaptor,
) error {
	logger := utils.LoggerFromContext(ctx)

	// PartitionListers gives us a single-partition view scoped to this pod's
	// management cluster, so the informer relists never touch other partitions.
	partitionListers := o.KubeApplierDBClient.PartitionListers(o.ManagementCluster)

	applyInformer := informers.NewApplyDesireInformer(partitionListers.ApplyDesires())
	deleteInformer := informers.NewDeleteDesireInformer(partitionListers.DeleteDesires())
	readInformer := informers.NewReadDesireInformer(partitionListers.ReadDesires())

	// Each controller takes its peer interface off the same per-management-cluster
	// KubeApplierCRUD so status replaces use the correct (cluster, [nodepool])
	// parent for every desire they touch.
	kubeApplierCRUD := o.KubeApplierDBClient.KubeApplier(o.ManagementCluster)

	applyCtl, err := apply_desire.NewApplyDesireController(applyInformer, o.DynamicClient, kubeApplierCRUD, apply_desire.Config{})
	if err != nil {
		return fmt.Errorf("apply controller: %w", err)
	}
	deleteCtl, err := delete_desire.NewDeleteDesireController(deleteInformer, o.DynamicClient, kubeApplierCRUD, delete_desire.Config{})
	if err != nil {
		return fmt.Errorf("delete controller: %w", err)
	}
	readMgr, err := read_desire_manager.NewReadDesireInformerManagingController(readInformer, o.DynamicClient, kubeApplierCRUD, read_desire_manager.Config{})
	if err != nil {
		return fmt.Errorf("read manager: %w", err)
	}

	le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          o.LeaderElectionLock,
		LeaseDuration: leaderElectionLeaseDuration,
		RenewDeadline: leaderElectionRenewDeadline,
		RetryPeriod:   leaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("acquired leader election lease; starting informers and controllers")
				go applyInformer.RunWithContext(ctx)
				go deleteInformer.RunWithContext(ctx)
				go readInformer.RunWithContext(ctx)

				if !cache.WaitForCacheSync(ctx.Done(),
					applyInformer.HasSynced, deleteInformer.HasSynced, readInformer.HasSynced) {
					logger.Info("informer caches did not sync; aborting controller startup")
					return
				}

				go applyCtl.Run(ctx, threadsApply)
				go deleteCtl.Run(ctx, threadsDelete)
				go readMgr.Run(ctx, threadsReadManager)
			},
			OnStoppedLeading: func() {
				logger.Info("lost leader election lease")
			},
		},
		ReleaseOnCancel: true,
		WatchDog:        electionChecker,
		Name:            "kube-applier",
	})
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

func shutdownHTTPServer(ctx context.Context, server *http.Server, name string) error {
	if server == nil {
		return nil
	}
	logger := utils.LoggerFromContext(ctx)
	logger.Info(fmt.Sprintf("shutting down %s", name))
	if err := server.Shutdown(ctx); err != nil {
		logger.Error(err, fmt.Sprintf("failed to shut down %s", name))
		return err
	}
	logger.Info(fmt.Sprintf("%s shut down completed", name))
	return nil
}
