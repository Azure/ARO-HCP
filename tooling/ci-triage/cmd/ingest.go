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
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/config"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/db"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/ingest"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// NewIngestCommand creates the ingest cobra command.
func NewIngestCommand() *cobra.Command {
	var (
		env    string
		since  string
		retain string
		poll   time.Duration
	)

	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest CI job data from GCS into the database",
		Long:  `Fetches new CI job data from GCS, parses JUnit results, and stores them in SQLite.`,
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

			// Prune old data if --retain is set
			if retain != "" {
				retainStr, err := config.ParseSince(retain)
				if err != nil {
					return fmt.Errorf("invalid --retain: %w", err)
				}
				if retainStr != "" {
					pruned, err := s.PruneOlderThan(ctx, retainStr)
					if err != nil {
						return err
					}
					if pruned > 0 {
						fmt.Fprintf(cmd.OutOrStdout(), "Pruned %d jobs older than %s\n", pruned, retainStr)
					}
				}
			}

			gcsClient := gcs.NewClient(&http.Client{Timeout: 30 * time.Second})
			ing := ingest.New(gcsClient, s, log)

			if poll > 0 {
				ing.PollLoop(ctx, poll, sinceTime)
				return nil
			}

			if env != "" {
				jobTypes := config.JobTypes(env)
				for _, jt := range jobTypes {
					r, err := ing.IngestEnv(ctx, env, jt, sinceTime)
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: %d new, %d skipped, %d errors\n",
						env, jt, r.NewJobs, r.Skipped, r.Errors)
				}
				return nil
			}

			r, err := ing.IngestAll(ctx, sinceTime)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Ingested: %d new, %d skipped, %d errors\n",
				r.NewJobs, r.Skipped, r.Errors)
			return nil
		},
	}

	cmd.Flags().StringVar(&env, "env", "", "environment to ingest (default: all)")
	cmd.Flags().StringVar(&since, "since", "7d", "ingest data since (ISO date or relative: 7d, 24h, 2w)")
	cmd.Flags().StringVar(&retain, "retain", "", "prune data older than this (e.g., 30d, 90d)")
	cmd.Flags().DurationVar(&poll, "poll", 0, "poll interval for continuous ingestion (e.g., 5m)")

	return cmd
}

func parseSinceToTime(s string) (time.Time, error) {
	if len(s) == 10 {
		return time.Parse("2006-01-02", s)
	}
	return time.Parse("2006-01-02T15:04:05", s)
}
