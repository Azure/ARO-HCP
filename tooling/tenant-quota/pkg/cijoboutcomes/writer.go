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

// Package cijoboutcomes records the outcome of each CI job run in Kusto.
//
// Everything else in that database is keyed by the cluster a run used, but
// nothing records whether the run passed, so an alert firing can be found and
// not attributed. Writing outcomes alongside makes the two joinable.
package cijoboutcomes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/Azure/azure-kusto-go/azkustodata"
	"github.com/Azure/azure-kusto-go/azkustodata/kql"
	"github.com/Azure/azure-kusto-go/azkustoingest"

	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const CollectorName = "ci-job-outcomes"

// Writer records CI job outcomes on a timer.
type Writer struct {
	config *config.Config
	logger *slog.Logger
	client *http.Client

	// queued holds runs written but not yet visible to a query, keyed by build
	// id and held with their start time so they can be dropped once the query
	// covers them.
	queuedMu sync.Mutex
	queued   map[string]time.Time
}

func NewWriter(cfg *config.Config, logger *slog.Logger) *Writer {
	return &Writer{
		config: cfg,
		logger: logger.With("collector", CollectorName),
		client: &http.Client{Timeout: 2 * time.Minute},
		queued: make(map[string]time.Time),
	}
}

// Start records outcomes immediately and then on every tick, until the context
// is cancelled. A failed pass is left to the next one: each resumes from what is
// already stored, so nothing is lost by skipping a tick.
func (w *Writer) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()

	settings := w.config.CIJobOutcomes
	interval := settings.GetInterval()
	w.logger.Info("Starting CI job outcome collection",
		"interval", interval,
		"database", settings.Database,
		"table", settings.Table,
		"releases", settings.Releases,
	)

	w.writeOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Stopping CI job outcome collection")
			return
		case <-ticker.C:
			w.writeOnce(ctx)
		}
	}
}

func (w *Writer) writeOnce(ctx context.Context) {
	if err := w.write(ctx); err != nil {
		w.logger.Error("Failed to record CI job outcomes", "error", err)
	}
}

func (w *Writer) write(ctx context.Context) error {
	settings := w.config.CIJobOutcomes

	queryClient, err := azkustodata.New(azkustodata.NewConnectionStringBuilder(settings.ClusterURI).WithDefaultAzureCredential())
	if err != nil {
		return fmt.Errorf("failed to create Kusto client: %w", err)
	}
	defer func() {
		if closeErr := queryClient.Close(); closeErr != nil {
			w.logger.Error("Failed to close Kusto client", "error", closeErr)
		}
	}()

	// Runs are read from a fixed recent window rather than resumed from the
	// newest row stored. A run only appears in Sippy once it completes, and runs
	// complete out of order - a fast provisioning failure is recorded while a
	// longer run that started earlier is still going - so resuming after the
	// newest start time would skip the longer run permanently. Re-reading the
	// window is safe because runs already stored are subtracted by build id, and
	// it self-heals: any gap inside the window is filled by the next pass.
	since := time.Now().UTC().Add(-settings.GetWindow())

	recorded, err := w.recordedBuildIDs(ctx, queryClient, since)
	if err != nil {
		return err
	}
	w.forgetQueuedBefore(since)

	var outcomes []ciJobOutcome
	var failures []error
	for _, release := range settings.Releases {
		runs, err := fetchRuns(ctx, w.client, settings.SippyURI, release, settings.JobFilter, since)
		if err != nil {
			// One unavailable release must not cost the others their rows.
			failures = append(failures, err)
			w.logger.Error("Failed to fetch runs", "release", release, "error", err)
			continue
		}

		var added int
		for _, run := range runs {
			outcome := outcomeFor(run, release)
			if outcome.BuildID == "" {
				continue
			}
			if _, seen := recorded[outcome.BuildID]; seen {
				continue
			}
			// Queued rows stay invisible to the query above for several
			// minutes, so a pass has to remember what it queued itself.
			if w.queuedRecently(outcome.BuildID) {
				continue
			}
			recorded[outcome.BuildID] = struct{}{}
			outcomes = append(outcomes, outcome)
			added++
		}
		w.logger.Info("Fetched runs", "release", release, "since", since.Format(time.RFC3339), "fetched", len(runs), "new", added)
	}

	if len(outcomes) == 0 {
		return errors.Join(failures...)
	}

	payload, err := encodeOutcomes(outcomes)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	if err := w.ingest(ctx, payload, len(outcomes)); err != nil {
		return errors.Join(append(failures, err)...)
	}
	w.rememberQueued(outcomes)
	return errors.Join(failures...)
}

