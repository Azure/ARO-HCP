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
const CIJobDiscoveryControllerName = "ci-job-discovery"
const CollectorName = CIJobOutcomesControllerName

type BuildID string

// Writer owns two URI queues and one shared client lifetime. Kusto tables are
// the durable work ledger; the cache only suppresses recently accepted writes.
type Writer struct {
	name, discoveryName     string
	config                  *config.Config
	logger, discoveryLogger logr.Logger
	client                  *http.Client
	gcs                     *GCSClient
	queue, discoveryQueue   workqueue.TypedRateLimitingInterface[JobURI]
	batches                 *ttlCache[string, struct{}]
	metrics                 writerMetrics
	lastRepair              map[string]time.Time // discovery goroutine only, keyed by listing prefix
	now                     func() time.Time
	dryRun                  bool
	report                  func(BatchReport)

	initialize         func(context.Context) error
	initializeReadOnly func(context.Context) error
	closeClients       func()
	jobCursor          func(context.Context, GCSJob) (string, error)
	pendingJobs        func(context.Context) ([]JobURI, error)
	discoveredExists   func(context.Context, JobURI) (bool, error)
	processedExists    func(context.Context, BuildID) (bool, error)
	submitDiscovery    func(context.Context, []DiscoveredJob, string) error
	submitProcessed    func(context.Context, BuildID) error
	tagExists          func(context.Context, batchKind, BuildID) (bool, error)
	submit             func(context.Context, batchKind, BuildID, *bytes.Buffer) error
	completion         func(context.Context, *http.Client, string) (*prowCompletion, error)
	jobDetail          func(context.Context, *http.Client, string) (runDetail, error)
	e2eRows            func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error)
	observabilityRows  func(context.Context, *http.Client, string) ([]ciTestResult, []ciTestName, error)
}

func NewWriter(cfg *config.Config, logger *slog.Logger) *Writer {
	newQueue := func(name string) workqueue.TypedRateLimitingInterface[JobURI] {
		return workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[JobURI](time.Second, 5*time.Minute),
			workqueue.TypedRateLimitingQueueConfig[JobURI]{Name: name})
	}
	base := logr.FromSlogHandler(logger.Handler())
	w := &Writer{
		name: CIJobOutcomesControllerName, discoveryName: CIJobDiscoveryControllerName, config: cfg,
		logger:          base.WithValues(utils.LogValues{}.AddControllerName(CIJobOutcomesControllerName)...),
		discoveryLogger: base.WithValues(utils.LogValues{}.AddControllerName(CIJobDiscoveryControllerName)...),
		client:          &http.Client{Timeout: 2 * time.Minute},
		queue:           newQueue(CIJobOutcomesControllerName), discoveryQueue: newQueue(CIJobDiscoveryControllerName),
		batches: newTTLCache[string, struct{}](cfg.CIJobOutcomes.GetCacheSize(), cfg.CIJobOutcomes.GetCacheTTL()),
		metrics: newWriterMetrics(), lastRepair: map[string]time.Time{}, now: time.Now,
		completion: fetchProwCompletion, jobDetail: fetchJobOutcomeDetail,
		e2eRows: fetchE2ERows, observabilityRows: fetchObservabilityRows,
	}
	w.gcs = &GCSClient{Client: w.client, Bucket: cfg.CIJobOutcomes.GCSBucket, JobFilter: cfg.CIJobOutcomes.JobFilter}
	w.initialize = w.initializeKusto
	w.initializeReadOnly = func(ctx context.Context) error { return w.initializeKustoClients(ctx, false) }
	return w
}

// Start joins both controllers before closing their shared clients.
func (w *Writer) Start(ctx context.Context) {
	defer utilruntime.HandleCrash()
	defer w.queue.ShutDown()
	defer w.discoveryQueue.ShutDown()
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
	for _, controller := range []struct {
		name    string
		logger  logr.Logger
		workers int
		process func(context.Context) bool
		loop    func(context.Context)
	}{
		{w.name, w.logger, w.config.CIJobOutcomes.GetWorkers(), w.processNext, w.processingLoop},
		{w.discoveryName, w.discoveryLogger, w.config.CIJobOutcomes.GetDiscoveryWorkers(), w.processNextDiscovery, w.discoveryLoop},
	} {
		controllerCtx := utils.ContextWithLogger(utils.ContextWithControllerName(ctx, controller.name), controller.logger)
		for range controller.workers {
			workers.Add(1)
			go func() {
				defer utilruntime.HandleCrash()
				defer workers.Done()
				for controller.process(controllerCtx) {
				}
			}()
		}
		workers.Add(1)
		go func() {
			defer utilruntime.HandleCrash()
			defer workers.Done()
			controller.loop(controllerCtx)
		}()
	}
	<-ctx.Done()
	w.queue.ShutDown()
	w.discoveryQueue.ShutDown()
	workers.Wait()
}

func (w *Writer) processNext(ctx context.Context) bool {
	return w.processNextQueue(ctx, w.queue, w.reconcile)
}

