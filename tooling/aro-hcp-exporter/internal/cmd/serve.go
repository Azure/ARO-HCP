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

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/aro-hcp-exporter/internal/metrics"
)

const (
	metricsPath = "/metrics"
)

// NewServeCommand creates the serve command
func NewServeCommand() (*cobra.Command, error) {
	opts := DefaultOptions()

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Prometheus metrics HTTP server",
		Long: `Start the HTTP server that exposes Prometheus metrics.

The server will listen on the specified address and expose metrics at the metrics path.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.Run(cmd.Context())
		},
	}

	if err := BindOptions(opts, cmd); err != nil {
		return nil, fmt.Errorf("failed to bind options: %w", err)
	}

	return cmd, nil
}

func (o *CompletedOptions) Run(ctx context.Context) error {
	logger := logr.FromContextOrDiscard(ctx)

	handler := promhttp.HandlerFor(o.Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})

	// Create HTTP mux
	mux := http.NewServeMux()
	mux.Handle(metricsPath, handler)

	// Create HTTP server
	server := &http.Server{
		Addr:         o.ListenAddress,
		Handler:      mux,
		ReadTimeout:  30 * time.Second, // Increased timeout for Azure API calls
		WriteTimeout: 30 * time.Second, // Increased timeout for Azure API calls
	}

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		logger.Info("Starting server", "address", o.ListenAddress)
		logger.Info("Metrics available", "url", fmt.Sprintf("http://%s%s", o.ListenAddress, metricsPath))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server error: %w", err)
		}
	}()

	for _, collector := range o.Collectors {
		logger.Info("Starting collector", "name", collector.Name())
		go collectLoop(ctx, collector, o.CollectionInterval)
	}

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		logger.Info("Shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
		logger.Info("Server shutdown completed")
		return nil
	case err := <-errChan:
		return err
	}
}

func collectLoop(ctx context.Context, collector metrics.CachingCollector, collectionInterval time.Duration) {
	logger := logr.FromContextOrDiscard(ctx)
	for {
		select {
		case <-ctx.Done():
			logger.Info("context cancelled, stopping collector")
			return
		default:
			start := time.Now()
			logger.Info("Collecting metrics", "name", collector.Name())
			collector.CollectMetricValues(ctx)
			duration := time.Since(start)
			logger.V(2).Info("collected metrics", "name", collector.Name(), "collector_runtime_seconds", duration.Seconds())
			sleepDuration := collectionInterval - duration
			if sleepDuration > 0 {
				time.Sleep(sleepDuration)
			}
		}
	}
}
