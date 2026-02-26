// Copyright 2025 Microsoft Corporation
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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/config"
	"github.com/Azure/ARO-HCP/dev-infrastructure/ops-tools/tenant-quota/pkg/tenantquota"
	"github.com/Azure/ARO-HCP/internal/version"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configPath := getEnv("CONFIG_PATH", "/etc/config/config.yaml")
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return err
	}

	logger.Info("Loaded configuration",
		"path", configPath,
		"tenants", len(cfg.Tenants),
		"interval", cfg.GetInterval())

	collector := tenantquota.NewCollector(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go collector.Start(ctx)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(collector.Gatherer(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", healthHandler)
	mux.HandleFunc("/version", versionHandler)

	port := getEnv("PORT", "8080")
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
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

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