// recordedBuildIDs reports which runs are already stored from the given time on,
// so that re-reading the window does not write them twice.
func (w *Writer) recordedBuildIDs(ctx context.Context, client *azkustodata.Client, since time.Time) (map[string]struct{}, error) {
	settings := w.config.CIJobOutcomes

	statement := kql.New("").AddTable(settings.Table).
		AddLiteral(" | where startedAt >= ").AddDateTime(since).
		AddLiteral(" | distinct buildId")
	dataset, err := client.Query(ctx, settings.Database, statement)
	if err != nil {
		return nil, fmt.Errorf("failed to read recorded runs: %w", err)
	}

	recorded := make(map[string]struct{})
	tables := dataset.Tables()
	if len(tables) == 0 {
		return recorded, nil
	}
	for _, row := range tables[0].Rows() {
		buildID, err := row.StringByName("buildId")
		if err != nil || buildID == "" {
			continue
		}
		recorded[buildID] = struct{}{}
	}
	return recorded, nil
}

func (w *Writer) queuedRecently(buildID string) bool {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	_, queued := w.queued[buildID]
	return queued
}

func (w *Writer) rememberQueued(outcomes []ciJobOutcome) {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	for _, outcome := range outcomes {
		w.queued[outcome.BuildID] = outcome.StartedAt
	}
}

// forgetQueuedBefore drops runs that the query above now covers, so the set
// cannot grow without bound.
func (w *Writer) forgetQueuedBefore(cutoff time.Time) {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	for buildID, startedAt := range w.queued {
		if startedAt.Before(cutoff) {
			delete(w.queued, buildID)
		}
	}
}

// encodeOutcomes renders rows as the multi-line JSON that Kusto ingests.
func encodeOutcomes(outcomes []ciJobOutcome) (*bytes.Buffer, error) {
	payload := &bytes.Buffer{}
	encoder := json.NewEncoder(payload)
	for _, outcome := range outcomes {
		if err := encoder.Encode(outcome); err != nil {
			return nil, fmt.Errorf("failed to encode run %q: %w", outcome.BuildID, err)
		}
	}
	return payload, nil
}

// ingest queues the rows, the same path the cluster log forwarder uses.
func (w *Writer) ingest(ctx context.Context, payload *bytes.Buffer, rows int) error {
	settings := w.config.CIJobOutcomes

	ingestor, err := azkustoingest.New(
		azkustodata.NewConnectionStringBuilder(settings.IngestionURI).WithDefaultAzureCredential(),
		azkustoingest.WithDefaultDatabase(settings.Database),
		azkustoingest.WithDefaultTable(settings.Table),
	)
	if err != nil {
		return fmt.Errorf("failed to create ingestor: %w", err)
	}
	defer func() {
		if closeErr := ingestor.Close(); closeErr != nil {
			w.logger.Error("Failed to close ingestor", "error", closeErr)
		}
	}()

	if _, err := ingestor.FromReader(ctx, payload,
		azkustoingest.IngestionMappingRef(settings.IngestionMapping, azkustoingest.MultiJSON),
		azkustoingest.FileFormat(azkustoingest.MultiJSON),
	); err != nil {
		return fmt.Errorf("failed to queue rows for ingestion: %w", err)
	}

	w.logger.Info("Queued rows for ingestion", "rows", rows, "database", settings.Database, "table", settings.Table)
	return nil
}
