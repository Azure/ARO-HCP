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
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"

	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/gcs"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/ingest"
	"github.com/Azure/ARO-HCP/tooling/ci-triage/internal/store"
)

// defaultDBPath returns the default database path.
func defaultDBPath() string {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "ci-triage", "ci-triage.db")
}

// mustDBPath returns the --db flag value or the default.
func mustDBPath(cmd *cobra.Command) string {
	dbPath, _ := cmd.Flags().GetString("db")
	if dbPath == "" {
		dbPath = defaultDBPath()
	}
	return dbPath
}

// defaultRetention is how long to keep data in the DB. Data older than this
// is pruned during sync to prevent unbounded growth.
const defaultRetention = 30 * 24 * time.Hour // 30 days

// syncIngest fetches fresh data from GCS into the store before querying.
// If env is empty, ingests all environments. sinceTime controls the lookback window.
// Also prunes data older than defaultRetention.
func syncIngest(ctx context.Context, s *store.Store, sinceTime time.Time, env string) error {
	log := logr.FromContextOrDiscard(ctx)

	// Prune old data to prevent unbounded DB growth
	pruneDate := time.Now().UTC().Add(-defaultRetention).Format("2006-01-02")
	pruned, err := s.PruneOlderThan(ctx, pruneDate)
	if err != nil {
		log.V(1).Info("prune warning", "error", err)
	} else if pruned > 0 {
		log.V(1).Info("pruned old data", "deleted", pruned, "before", pruneDate)
	}

	gcsClient := gcs.NewClient(&http.Client{Timeout: 30 * time.Second})
	ing := ingest.New(gcsClient, s, log)

	var r *ingest.IngestResult
	if env != "" {
		r = &ingest.IngestResult{}
		for _, jt := range []string{"periodic", "presubmit"} {
			jr, jerr := ing.IngestEnv(ctx, env, jt, sinceTime)
			if jerr != nil {
				log.V(1).Info("ingestion skipped", "env", env, "jobType", jt, "error", jerr)
				continue
			}
			r.NewJobs += jr.NewJobs
			r.Skipped += jr.Skipped
			r.Errors += jr.Errors
		}
	} else {
		r, err = ing.IngestAll(ctx, sinceTime)
		if err != nil {
			return fmt.Errorf("ingestion failed: %w", err)
		}
	}

	if r.NewJobs > 0 {
		log.V(1).Info("ingested fresh data", "new", r.NewJobs, "skipped", r.Skipped)
	}
	return nil
}
