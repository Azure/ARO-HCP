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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/tooling/azutils/subscriptions"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/credentials"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/subscriptionquota"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/tenantquota"
)

func main() {
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

	logger.Info("Loaded configuration",
		"path", configPath,
		"tenants", len(cfg.Tenants),
		"interval", cfg.GetInterval(),
		"hasSubscriptions", cfg.HasSubscriptions())

	credProvider := credentials.NewProvider(logger)

	if err := credProvider.ValidateCredentials(cfg.Tenants); err != nil {
		return fmt.Errorf("credential validation failed: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
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
	go dirCollector.Start(ctx)

	registry := prometheus.NewRegistry()
	registry.MustRegister(dirCollector.GaugeCollectors()...)

	if cfg.HasSubscriptions() {
		subCollector := subscriptionquota.NewCollector(cfg, logger, credProvider, cfg.GetCacheTTL())
		registry.MustRegister(subCollector)
		go subCollector.Start(ctx)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", healthHandler)
	mux.HandleFunc("/version", versionHandler)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		defer utilruntime.HandleCrash()
		logger.Info("Starting HTTP server", "port", port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errChan:
		return err
	case sig := <-sigChan:
		logger.Info("Received signal, shutting down", "signal", sig)
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP server shutdown error", "error", err)
	}

	logger.Info("Shutdown complete")
	return nil
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
