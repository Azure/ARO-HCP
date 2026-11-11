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

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/image-updater/internal/monitor"
)

const (
	defaultProwBaseURL = "https://prow.ci.openshift.org"
	defaultJobRegex    = `^periodic-ci-Azure-ARO-HCP-main-image-updater-`
	defaultInterval    = 5 * time.Minute
	defaultPort        = 8080

	// Per-request timeout for the Prow client, decoupled from the poll
	// interval: the shared client retries up to 3 times, so an interval-sized
	// timeout would let one hung poll block ~3x the interval and trip the
	// scrape-failure alert while the exporter is merely retrying.
	prowRequestTimeout = 30 * time.Second
)

// NewMonitorCommand returns the `image-updater monitor` subcommand: a
// long-running health exporter that polls the Prow API for the image-updater
// periodic jobs and serves run-level health metrics for scraping.
func NewMonitorCommand() *cobra.Command {
	var (
		prowBaseURL   string
		jobRegex      string
		resultsBucket string
		interval      time.Duration
		port          int
	)

	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Export image-updater Prow job health metrics",
		Long: `Monitor polls the Prow API for the image-updater periodic jobs and exposes
Prometheus metrics describing the most recent run of each monitored job:
its result, duration, completion timestamp, consecutive-failure count and a
link to the run's logs. A stalled run (no completion when one was expected)
is visible through the missing-completion timestamps, which is the only way
to detect a hung run.

Serves /metrics for scraping and /healthz for liveness/readiness probes.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ctxErr := cmd.Context().Err(); ctxErr != nil {
				return ctxErr
			}
			return runMonitor(cmd.Context(), slog.Default(), prowBaseURL, jobRegex, resultsBucket, interval, port)
		},
	}

	cmd.Flags().StringVar(&prowBaseURL, "prow-base-url", defaultProwBaseURL, "base URL of the Prow API")
	cmd.Flags().StringVar(&jobRegex, "job-regex", defaultJobRegex, "regular expression matching the Prow job names to monitor")
	cmd.Flags().StringVar(&resultsBucket, "results-bucket", monitor.DefaultResultsBucket, "GCS bucket holding Prow run artifacts, read to classify failure reasons")
	cmd.Flags().DurationVar(&interval, "interval", defaultInterval, "how often to poll the Prow API")
	cmd.Flags().IntVar(&port, "port", defaultPort, "port to serve /metrics and /healthz on")

	return cmd
}

func runMonitor(ctx context.Context, logger *slog.Logger, prowBaseURL, jobRegex, resultsBucket string, interval time.Duration, port int) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be positive, got %s", interval)
	}
	compiled, err := regexp.Compile(jobRegex)
	if err != nil {
		return fmt.Errorf("compile job regex %q: %w", jobRegex, err)
	}

	collector := monitor.NewCollector(
		monitor.NewProwClient(prowBaseURL, prowRequestTimeout),
		compiled,
		logger,
		monitor.NewGCSReasonClient(resultsBucket, prowRequestTimeout),
	)
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		return fmt.Errorf("register collector: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	pollErr := make(chan error, 1)
	go func() {
		pollErr <- collector.Run(ctx, interval)
	}()

	logger.Info("image-updater monitor started",
		"prowBaseURL", prowBaseURL,
		"jobRegex", jobRegex,
		"resultsBucket", resultsBucket,
		"interval", interval.String(),
		"port", port,
	)

	// collector.Run returns nil only on ctx cancellation; both that case and
	// ctx.Done() fall through to a graceful shutdown instead of wrapping a
	// nil error.
	select {
	case <-ctx.Done():
	case err := <-errCh:
		return fmt.Errorf("metrics server: %w", err)
	case err := <-pollErr:
		if err != nil {
			return fmt.Errorf("collector: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