func (w *Writer) processNextDiscovery(ctx context.Context) bool {
	return w.processNextQueue(ctx, w.discoveryQueue, func(ctx context.Context, uri JobURI) (time.Duration, error) {
		if err := w.reconcileDiscovery(ctx, uri); err != nil {
			return 0, err
		}
		// Acceptance is not visibility. After cache expiry, reconcile checks the
		// live row and either finishes or resubmits failed asynchronous ingestion.
		if _, accepted := w.batches.get(discoveryTag(uri)); accepted {
			return w.config.CIJobOutcomes.GetInterval(), nil
		}
		return 0, nil
	})
}

func (w *Writer) processNextQueue(ctx context.Context, queue workqueue.TypedRateLimitingInterface[JobURI], reconcile func(context.Context, JobURI) (time.Duration, error)) bool {
	uri, shutdown := queue.Get()
	if shutdown {
		return false
	}
	defer queue.Done(uri)
	if ctx.Err() != nil {
		queue.Forget(uri)
		return false
	}
	logger := utils.AddLoggerValues(utils.LoggerFromContext(ctx), uri)
	ctx = utils.ContextWithLogger(ctx, logger)
	delay, err := func() (delay time.Duration, err error) {
		// Recover inside the loop: nonfatal panics must not retire a worker.
		defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("reconcile panic: %v", value) })
		return reconcile(ctx, uri)
	}()
	if err != nil && ctx.Err() == nil {
		logger.Error(err, "Reconcile failed; retrying")
		queue.AddRateLimited(uri)
	} else {
		queue.Forget(uri)
		if delay > 0 && ctx.Err() == nil {
			queue.AddAfter(uri, delay)
		}
	}
	return ctx.Err() == nil
}

func (w *Writer) discoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(w.config.CIJobOutcomes.GetDiscoveryInterval())
	defer ticker.Stop()
	for {
		w.discover(ctx, w.now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Writer) discover(ctx context.Context, now time.Time) {
	defer utilruntime.HandleCrashWithContext(ctx)
	jobs, err := w.gcs.ListJobs(ctx)
	if err != nil {
		utils.LoggerFromContext(ctx).Error(err, "List job names failed")
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		if err := w.discoverJob(ctx, job, now); err != nil {
			utils.LoggerFromContext(ctx).Error(err, "Discovery failed; retrying next poll", "jobName", job.Name, "prefix", job.Prefix)
		}
	}
}

func (w *Writer) discoverJob(ctx context.Context, job GCSJob, now time.Time) (err error) {
	defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("discovery panic: %v", value) })
	start := time.Now()
	settings := w.config.CIJobOutcomes
	since, mode, fetched, enqueued := now.Add(-settings.GetWindow()), "incremental", 0, 0
	defer func() {
		utils.LoggerFromContext(ctx).Info("Discovery scan", "jobName", job.Name, "prefix", job.Prefix, "since", since, "until", now,
			"mode", mode, "fetched", fetched, "enqueueAttempts", enqueued, "queueDepth", w.discoveryQueue.Len(), "duration", time.Since(start), "error", err)
	}()
	cursor, err := w.jobCursor(ctx, job)
	if err != nil {
		return err
	}
	if cursor != "" {
		since, err = BuildIDTime(cursor)
		if err != nil {
			return err
		}
		since = since.Add(-settings.GetOverlap())
	}
	repair := w.lastRepair[job.Prefix].IsZero() || !now.Before(w.lastRepair[job.Prefix].Add(settings.GetRepairInterval()))
	if repair {
		mode = "repair"
		if floor := now.Add(-settings.GetWindow()); floor.Before(since) {
			since = floor
		}
	}
	if startup := settings.GetStartupSince(); w.lastRepair[job.Prefix].IsZero() && !startup.IsZero() && startup.Before(since) {
		since, mode = startup, "startup"
	}
	if since.After(now) {
		since = now
	}
	err = w.gcs.ListRuns(ctx, job, since, now, func(uri JobURI) error {
		fetched++
		// Never subtract accepted cache entries: replay repairs failed ingestion.
		w.discoveryQueue.Add(uri)
		enqueued++
		return nil
	})
	if err != nil {
		return err
	}
	if repair {
		w.lastRepair[job.Prefix] = now
	}
	w.metrics.discovery.WithLabelValues(w.discoveryName).Set(float64(now.Unix()))
	return nil
}

func (w *Writer) reconcileDiscovery(ctx context.Context, uri JobURI) error {
	row, err := JobReference(uri)
	if err != nil {
		return err
	}
	if row.JobURI != uri {
		return fmt.Errorf("noncanonical job URI %q", uri)
	}
	tag := discoveryTag(uri)
	if _, accepted := w.batches.get(tag); accepted {
		w.metrics.cacheHits.WithLabelValues("batches").Inc()
		return nil
	}
	exists, err := w.discoveredExists(ctx, uri)
	if err != nil || exists {
		return err
	}
	if err := w.submitDiscovery(ctx, []DiscoveredJob{row}, tag); err != nil {
		w.metrics.submissions.WithLabelValues(string(discoveredBatch), "error").Inc()
		return err
	}
	w.metrics.submissions.WithLabelValues(string(discoveredBatch), "accepted").Inc()
	w.batches.put(tag, struct{}{})
	return nil
}

