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

package cipatterns

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/util/workqueue"

	"github.com/Azure/ARO-HCP/internal/utils"
	buildversion "github.com/Azure/ARO-HCP/internal/version"
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const (
	ControllerName = "ci-failure-patterns"
	// headPageSize bounds each newest-build poll; discoveryPageSize bounds each
	// step of the history sweep.
	headPageSize      = 64
	discoveryPageSize = 128
	// sweepRest is the pause after a sweep reaches the oldest retained build.
	sweepRest = 12 * time.Hour
	// defaultOperationTimeout bounds a Kusto operation when the config sets no timeout.
	defaultOperationTimeout = 2 * time.Minute
)

// Controller computes CI failure patterns for the failed results stored in Kusto
// and writes them to the results table. Once it has verified the Kusto tables and
// mappings it needs, it discovers builds newest first and reconciles each one.
type Controller struct {
	config          *config.Config
	logger          *slog.Logger
	client          *http.Client
	queue           workqueue.TypedRateLimitingInterface[string]
	metrics         controllerMetrics
	batches         *submissionCache
	now             func() time.Time
	retryDelay      time.Duration
	maxRetryDelay   time.Duration
	initializeDelay time.Duration
	sweepPoll       time.Duration
	initialize      func(context.Context) error
	closeClients    func()
	readBuildIDs    func(ctx context.Context, before string, limit int) ([]string, error)
	readInputs      func(context.Context, string) ([]input, error)
	readResults     func(context.Context, []input, failurePatternVersion) ([]resultRow, error)
	submit          func(context.Context, *bytes.Buffer) error
	readCoverage    func(ctx context.Context, version failurePatternVersion, cutoff time.Time) ([]sourceCoverage, error)
	readControl     func(context.Context) (failurePatternVersion, bool, error)
	submitControl   func(context.Context, *bytes.Buffer) error

	// sweepDone receives a non-blocking signal each time the history sweep completes.
	sweepDone        chan struct{}
	coverageFallback time.Duration

	// sweepBefore, sweepBuilds and sweepRestUntil are owned by the sweep loop.
	sweepBefore    string
	sweepBuilds    int
	sweepRestUntil time.Time

	pendingMu       sync.Mutex
	pendingAttempts map[string]int
}

func NewController(cfg *config.Config, logger *slog.Logger) *Controller {
	c := &Controller{
		config: cfg, logger: logger.With(utils.LogValues{}.AddControllerName(ControllerName)...),
		client: &http.Client{Timeout: defaultOperationTimeout},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, 5*time.Minute),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: ControllerName}),
		metrics: newControllerMetrics(), batches: newSubmissionCache(cfg.CIFailurePatterns.GetCacheSize(), cfg.CIFailurePatterns.GetCacheTTL()),
		now: time.Now, retryDelay: 5 * time.Second, maxRetryDelay: 2 * time.Minute,
		initializeDelay: 5 * time.Second, sweepPoll: 5 * time.Second,
		pendingAttempts: map[string]int{}, sweepDone: make(chan struct{}, 1), coverageFallback: coverageFallbackInterval,
	}
	c.initialize = c.initializeKusto
	return c
}

