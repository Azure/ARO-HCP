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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "k8s.io/component-base/metrics/prometheus/workqueue"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/klog/v2"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/tooling/azutils/subscriptions"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/cijoboutcomes"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
	prowmetrics "github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/prow"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/resourcegroups"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/subscriptionquota"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/tenantquota"
)

func main() {
	if len(os.Args) > 1 {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runCommand(ctx, os.Args[1:], os.Stdout, os.Stderr, logger); err != nil {
			logger.Error("Command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configPath := envOrDefault("CONFIG_PATH", "/etc/config/config.yaml")
	port := envOrDefault("PORT", "8080")

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return err
	}
	configurePanicHandling(cfg.ExitOnPanic)
	klog.SetLogger(logr.FromSlogHandler(logger.Handler()))

	logger.Info("Loaded configuration",
		"path", configPath,
		"tenants", len(cfg.Tenants),
		"interval", cfg.GetInterval(),
		"hasSubscriptions", cfg.HasSubscriptions())

	credProvider := credentials.NewProvider(logger)

	if err := credProvider.ValidateCredentials(cfg.Tenants); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = utils.ContextWithLogger(ctx, logr.FromSlogHandler(logger.Handler()))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := credProvider.StartWatching(ctx, cfg.Tenants); err != nil {
		return fmt.Errorf("start credential watchers: %w", err)
	}

	if cfg.HasSubscriptions() {
		if err := resolveSubscriptionIDs(ctx, cfg, credProvider, logger); err != nil {
			return fmt.Errorf("subscription ID resolution failed: %w", err)
		}
	}

	dirCollector := tenantquota.NewCollector(cfg, logger, credProvider)
	go runCollector(ctx, dirCollector.Start)

	registry := prometheus.NewRegistry()
	registry.MustRegister(dirCollector.GaugeCollectors()...)

	if cfg.HasSubscriptions() {
		subCollector := subscriptionquota.NewCollector(cfg, logger, credProvider, cfg.GetCacheTTL())
		registry.MustRegister(subCollector)
		go runCollector(ctx, subCollector.Start)

		e2eRGCollector := resourcegroups.NewCollector(resourcegroups.E2ECollectorConfig, cfg, logger, credProvider)
		registry.MustRegister(e2eRGCollector)
		go runCollector(ctx, e2eRGCollector.Start)
	}

	if cfg.Prow.Enabled {
		prowCollector := prowmetrics.NewCollector(cfg, logger)
		registry.MustRegister(prowCollector)
		go runCollector(ctx, prowCollector.Start)
	}

	var startController func(context.Context)
	if cfg.CIJobOutcomes.Enabled {
		writer := cijoboutcomes.NewWriter(cfg, logger)
		writer.RegisterMetrics(registry)
		startController = writer.Start
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           newHTTPHandler(registry),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return serve(ctx, cancel, logger, server, startController)
}

// Start normally blocks until cancellation, but returns after recovering a
// nonfatal panic. Restart with a delay so collection cannot silently stop or spin.
func runCollector(ctx context.Context, start func(context.Context)) {
	defer utilruntime.HandleCrashWithContext(ctx)
	wait.UntilWithContext(ctx, start, time.Second)
}

var panicMetricsOnce sync.Once

func configurePanicHandling(exitOnPanic bool) {
	utilruntime.ReallyCrash = exitOnPanic
	panicMetricsOnce.Do(func() {
		utilruntime.PanicHandlers = append(utilruntime.PanicHandlers, utils.IncrementPanicMetrics)
	})
}

func newHTTPHandler(registry *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.Gatherers{registry, legacyregistry.DefaultGatherer}, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", healthHandler)
	mux.HandleFunc("/version", versionHandler)
	return mux
}

// cancel also stops the collectors when the HTTP server fails. Controller and
// HTTP shutdown share one grace period; pending CI work is not drained.
func serve(ctx context.Context, cancel context.CancelFunc, logger *slog.Logger, server *http.Server, startController func(context.Context)) error {
	controllerDone := make(chan struct{})
	if startController == nil {
		close(controllerDone)
	} else {
		go func() {
			defer utilruntime.HandleCrashWithContext(ctx)
			defer close(controllerDone)
			startController(ctx)
		}()
	}

	errChan := make(chan error, 1)
	go func() {
		defer utilruntime.HandleCrashWithContext(ctx)
		defer close(errChan)
		logger.Info("Starting HTTP server", "address", server.Addr)
		errChan <- server.ListenAndServe()
	}()

	var serveErr error
	select {
	case serveErr = <-errChan:
		if serveErr == http.ErrServerClosed {
			serveErr = nil
		}
	case <-ctx.Done():
		logger.Info("Received cancellation, shutting down")
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP server shutdown error", "error", err)
	}
	select {
	case <-controllerDone:
	case <-shutdownCtx.Done():
		logger.Warn("CI controller shutdown timed out", "error", shutdownCtx.Err())
	}

	logger.Info("Shutdown complete")
	return serveErr
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"commitSHA": version.CommitSHA,
	})
}

func resolveSubscriptionIDs(ctx context.Context, cfg *config.Config,
	credProvider *credentials.Provider, logger *slog.Logger) error {

	for i := range cfg.Tenants {
		tenant := &cfg.Tenants[i]
		if len(tenant.Subscriptions) == 0 {
			continue
		}

		cred, err := credProvider.GetCredential(*tenant)
		if err != nil {
			return fmt.Errorf("tenant %s: get credential: %w", tenant.GetDisplayName(), err)
		}

		names := make([]string, len(tenant.Subscriptions))
		for j, sub := range tenant.Subscriptions {
			names[j] = sub.Name
		}

		nameToID, err := subscriptions.ResolveByName(ctx, cred, names)
		if err != nil {
			return fmt.Errorf("tenant %s: %w", tenant.GetDisplayName(), err)
		}

		for j := range tenant.Subscriptions {
			sub := &tenant.Subscriptions[j]
			sub.SubscriptionID = nameToID[sub.Name]
			logger.Info("Resolved subscription ID",
				"tenant", tenant.GetDisplayName(),
				"subscription", sub.Name,
				"subscriptionId", sub.SubscriptionID)
		}
	}
	return nil
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
