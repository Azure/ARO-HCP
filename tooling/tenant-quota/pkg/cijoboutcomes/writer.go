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

// maxArtifactAttempts is how many passes may fail to read a run's artifacts
// before the run is stored without them. Three covers a transient outage
// without holding a run back long enough for it to leave the window unstored.
const maxArtifactAttempts = 3

// Writer records CI job outcomes on a timer.
type Writer struct {
	config *config.Config
	logger *slog.Logger
	client *http.Client

	// queued holds runs written but not yet visible to a query, keyed by build
	// id and held with their start time so they can be dropped once the query
	// covers them. artifactAttempts counts failed artifact reads per run, so a
	// transient failure is retried rather than stored as a run with no tests.
	// Both are guarded by queuedMu and are optimisations rather than
	// correctness guarantees: ingestion is idempotent, so losing either to a
	// restart costs repeated work and not duplicate rows.
	queuedMu         sync.Mutex
	queued           map[string]time.Time
	artifactAttempts map[string]artifactAttempt
}

// artifactAttempt counts a run's failed artifact reads, alongside when the run
// started so the entry can be dropped once the run leaves the window - a run
// that is never stored would otherwise leave its count behind for the life of
// the process.
type artifactAttempt struct {
	count     int
	startedAt time.Time
}

func NewWriter(cfg *config.Config, logger *slog.Logger) *Writer {
	return &Writer{
		config:           cfg,
		logger:           logger.With("collector", CollectorName),
		client:           &http.Client{Timeout: 2 * time.Minute},
		queued:           make(map[string]time.Time),
		artifactAttempts: make(map[string]artifactAttempt),
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

	// Runs are gated on the outcome table, which is written last for a run and
	// so marks it complete. Ingestion is idempotent on top of that - see
	// ingestRows - so a gate that is merely stale, rather than wrong, costs a
	// wasted read and not a duplicate row.
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
		pending  []runRows
		names    []ciTestName
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
			// minutes, so a pass has to remember what it queued itself. This is
			// an optimisation, not the correctness guarantee: losing it to a
			// restart costs a repeated read, which ingestion then discards.
			if w.queuedRecently(outcome.BuildID) {
				continue
			}

			detail, err := fetchRunDetail(ctx, w.client, outcome.ProwURL)
			if err != nil {
				// Artifacts are read over the network, so a failure here may be
				// the run's (aborted before its test step uploaded anything) or
				// the network's. The two are not reliably distinguishable, so
				// the run is retried a few times before being stored for its
				// pass or fail alone. Storing it on the first failure would
				// freeze a transient error into a run that permanently appears
				// to have run no tests.
				if w.shouldRetryArtifacts(outcome) {
					w.logger.Warn("Failed to read run artifacts; will retry",
						"release", release, "buildId", outcome.BuildID, "error", err)
					continue
				}
				w.logger.Warn("Failed to read run artifacts; storing run without them",
					"release", release, "buildId", outcome.BuildID, "error", err)
			}
			outcome.SvcCluster = detail.SvcCluster
			outcome.MgmtCluster = detail.MgmtCluster
			outcome.FinishedAt = detail.FinishedAt

			recorded[outcome.BuildID] = struct{}{}
			pending = append(pending, runRows{outcome: outcome, tests: detail.Tests})
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

	if len(pending) == 0 {
		return errors.Join(failures...)
	}

	ingestors, err := w.newIngestors()
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	defer ingestors.close(w.logger)

	// Names are written first and in one batch. They are a dimension shared by
	// every run, so there is no run to key them to, and a name row that arrives
	// before the results referencing it is simply unreferenced.
	//
	// A failure here abandons the pass rather than continuing. Names are
	// derived from a run's artifacts, and a run is only re-read while it is
	// ungated, so writing the runs anyway would gate them with their names
	// missing - leaving test rows that join to nothing. Returning instead
	// leaves every run ungated for the next pass, which re-derives both.
	if err := ingestRows(ctx, ingestors.testNames, names, ""); err != nil {
		return errors.Join(append(failures, err)...)
	}

	// Each run is then written on its own: its tests, then the outcome row that
	// marks it complete. Runs are independent, so one that fails to ingest must
	// not cost the others their rows.
	for _, run := range pending {
		if err := ingestRows(ctx, ingestors.testResults, run.tests, testsTag(run.outcome.BuildID)); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := ingestRows(ctx, ingestors.outcomes, []ciJobOutcome{run.outcome}, runTag(run.outcome.BuildID)); err != nil {
			failures = append(failures, err)
			continue
		}
		w.rememberQueued(run.outcome)
	}
	return errors.Join(failures...)
}

// runRows is everything written for one run.
type runRows struct {
	outcome ciJobOutcome
	tests   []ciTestResult
}

// Rows are tagged so that Kusto itself rejects a second attempt to write them.
// Nothing about the pipeline makes a repeat impossible: a pass that ingests a
// run's tests and then fails to ingest its outcome row leaves the run ungated,
// and a restart discards the in-process record of what was queued while rows
// are still invisible to queries. Both are ordinary, and Kusto has neither a
// primary key nor an upsert, so a duplicate row would be permanent.
func runTag(buildID string) string   { return "run-" + buildID }
func testsTag(buildID string) string { return "tests-" + buildID }

// shouldRetryArtifacts reports whether a run whose artifacts could not be read
// should be left for a later pass, and counts the attempt.
//
// The count is held in memory only. Losing it to a restart costs at most a few
// more attempts, which ingestion deduplicates, whereas persisting it would mean
// a second store to keep consistent with Kusto.
func (w *Writer) shouldRetryArtifacts(outcome ciJobOutcome) bool {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	attempt := w.artifactAttempts[outcome.BuildID]
	attempt.count++
	attempt.startedAt = outcome.StartedAt
	w.artifactAttempts[outcome.BuildID] = attempt
	return attempt.count < maxArtifactAttempts
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

func (w *Writer) rememberQueued(outcome ciJobOutcome) {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	w.queued[outcome.BuildID] = outcome.StartedAt
	delete(w.artifactAttempts, outcome.BuildID)
}

// forgetQueuedBefore drops runs that the query above now covers, so the set
// cannot grow without bound. Runs that left the window without ever being
// stored take their attempt count with them.
func (w *Writer) forgetQueuedBefore(cutoff time.Time) {
	w.queuedMu.Lock()
	defer w.queuedMu.Unlock()
	for buildID, startedAt := range w.queued {
		if startedAt.Before(cutoff) {
			delete(w.queued, buildID)
		}
	}
	for buildID, attempt := range w.artifactAttempts {
		if attempt.startedAt.Before(cutoff) {
			delete(w.artifactAttempts, buildID)
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

// ingestors holds one queued ingestor per destination table, so that a pass
// authenticates once rather than once per run.
type ingestors struct {
	outcomes    *tableIngestor
	testNames   *tableIngestor
	testResults *tableIngestor
}

type tableIngestor struct {
	name     string
	mapping  string
	ingestor *azkustoingest.Ingestion
}

func (w *Writer) newIngestors() (*ingestors, error) {
	settings := w.config.CIJobOutcomes

	built := &ingestors{}
	for _, target := range []struct {
		table config.KustoTableConfig
		into  **tableIngestor
	}{
		{settings.Outcomes, &built.outcomes},
		{settings.TestNames, &built.testNames},
		{settings.TestResults, &built.testResults},
	} {
		ingestor, err := azkustoingest.New(
			azkustodata.NewConnectionStringBuilder(settings.IngestionURI).WithDefaultAzureCredential(),
			azkustoingest.WithDefaultDatabase(settings.Database),
			azkustoingest.WithDefaultTable(target.table.Table),
		)
		if err != nil {
			built.close(w.logger)
			return nil, fmt.Errorf("failed to create ingestor for %s: %w", target.table.Table, err)
		}
		*target.into = &tableIngestor{name: target.table.Table, mapping: target.table.IngestionMapping, ingestor: ingestor}
	}
	return built, nil
}

func (i *ingestors) close(logger *slog.Logger) {
	for _, target := range []*tableIngestor{i.outcomes, i.testNames, i.testResults} {
		if target == nil {
			continue
		}
		if err := target.ingestor.Close(); err != nil {
			logger.Error("Failed to close ingestor", "table", target.name, "error", err)
		}
	}
}

// ingestRows queues rows for one table, the same path the cluster log forwarder
// uses. Ingesting nothing is a no-op rather than an empty batch.
//
// When tag is set the rows are tagged with it and the ingestion is rejected if
// the table already holds an extent carrying that tag, which is what makes a
// repeated write harmless. The rejection is silent by design: it is the
// expected outcome of a retry, not a failure.
func ingestRows[T any](ctx context.Context, target *tableIngestor, rows []T, tag string) error {
	if len(rows) == 0 {
		return nil
	}
	payload, err := encodeRows(rows)
	if err != nil {
		return fmt.Errorf("failed to encode rows for %s: %w", target.name, err)
	}

	options := []azkustoingest.FileOption{
		azkustoingest.IngestionMappingRef(target.mapping, azkustoingest.MultiJSON),
		azkustoingest.FileFormat(azkustoingest.MultiJSON),
	}
	if tag != "" {
		ingestBy := "ingest-by:" + tag
		options = append(options,
			azkustoingest.Tags([]string{ingestBy}),
			azkustoingest.IfNotExists(tag),
		)
	}

	if _, err := target.ingestor.FromReader(ctx, payload, options...); err != nil {
		return fmt.Errorf("failed to queue rows for ingestion into %s: %w", target.name, err)
	}
	return nil
}
