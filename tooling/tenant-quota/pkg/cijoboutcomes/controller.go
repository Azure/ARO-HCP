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

package cijoboutcomes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	"github.com/Azure/ARO-HCP/tooling/hcpctl/pkg/snapshot"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const CIJobOutcomesControllerName = "ci-job-outcomes"
const CollectorName = CIJobOutcomesControllerName

type BuildID string

func (id BuildID) AddLoggerValues(logger logr.Logger) logr.Logger {
	return logger.WithValues("buildId", string(id))
}

var _ utils.LoggableKey = BuildID("")

type runMetadata struct {
	run     sippyRun
	release string
}

// Writer owns a queue of build IDs, not artifact payloads. All caches may be
// evicted without losing work: Sippy recovers metadata and Kusto owns deduplication.
type Writer struct {
	name       string
	config     *config.Config
	logger     logr.Logger
	client     *http.Client
	queue      workqueue.TypedRateLimitingInterface[BuildID]
	batches    *ttlCache[string, struct{}]
	metadata   *ttlCache[BuildID, runMetadata]
	metrics    writerMetrics
	lastRepair map[string]time.Time // discovery goroutine only
	dryRun     bool
	report     func(BatchReport)

	initialize         func(context.Context) error
	initializeReadOnly func(context.Context) error
	closeClients       func()
	cursor             func(context.Context, string) (time.Time, error)
	tagExists          func(context.Context, batchKind, BuildID) (bool, error)
	submit             func(context.Context, batchKind, BuildID, *bytes.Buffer) error
	jobDetail          func(context.Context, *http.Client, string) (runDetail, error)
	e2eRows            func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error)
	observabilityRows  func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error)
}

func NewWriter(cfg *config.Config, logger *slog.Logger) *Writer {
	w := &Writer{
		name: CIJobOutcomesControllerName, config: cfg,
		logger: logr.FromSlogHandler(logger.Handler()).WithValues(utils.LogValues{}.AddControllerName(CIJobOutcomesControllerName)...),
		client: &http.Client{Timeout: 2 * time.Minute},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[BuildID](time.Second, 5*time.Minute),
			workqueue.TypedRateLimitingQueueConfig[BuildID]{Name: CIJobOutcomesControllerName}),
		batches:  newTTLCache[string, struct{}](cfg.CIJobOutcomes.GetCacheSize(), cfg.CIJobOutcomes.GetCacheTTL()),
		metadata: newTTLCache[BuildID, runMetadata](cfg.CIJobOutcomes.GetCacheSize(), cfg.CIJobOutcomes.GetCacheTTL()),
		metrics:  newWriterMetrics(), lastRepair: map[string]time.Time{},
		jobDetail: fetchJobOutcomeDetail, e2eRows: fetchE2ERows, observabilityRows: fetchObservabilityRows,
	}
	w.initialize = w.initializeKusto
	w.initializeReadOnly = func(ctx context.Context) error { return w.initializeKustoClients(ctx, false) }
	return w
}

// Start blocks until cancellation and joins all workers before closing clients.
// The caller configures the process-wide panic policy and workqueue metrics.
func (w *Writer) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	ctx = utils.ContextWithControllerName(ctx, w.name)
	ctx = utils.ContextWithLogger(ctx, w.logger)
	defer w.queue.ShutDown()
	for ctx.Err() == nil {
		err := func() (err error) {
			defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("initialization panic: %v", value) })
			return w.initialize(ctx)
		}()
		if err == nil {
			break
		}
		w.logger.Error(err, "Kusto initialization failed; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	if w.closeClients != nil {
		defer w.closeClients()
	}
	if ctx.Err() != nil {
		return
	}
	var workers sync.WaitGroup
	for range w.config.CIJobOutcomes.GetWorkers() {
		workers.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer workers.Done()
			for w.processNext(ctx) {
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer utilruntime.HandleCrash()
		defer workers.Done()
		w.discoveryLoop(ctx)
	}()
	<-ctx.Done()
	w.queue.ShutDown()
	workers.Wait()
}

func (w *Writer) processNext(ctx context.Context) bool {
	id, shutdown := w.queue.Get()
	if shutdown {
		return false
	}
	defer w.queue.Done(id)
	if ctx.Err() != nil {
		w.queue.Forget(id)
		return false
	}
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), id)
	ctx = utils.ContextWithLogger(ctx, logger)
	// Recovery belongs inside the loop, so a recovered reconcile cannot remove
	// a worker permanently. The handler turns a panic into an ordinary retry.
	err := func() (err error) {
		defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("reconcile panic: %v", value) })
		return w.reconcile(ctx, id)
	}()
	if err != nil && ctx.Err() == nil {
		logger.Error(err, "Reconcile failed; retrying")
		w.queue.AddRateLimited(id)
	} else {
		w.queue.Forget(id)
	}
	return ctx.Err() == nil
}

