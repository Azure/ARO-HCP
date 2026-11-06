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
	"github.com/Azure/ARO-HCP/tooling/tenant-quota/pkg/config"
)

const (
	ControllerName    = "ci-failure-patterns"
	discoveryPageSize = 128
)

// Controller owns normal reconciliation and its clients. Successful initialization
// admits the compiled extractor/schema contract; PR3 will coordinate upgrades.
type Controller struct {
	config          *config.Config
	logger          *slog.Logger
	client          *http.Client
	queue           workqueue.TypedRateLimitingInterface[string]
	metrics         controllerMetrics
	batches         *submissionCache
	now             func() time.Time
	retryDelay      time.Duration
	initializeDelay time.Duration
	initialize      func(context.Context) error
	closeClients    func()
	readBuildIDs    func(context.Context, string) ([]string, error)
	readInputs      func(context.Context, string) ([]input, error)
	readResults     func(context.Context, []input, failurePatternVersion) ([]resultRow, error)
	submit          func(context.Context, *bytes.Buffer) error

	scanAfterBuildID string

	normalAdmissionMu             sync.Mutex
	admittedFailurePatternVersion failurePatternVersion
	normalReconciliationAllowed   bool
	admittedReconciliations       int
	admittedReconciliationsDone   chan struct{}
}

func NewController(cfg *config.Config, logger *slog.Logger) *Controller {
	c := &Controller{
		config: cfg, logger: logger.With(utils.LogValues{}.AddControllerName(ControllerName)...),
		client: &http.Client{Timeout: 2 * time.Minute},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.NewTypedItemExponentialFailureRateLimiter[string](time.Second, 5*time.Minute),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: ControllerName}),
		metrics: newControllerMetrics(), batches: newSubmissionCache(cfg.CIFailurePatterns.GetCacheSize(), cfg.CIFailurePatterns.GetCacheTTL()),
		now: time.Now, retryDelay: 5 * time.Second, initializeDelay: 5 * time.Second,
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
	if err := c.allowNormalReconciliation(compiledFailurePatternVersion()); err != nil {
		c.logger.Error("Cannot start normal reconciliation", "error", err)
		return
	}
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
	workers.Add(1)
	go func() {
		defer utilruntime.HandleCrashWithContext(ctx)
		defer workers.Done()
		ticker := time.NewTicker(c.config.CIFailurePatterns.GetInterval())
		defer ticker.Stop()
		for {
			if err := recoverError(ctx, func() error { return c.scan(ctx) }); err != nil && ctx.Err() == nil {
				c.metrics.failures.WithLabelValues("scan").Inc()
				c.logger.Error("Input scan failed; retrying next poll", "error", err)
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
		c.queue.AddRateLimited(build)
		return true
	}
	c.queue.Forget(build)
	if delay > 0 {
		c.queue.AddAfter(build, delay)
	}
	return true
}

func (c *Controller) normalReconciliationVersion() (failurePatternVersion, bool) {
	c.normalAdmissionMu.Lock()
	defer c.normalAdmissionMu.Unlock()
	return c.admittedFailurePatternVersion, c.normalReconciliationAllowed && c.admittedFailurePatternVersion == compiledFailurePatternVersion()
}

func (c *Controller) tryAdmitNormalReconciliation() (failurePatternVersion, bool) {
	c.normalAdmissionMu.Lock()
	defer c.normalAdmissionMu.Unlock()
	if !c.normalReconciliationAllowed || c.admittedFailurePatternVersion != compiledFailurePatternVersion() {
		return failurePatternVersion{}, false
	}
	if c.admittedReconciliations == 0 {
		c.admittedReconciliationsDone = make(chan struct{})
	}
	c.admittedReconciliations++
	return c.admittedFailurePatternVersion, true
}

func (c *Controller) finishNormalReconciliation() {
	c.normalAdmissionMu.Lock()
	defer c.normalAdmissionMu.Unlock()
	c.admittedReconciliations--
	if c.admittedReconciliations == 0 {
		close(c.admittedReconciliationsDone)
	}
}

// pauseNormalReconciliationAndWait prevents new normal work before waiting, without holding the
// mutex over a network operation. Cancellation never reopens the gate.
func (c *Controller) pauseNormalReconciliationAndWait(ctx context.Context) error {
	c.normalAdmissionMu.Lock()
	c.normalReconciliationAllowed = false
	if c.admittedReconciliations == 0 {
		c.normalAdmissionMu.Unlock()
		return nil
	}
	admittedReconciliationsDone := c.admittedReconciliationsDone
	c.normalAdmissionMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-admittedReconciliationsDone:
		return nil
	}
}

// allowNormalReconciliation admits only the contract compiled into this process.
func (c *Controller) allowNormalReconciliation(version failurePatternVersion) error {
	if version != compiledFailurePatternVersion() {
		return fmt.Errorf("admission target does not match executable")
	}
	c.normalAdmissionMu.Lock()
	defer c.normalAdmissionMu.Unlock()
	if c.admittedReconciliations != 0 {
		return fmt.Errorf("cannot open admission before drain")
	}
	c.admittedFailurePatternVersion, c.normalReconciliationAllowed = version, true
	return nil
}

func (c *Controller) scan(ctx context.Context) error {
	if _, allowed := c.normalReconciliationVersion(); !allowed {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	builds, err := c.readBuildIDs(ctx, c.scanAfterBuildID)
	if err != nil {
		return fmt.Errorf("read retained failed build IDs: %w", err)
	}
	for _, build := range builds {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.queue.Add(build)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.scanAfterBuildID = ""
	if len(builds) == discoveryPageSize {
		c.scanAfterBuildID = builds[len(builds)-1]
	}
	c.metrics.scans.Inc()
	return nil
}

func (c *Controller) reconcile(ctx context.Context, build string) (time.Duration, error) {
	version, allowed := c.tryAdmitNormalReconciliation()
	if !allowed {
		return c.retryDelay, nil
	}
	defer c.finishNormalReconciliation()
	inputs, err := c.readInputs(ctx, build)
	if err != nil {
		return 0, fmt.Errorf("read build %s inputs: %w", build, err)
	}
	return c.reconcileInputs(ctx, version, inputs)
}
