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
		"tables", []string{settings.Outcomes.Table, settings.TestNames.Table, settings.TestResults.Table},
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

	// The run table gates all three: a run's test rows are written in the same
	// pass as its outcome row, so a build id already present means the tests
	// that belong to it are too. Gating each table on itself would let a
	// partially ingested run be completed twice.
	recorded, err := w.recordedBuildIDs(ctx, queryClient, since)
	if err != nil {
		return err
	}
	// Test names are shared across runs and accumulate rather than expiring, so
	// they are gated on the whole table rather than a window.
	storedNames, err := w.recordedTestIDs(ctx, queryClient)
	if err != nil {
		return err
	}
	w.forgetQueuedBefore(since)

	var (
		outcomes []ciJobOutcome
		names    []ciTestName
		results  []ciTestResult
		failures []error
	)
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

			// A run whose artifacts cannot be read is still worth storing for
			// its pass or fail, so enrichment failures are logged and the run
			// kept rather than dropped. The run is recorded either way, so a
			// later pass will not retry it - the artifacts of a finished run do
			// not appear late.
			detail, err := fetchRunDetail(ctx, w.client, outcome.ProwURL)
			if err != nil {
				w.logger.Warn("Failed to read run artifacts; storing run without them",
					"release", release, "buildId", outcome.BuildID, "error", err)
			}
			outcome.SvcCluster = detail.SvcCluster
			outcome.MgmtCluster = detail.MgmtCluster
			outcome.FinishedAt = detail.FinishedAt

			outcomes = append(outcomes, outcome)
			results = append(results, detail.Tests...)
			for _, name := range detail.Names {
				if _, seen := storedNames[name.TestID]; seen {
					continue
				}
				storedNames[name.TestID] = struct{}{}
				names = append(names, name)
			}
			added++
		}
		w.logger.Info("Fetched runs", "release", release, "since", since.Format(time.RFC3339), "fetched", len(runs), "new", added)
	}

	if len(outcomes) == 0 {
		return errors.Join(failures...)
	}

	// Tests are written before the runs that own them. Both orders leave a
	// window where the tables disagree, but a test row without its run is
	// invisible to queries that start from a run, whereas a run whose tests
	// have not landed looks like a run that failed nothing.
	if err := ingestRows(ctx, w, settings.TestNames, names); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if err := ingestRows(ctx, w, settings.TestResults, results); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if err := ingestRows(ctx, w, settings.Outcomes, outcomes); err != nil {
		return errors.Join(append(failures, err)...)
	}
	w.rememberQueued(outcomes)
	return errors.Join(failures...)
}

// recordedBuildIDs reports which runs are already stored from the given time on,
// so that re-reading the window does not write them twice.
func (w *Writer) recordedBuildIDs(ctx context.Context, client *azkustodata.Client, since time.Time) (map[string]struct{}, error) {
	settings := w.config.CIJobOutcomes

	statement := kql.New("").AddTable(settings.Outcomes.Table).
		AddLiteral(" | where startedAt >= ").AddDateTime(since).
		AddLiteral(" | distinct buildId")
	return w.distinctStrings(ctx, client, statement, "buildId", "recorded runs")
}

// recordedTestIDs reports which test names are already stored.
//
// The whole table is read rather than a window of it: names are a dimension
// that accumulates, and a name first seen months ago is still referenced by
// runs arriving now. The table holds one row per distinct test, so this stays
// small even as the join table grows.
func (w *Writer) recordedTestIDs(ctx context.Context, client *azkustodata.Client) (map[string]struct{}, error) {
	settings := w.config.CIJobOutcomes

	statement := kql.New("").AddTable(settings.TestNames.Table).
		AddLiteral(" | distinct testId")
	return w.distinctStrings(ctx, client, statement, "testId", "recorded test names")
}

// distinctStrings runs a query returning a single string column and collects it
// into a set.
func (w *Writer) distinctStrings(ctx context.Context, client *azkustodata.Client, statement *kql.Builder, column, what string) (map[string]struct{}, error) {
	dataset, err := client.Query(ctx, w.config.CIJobOutcomes.Database, statement)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", what, err)
	}

	values := make(map[string]struct{})
	tables := dataset.Tables()
	if len(tables) == 0 {
		return values, nil
	}
	for _, row := range tables[0].Rows() {
		value, err := row.StringByName(column)
		if err != nil || value == "" {
			continue
		}
		values[value] = struct{}{}
	}
	return values, nil
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

// encodeRows renders rows as the multi-line JSON that Kusto ingests.
func encodeRows[T any](rows []T) (*bytes.Buffer, error) {
	payload := &bytes.Buffer{}
	encoder := json.NewEncoder(payload)
	for i, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return nil, fmt.Errorf("failed to encode row %d: %w", i, err)
		}
	}
	return payload, nil
}

// ingestRows queues rows for one table, the same path the cluster log forwarder
// uses. Ingesting nothing is a no-op rather than an empty batch.
func ingestRows[T any](ctx context.Context, w *Writer, table config.KustoTableConfig, rows []T) error {
	if len(rows) == 0 {
		return nil
	}
	payload, err := encodeRows(rows)
	if err != nil {
		return fmt.Errorf("failed to encode rows for %s: %w", table.Table, err)
	}
	return w.ingest(ctx, table, payload, len(rows))
}

func (w *Writer) ingest(ctx context.Context, table config.KustoTableConfig, payload *bytes.Buffer, rows int) error {
	settings := w.config.CIJobOutcomes

	ingestor, err := azkustoingest.New(
		azkustodata.NewConnectionStringBuilder(settings.IngestionURI).WithDefaultAzureCredential(),
		azkustoingest.WithDefaultDatabase(settings.Database),
		azkustoingest.WithDefaultTable(table.Table),
	)
	if err != nil {
		return fmt.Errorf("failed to create ingestor for %s: %w", table.Table, err)
	}
	defer func() {
		if closeErr := ingestor.Close(); closeErr != nil {
			w.logger.Error("Failed to close ingestor", "table", table.Table, "error", closeErr)
		}
	}()

	if _, err := ingestor.FromReader(ctx, payload,
		azkustoingest.IngestionMappingRef(table.IngestionMapping, azkustoingest.MultiJSON),
		azkustoingest.FileFormat(azkustoingest.MultiJSON),
	); err != nil {
		return fmt.Errorf("failed to queue rows for ingestion into %s: %w", table.Table, err)
	}

	w.logger.Info("Queued rows for ingestion", "rows", rows, "database", settings.Database, "table", table.Table)
	return nil
}