func (w *Writer) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(w.config.CIJobOutcomes.GetInterval())
	defer ticker.Stop()
	for {
		w.discover(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Writer) discover(ctx context.Context, now time.Time) {
	defer utilruntime.HandleCrashWithContext(ctx)
	for _, release := range w.config.CIJobOutcomes.Releases {
		if ctx.Err() != nil {
			return
		}
		if err := w.discoverRelease(ctx, release, now); err != nil {
			w.logger.Error(err, "Discovery failed; retrying next poll", "release", release)
		}
	}
}

func (w *Writer) discoverRelease(ctx context.Context, release string, now time.Time) (err error) {
	defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("discovery panic: %v", value) })
	settings := w.config.CIJobOutcomes
	cursor, err := w.cursor(ctx, release)
	if err != nil {
		return err
	}
	repair := w.lastRepair[release].IsZero() || !now.Before(w.lastRepair[release].Add(settings.GetRepairInterval()))
	since := cursor
	if since.IsZero() {
		since = now.Add(-settings.GetWindow())
	} else {
		since = cursor.Add(-settings.GetOverlap())
	}
	if repair {
		if floor := now.Add(-settings.GetWindow()); floor.Before(since) {
			since = floor
		}
	}
	if startupSince := settings.GetStartupSince(); w.lastRepair[release].IsZero() && !startupSince.IsZero() && startupSince.Before(since) {
		since = startupSince
	}
	runs, err := fetchRuns(ctx, w.client, settings.SippyURI, release, settings.JobFilter, since)
	if err != nil {
		return err
	}
	slices.SortFunc(runs, func(a, b sippyRun) int {
		if order := a.Timestamp.Compare(b.Timestamp.Time); order != 0 {
			return order
		}
		return strings.Compare(a.ProwID, b.ProwID)
	})
	for _, run := range runs {
		if run.Timestamp.IsZero() || run.Timestamp.Before(since) || run.ProwID == "" {
			continue
		}
		if !validRun(ctx, run, BuildID(run.ProwID)) {
			continue
		}
		id := BuildID(run.ProwID)
		if run.hasTerminalOutcome() {
			w.metadata.put(id, runMetadata{run: run, release: release})
		}
		// Never subtract completed outcomes or cache entries here. Each source
		// has its own tag and a later repair must be able to fill missing batches.
		w.queue.Add(id)
	}
	if repair {
		w.lastRepair[release] = now
	}
	w.metrics.discovery.WithLabelValues(release).Set(float64(now.Unix()))
	return nil
}

func validRun(ctx context.Context, run sippyRun, id BuildID) bool {
	info, err := snapshot.ParseProwURL(run.URL)
	if err != nil || info.ProwID != string(id) || run.ProwID != string(id) {
		utils.LoggerFromContext(ctx).Info("Skipping malformed or mismatched Sippy run", "buildId", id, "url", run.URL, "error", err)
		return false
	}
	return true
}

func (w *Writer) lookupMetadata(ctx context.Context, id BuildID) (runMetadata, error) {
	if value, ok := w.metadata.get(id); ok && value.run.hasTerminalOutcome() {
		w.metrics.cacheHits.WithLabelValues("metadata").Inc()
		return value, nil
	}
	var failures []error
	settings := w.config.CIJobOutcomes
	for _, release := range settings.Releases {
		runs, err := fetchRunByID(ctx, w.client, settings.SippyURI, release, string(id))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, run := range runs {
			if run.ProwID != string(id) {
				continue
			}
			value := runMetadata{run: run, release: release}
			if validRun(ctx, run, id) && run.hasTerminalOutcome() {
				w.metadata.put(id, value)
			}
			return value, nil
		}
	}
	return runMetadata{}, errors.Join(append(failures, fmt.Errorf("sippy metadata not found for %s", id))...)
}