// Start blocks until all workers/pollers have joined, then closes the clients.
func (c *Controller) Start(ctx context.Context) {
	ctx = utils.ContextWithLogger(utils.ContextWithControllerName(ctx, ControllerName), logr.FromSlogHandler(c.logger.Handler()))
	defer utilruntime.HandleCrashWithContext(ctx)
	defer c.queue.ShutDown()
	for ctx.Err() == nil {
		err := recoverError(ctx, func() error { return c.initialize(ctx) })
		if err == nil {
			break
		}
		c.metrics.failures.WithLabelValues("initialize").Inc()
		c.logger.Error("Kusto initialization failed; retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.initializeDelay):
		}
	}
	if c.closeClients != nil {
		defer c.closeClients()
	}
	if ctx.Err() != nil {
		return
	}
	version := compiledFailurePatternVersion()
	c.logger.Info("Reconciliation started",
		"extractorVersion", version.extractorVersion, "resultSchemaVersion", version.resultSchemaVersion,
		"table", c.config.CIFailurePatterns.Results.Table, "workers", c.config.CIFailurePatterns.GetWorkers(),
		"interval", c.config.CIFailurePatterns.GetInterval(), "producerCommit", buildversion.CommitSHA)
	var workers sync.WaitGroup
	for range c.config.CIFailurePatterns.GetWorkers() {
		workers.Add(1)
		go func() {
			defer utilruntime.HandleCrashWithContext(ctx)
			defer workers.Done()
			for c.processNext(ctx) {
			}
		}()
	}
	workers.Add(3)
	go func() {
		defer utilruntime.HandleCrashWithContext(ctx)
		defer workers.Done()
		c.runCoverage(ctx)
	}()
	go func() {
		defer utilruntime.HandleCrashWithContext(ctx)
		defer workers.Done()
		ticker := time.NewTicker(c.config.CIFailurePatterns.GetInterval())
		defer ticker.Stop()
		for {
			if err := recoverError(ctx, func() error { return c.scanHead(ctx) }); err != nil && ctx.Err() == nil {
				c.metrics.failures.WithLabelValues("scan").Inc()
				c.logger.Error("Newest-build scan failed; retrying next poll", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		defer utilruntime.HandleCrashWithContext(ctx)
		defer workers.Done()
		ticker := time.NewTicker(c.sweepPoll)
		defer ticker.Stop()
		for {
			if err := recoverError(ctx, func() error { return c.sweepStep(ctx) }); err != nil && ctx.Err() == nil {
				c.metrics.failures.WithLabelValues("sweep").Inc()
				c.logger.Error("History sweep step failed; retrying", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	<-ctx.Done()
	c.queue.ShutDown()
	workers.Wait()
}

func recoverError(ctx context.Context, fn func() error) (err error) {
	defer utilruntime.HandleCrashWithContext(ctx, func(_ context.Context, value any) { err = fmt.Errorf("controller panic: %v", value) })
	return fn()
}

func (c *Controller) processNext(ctx context.Context) bool {
	build, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(build)
	if ctx.Err() != nil {
		c.queue.Forget(build)
		return false
	}
	var delay time.Duration
	err := recoverError(ctx, func() (err error) { delay, err = c.reconcile(ctx, build); return err })
	if ctx.Err() != nil {
		c.queue.Forget(build)
		return false
	}
	if err != nil {
		c.metrics.failures.WithLabelValues("reconcile").Inc()
		c.logger.Error("Reconcile failed; retrying", "buildId", build, "error", err)
		c.trackUnfinished(build)
		c.queue.AddRateLimited(build)
		return true
	}
	c.queue.Forget(build)
	if delay > 0 {
		c.queue.AddAfter(build, c.pendingDelay(build, delay))
	} else {
		c.forgetPending(build)
	}
	return true
}

// scanHead enqueues the newest builds. It always starts from the newest build, so
// a recent run never waits for the history sweep to reach it.
func (c *Controller) scanHead(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	builds, err := c.readBuildIDs(ctx, "", headPageSize)
	if err != nil {
		return fmt.Errorf("read newest failed build IDs: %w", err)
	}
	if err := c.enqueueBuilds(ctx, builds); err != nil {
		return err
	}
	c.metrics.scans.WithLabelValues("head").Inc()
	c.logger.Info("Newest-build scan", "builds", len(builds), "queueLength", c.queue.Len())
	return nil
}

// sweepStep enqueues one page of the newest-to-oldest walk through retained history.
// It advances only while fewer than one page of builds is outstanding (queued, or
// submitted and awaiting result visibility), so a cold start or rebuild proceeds at
// worker speed and slow ingestion cannot pile pending visibility polls up across
// history. It rests for sweepRest after reaching the oldest build. Failed or
// cancelled steps leave the cursor unchanged.
func (c *Controller) sweepStep(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.now().Before(c.sweepRestUntil) || c.outstandingBuilds() >= discoveryPageSize {
		return nil
	}
	builds, err := c.readBuildIDs(ctx, c.sweepBefore, discoveryPageSize)
	if err != nil {
		return fmt.Errorf("read retained failed build IDs: %w", err)
	}
	if err := c.enqueueBuilds(ctx, builds); err != nil {
		return err
	}
	c.sweepBuilds += len(builds)
	sweepBuilds := c.sweepBuilds
	if len(builds) == discoveryPageSize {
		c.sweepBefore = builds[len(builds)-1]
	} else {
		c.logger.Info("History sweep complete", "builds", c.sweepBuilds, "rest", sweepRest)
		c.sweepBefore, c.sweepBuilds, c.sweepRestUntil = "", 0, c.now().Add(sweepRest)
		select {
		case c.sweepDone <- struct{}{}:
		default:
		}
	}
	c.metrics.scans.WithLabelValues("sweep").Inc()
	c.logger.Info("History sweep page", "builds", len(builds), "sweepBuilds", sweepBuilds, "queueLength", c.queue.Len())
	return nil
}

func (c *Controller) enqueueBuilds(ctx context.Context, builds []string) error {
	for _, build := range builds {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.queue.Add(build)
	}
	return ctx.Err()
}

// pendingDelay doubles a build's wait for result visibility from base up to
// maxRetryDelay. Queued ingestion takes minutes, so a fixed short delay would make
// every pending build re-read its evidence every few seconds.
func (c *Controller) pendingDelay(build string, base time.Duration) time.Duration {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delay := base
	for range c.pendingAttempts[build] {
		if delay >= c.maxRetryDelay {
			break
		}
		delay *= 2
	}
	c.pendingAttempts[build]++
	return min(delay, max(c.maxRetryDelay, base))
}

// trackUnfinished keeps a build that failed before reconciliation completed counted as
// outstanding while its retry is delayed, without affecting its visibility backoff.
func (c *Controller) trackUnfinished(build string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if _, tracked := c.pendingAttempts[build]; !tracked {
		c.pendingAttempts[build] = 0
	}
}

// outstandingBuilds counts builds that still need worker time: ready in the queue,
// submitted and waiting for their results to become visible, or failed and waiting to
// be retried (delayed or in flight). A build that is both ready and tracked is counted
// twice, which only makes the sweep gate more conservative.
func (c *Controller) outstandingBuilds() int {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.queue.Len() + len(c.pendingAttempts)
}

func (c *Controller) pendingPolls(build string) int {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	return c.pendingAttempts[build]
}

func (c *Controller) forgetPending(build string) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	delete(c.pendingAttempts, build)
}

func (c *Controller) reconcile(ctx context.Context, build string) (time.Duration, error) {
	inputs, err := c.readInputs(ctx, build)
	if err != nil {
		return 0, fmt.Errorf("read build %s inputs: %w", build, err)
	}
	return c.reconcileInputs(ctx, compiledFailurePatternVersion(), inputs)
}
