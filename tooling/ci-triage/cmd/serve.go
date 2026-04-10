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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/analysis"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/db"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/ingest"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/render"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// NewServeCommand creates the serve cobra command.
func NewServeCommand() *cobra.Command {
	var (
		listen string
		poll   time.Duration
		since  string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run as HTTP server with continuous ingestion",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			log := logr.FromContextOrDiscard(ctx)
			dbPath := mustDBPath(cmd)

			sinceStr, err := config.ParseSince(since)
			if err != nil {
				return err
			}
			if sinceStr == "" {
				sinceStr = time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
			}
			sinceTime, err := parseSinceToTime(sinceStr)
			if err != nil {
				return err
			}

			database, err := db.OpenAndMigrate(dbPath)
			if err != nil {
				return err
			}
			defer database.Close()

			s := store.New(database)
			gcsClient := gcs.NewClient(&http.Client{Timeout: 30 * time.Second})
			ing := ingest.New(gcsClient, s, log)

			// Start ingestion loop in background
			go ing.PollLoop(ctx, poll, sinceTime)

			// HTTP API
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v1/summary", handleSummary(s))
			mux.HandleFunc("GET /api/v1/failures/{env}", handleFailures(s))
			mux.HandleFunc("GET /api/v1/pr/{number}", handlePR(s))
			mux.HandleFunc("GET /healthz", handleHealth(database))

			server := &http.Server{
				Addr:    listen,
				Handler: mux,
			}

			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				server.Shutdown(shutdownCtx)
			}()

			log.Info("starting HTTP server", "listen", listen)
			if err := server.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	cmd.Flags().DurationVar(&poll, "poll", 5*time.Minute, "ingestion poll interval")
	cmd.Flags().StringVar(&since, "since", "7d", "ingest data since")

	return cmd
}

func handleSummary(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		if since == "" {
			since = time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
		} else {
			parsed, err := config.ParseSince(since)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			since = parsed
		}
		until := r.URL.Query().Get("until")

		data, err := analysis.Summary(r.Context(), s, since, until)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		format := r.URL.Query().Get("format")
		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		} else {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, render.Summary(data))
		}
	}
}

func handleFailures(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		env := r.PathValue("env")
		since := r.URL.Query().Get("since")
		if since == "" {
			since = time.Now().UTC().Add(-7 * 24 * time.Hour).Format("2006-01-02")
		} else {
			parsed, err := config.ParseSince(since)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			since = parsed
		}
		until := r.URL.Query().Get("until")

		data, err := analysis.Failures(r.Context(), s, env, since, until)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		format := r.URL.Query().Get("format")
		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		} else {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, render.Evidence(data))
		}
	}
}

func handlePR(s *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prStr := r.PathValue("number")
		prNumber, err := strconv.Atoi(prStr)
		if err != nil {
			http.Error(w, "invalid PR number", http.StatusBadRequest)
			return
		}

		data, err := analysis.PR(r.Context(), s, prNumber)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		format := r.URL.Query().Get("format")
		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(data)
		} else {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, render.PR(data))
		}
	}
}

func handleHealth(database interface{ Ping() error }) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := database.Ping(); err != nil {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}
}