func (w *Writer) reconcile(ctx context.Context, id BuildID) error {
	ctx = snapshot.WithProwArtifactProblemHandler(ctx, func(source, reason string) {
		w.metrics.artifacts.WithLabelValues(source, reason).Inc()
	})
	missing := map[batchKind]bool{}
	var failures []error
	for _, kind := range batchKinds {
		if _, ok := w.batches.get(kind.tag(id)); ok {
			w.metrics.cacheHits.WithLabelValues("batches").Inc()
			w.reportBatch(kind, id, "existing", 0, nil, nil)
			continue
		}
		exists, err := w.tagExists(ctx, kind, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("check %s: %w", kind, err))
			w.reportBatch(kind, id, "error", 0, nil, err)
			continue
		}
		// Live tags are not cached as submissions: after eviction the next
		// reconcile checks the current Kusto state again before any download.
		missing[kind] = !exists
		if exists {
			w.reportBatch(kind, id, "existing", 0, nil, nil)
		}
	}
	if !missing[runBatch] && !missing[testsBatch] && !missing[namesBatch] && !missing[observabilityTestsBatch] && !missing[observabilityNamesBatch] {
		return errors.Join(failures...)
	}
	metadata, err := w.lookupMetadata(ctx, id)
	if err == nil && !validRun(ctx, metadata.run, id) {
		if w.report == nil {
			return errors.Join(failures...)
		}
		err = fmt.Errorf("malformed or mismatched Sippy metadata for %s", id)
	}
	if err != nil {
		for _, kind := range batchKinds {
			if missing[kind] {
				w.reportBatch(kind, id, "error", 0, nil, err)
			}
		}
		return errors.Join(append(failures, err)...)
	}
	if missing[runBatch] {
		// The immutable run tag must not capture a pending Sippy verdict, even
		// if Prow already has a completion record. Other batches are independent.
		if !metadata.run.hasTerminalOutcome() {
			err := fmt.Errorf("job outcome pending: Sippy overall_result %q for %s is not terminal", metadata.run.OverallResult, id)
			failures = append(failures, err)
			w.reportBatch(runBatch, id, "error", 0, nil, err)
		} else if detail, err := w.jobDetail(ctx, w.client, metadata.run.URL); err != nil {
			failures = append(failures, fmt.Errorf("job outcome: %w", err))
			w.reportBatch(runBatch, id, "error", 0, nil, err)
		} else {
			outcome := outcomeFor(metadata.run, metadata.release)
			outcome.SvcCluster, outcome.MgmtCluster = detail.SvcCluster, detail.MgmtCluster
			outcome.FinishedAt, outcome.ADOBuildID = detail.FinishedAt, detail.ADOBuildID
			failures = append(failures, submitBatch(ctx, w, runBatch, id, []ciJobOutcome{outcome}))
		}
	}
	for _, source := range []struct {
		tests, names batchKind
		fetch        func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error)
	}{
		{testsBatch, namesBatch, w.e2eRows},
		{observabilityTestsBatch, observabilityNamesBatch, w.observabilityRows},
	} {
		if !missing[source.tests] && !missing[source.names] {
			continue
		}
		tests, names, err := source.fetch(ctx, w.client, metadata.run.URL)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source.tests, err))
			if missing[source.tests] && tests == nil {
				w.reportBatch(source.tests, id, "error", 0, nil, err)
			}
			if missing[source.names] && names == nil {
				w.reportBatch(source.names, id, "error", 0, nil, err)
			}
		}
		if missing[source.tests] && (err == nil || tests != nil) {
			failures = append(failures, submitBatch(ctx, w, source.tests, id, tests))
		}
		if missing[source.names] && (err == nil || names != nil) {
			failures = append(failures, submitBatch(ctx, w, source.names, id, names))
		}
	}
	return errors.Join(failures...)
}

func submitBatch[T any](ctx context.Context, w *Writer, kind batchKind, id BuildID, rows []T) error {
	if rows == nil {
		w.reportBatch(kind, id, "unavailable", 0, nil, nil)
		return nil
	}
	if len(rows) != 0 {
		payload, err := encodeRows(rows)
		if err == nil && w.dryRun {
			w.reportBatch(kind, id, "would-submit", len(rows), payload, nil)
			return nil
		}
		if err == nil {
			err = w.submit(ctx, kind, id, payload)
		}
		result := "accepted"
		if err != nil {
			result = "error"
		}
		w.metrics.submissions.WithLabelValues(string(kind), result).Inc()
		if err != nil {
			w.reportBatch(kind, id, "error", len(rows), nil, err)
			return fmt.Errorf("submit %s: %w", kind, err)
		}
		w.reportBatch(kind, id, "submitted", len(rows), nil, nil)
	} else {
		w.reportBatch(kind, id, "empty", 0, nil, nil)
	}
	// Valid checked-empty sources are cached only in memory. Absent or malformed
	// sources return nil slices and must remain eligible for the next reconcile.
	w.batches.put(kind.tag(id), struct{}{})
	return nil
}