func (w *Writer) processingLoop(ctx context.Context) {
	ticker := time.NewTicker(w.config.CIJobOutcomes.GetInterval())
	defer ticker.Stop()
	for {
		w.discoverPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Writer) discoverPending(ctx context.Context) {
	defer utilruntime.HandleCrashWithContext(ctx)
	start := time.Now()
	jobs, err := w.pendingJobs(ctx)
	enqueued := 0
	if err == nil {
		for _, uri := range jobs {
			canonical, validationErr := CanonicalJobURI(string(uri))
			if validationErr != nil || canonical != uri {
				utils.LoggerFromContext(ctx).Error(fmt.Errorf("invalid canonical job URI %q: %v", uri, validationErr), "Skipping invalid discovered row")
				continue
			}
			w.queue.Add(uri)
			enqueued++
		}
		w.metrics.discovery.WithLabelValues(w.name).Set(float64(w.now().Unix()))
	} else {
		utils.LoggerFromContext(ctx).Error(err, "Pending job scan failed; retrying next poll")
	}
	utils.LoggerFromContext(ctx).Info("Pending job scan", "mode", "pending", "window", "all", "fetched", len(jobs), "enqueueAttempts", enqueued,
		"queueDepth", w.queue.Len(), "duration", time.Since(start), "error", err)
}

func (w *Writer) reconcile(ctx context.Context, uri JobURI) (time.Duration, error) {
	row, err := JobReference(uri)
	if err != nil {
		return 0, err
	}
	if row.JobURI != uri {
		return 0, fmt.Errorf("noncanonical job URI %q", uri)
	}
	id := BuildID(row.BuildID)
	if exists, err := w.processedExists(ctx, id); err != nil || exists {
		return 0, err
	}
	ctx = snapshot.WithProwArtifactProblemHandler(ctx, func(source, reason string) { w.metrics.artifacts.WithLabelValues(source, reason).Inc() })
	delay := w.config.CIJobOutcomes.GetInterval()
	completion, err := w.completion(ctx, w.client, string(uri))
	if err != nil {
		return 0, err
	}
	if completion == nil || w.now().Before(completion.FinishedAt.Add(w.config.CIJobOutcomes.GetCompletionDelay())) {
		w.reportBatch(runBatch, id, "pending", 0, nil, nil)
		return delay, nil
	}
	info, _ := uri.Info() // JobReference already validated this URI.
	missing := map[batchKind]bool{}
	handled := map[batchKind]bool{}
	var failures []error
	for _, kind := range batchKinds {
		// Accepted submissions are never completion proof, even within the TTL.
		exists, err := w.tagExists(ctx, kind, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("check %s: %w", kind, err))
			w.reportBatch(kind, id, "error", 0, nil, err)
			continue
		}
		if exists {
			handled[kind] = true
			w.reportBatch(kind, id, "existing", 0, nil, nil)
		} else if _, accepted := w.batches.get(kind.tag(id)); accepted {
			w.metrics.cacheHits.WithLabelValues("batches").Inc()
			w.reportBatch(kind, id, "pending", 0, nil, nil)
		} else {
			missing[kind] = true
		}
	}
	if missing[runBatch] {
		if detail, err := w.jobDetail(ctx, w.client, info.URL); err != nil {
			failures = append(failures, fmt.Errorf("job outcome: %w", err))
			w.reportBatch(runBatch, id, "error", 0, nil, err)
		} else {
			failures = append(failures, submitBatch(ctx, w, runBatch, id, []ciJobOutcome{outcomeForProw(info, *completion, detail)}))
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
		tests, names, err := source.fetch(ctx, w.client, info.URL)
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
			handled[source.tests] = len(tests) == 0
		}
		if missing[source.names] && (err == nil || names != nil) {
			failures = append(failures, submitBatch(ctx, w, source.names, id, names))
			handled[source.names] = len(names) == 0
		}
	}
	if err := errors.Join(failures...); err != nil {
		return 0, err
	}
	for _, kind := range batchKinds {
		if !handled[kind] {
			return delay, nil
		}
	}
	if w.dryRun {
		w.reportBatch(processedBatch, id, "would-submit", 1, nil, nil)
		return 0, nil
	}
	err = w.submitProcessed(ctx, id)
	result := "accepted"
	if err != nil {
		result = "error"
	}
	w.metrics.submissions.WithLabelValues(string(processedBatch), result).Inc()
	if err != nil {
		w.reportBatch(processedBatch, id, "error", 1, nil, err)
		return 0, err
	}
	w.reportBatch(processedBatch, id, "submitted", 1, nil, nil)
	// Keep checking until the acknowledgment itself becomes visible.
	return delay, nil
}

func submitBatch[T any](ctx context.Context, w *Writer, kind batchKind, id BuildID, rows []T) error {
	if len(rows) == 0 {
		status := "empty"
		if rows == nil {
			status = "unavailable"
		}
		w.reportBatch(kind, id, status, 0, nil, nil)
		return nil
	}
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
	w.batches.put(kind.tag(id), struct{}{})
	return nil
}
