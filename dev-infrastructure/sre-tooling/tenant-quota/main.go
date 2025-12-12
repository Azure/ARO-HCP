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
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/collector"
	"github.com/Azure/ARO-HCP/dev-infrastructure/sre-tooling/tenant-quota/pkg/config"
)

func main() {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "/etc/config/config.yaml"
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Info("Loaded configuration", "collector_count", len(cfg.Collectors))

	// Create metrics collector
	metricsCollector := collector.NewCollector(cfg, logger)

	// Start collection in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go metricsCollector.Start(ctx)

	// Setup HTTP server
	http.Handle("/metrics", promhttp.HandlerFor(
		metricsCollector.Gatherer(),
		promhttp.HandlerOpts{},
	))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		logger.Info("Received shutdown signal")
		cancel()
		os.Exit(0)
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.Info("Starting custom metrics collector", "port", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
