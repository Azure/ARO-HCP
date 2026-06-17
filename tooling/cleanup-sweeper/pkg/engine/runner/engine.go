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

package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// DefaultParallelism is used when Engine.Parallelism is not set.
	DefaultParallelism = 4
	// DefaultRetries is the minimum retry count for retryable operations.
	DefaultRetries = 1
)

// Target represents a discovered resource selected for deletion.
type Target struct {
	ID   string
	Name string
	Type string
}

// Step defines one ordered cleanup stage in the engine execution model.
// Each step is responsible for discovery, per-target deletion, and verification.
type Step interface {
	Name() string
	Discover(ctx context.Context) ([]Target, error)
	Delete(ctx context.Context, target Target, wait bool) error
	Verify(ctx context.Context) error
	RetryLimit() int
	// ContinueOnError enables best-effort behavior for per-target deletions and
	// post-delete verification within the step.
	// Discovery failures remain fatal because no target set can be established.
	ContinueOnError() bool
}

// VerifyFn is an optional verification callback used by concrete step types.
type VerifyFn func(ctx context.Context) error

// Engine executes cleanup steps in order with bounded per-step parallelism.
type Engine struct {
	Steps       []Step
	Parallelism int
	DryRun      bool
	Wait        bool
	PostRunFn   func(ctx context.Context) error
}

// Run executes all configured steps in order and then executes PostRunFn if set.
func (e *Engine) Run(ctx context.Context) error {
	parallelism := e.Parallelism
	if parallelism < 1 {
		parallelism = DefaultParallelism
	}
	for _, step := range e.Steps {
		if err := e.runStep(ctx, step, parallelism); err != nil {
			return err
		}
	}
	if e.PostRunFn != nil {
		return e.PostRunFn(ctx)
	}
	return nil
}

func (e *Engine) runStep(ctx context.Context, step Step, parallelism int) error {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(err)
	}
	targets, err := step.Discover(ctx)
	if err != nil {
		// Discovery is treated as a control-plane prerequisite. If it fails we
		// cannot safely continue this step because targets are unknown.
		return fmt.Errorf("%s: discovery failed: %w", step.Name(), err)
	}
	if len(targets) == 0 {
		return nil
	}

	if e.DryRun {
		logger.Info("Dry-run deletion step", "step", step.Name(), "targets", len(targets))
		for _, target := range targets {
			logger.Info(
				"Dry-run deletion target",
				"step", step.Name(),
				"resource", target.Name,
				"type", target.Type,
				"id", target.ID,
			)
		}
		return nil
	}

	jobs := make(chan Target)
	errs := make(chan error, len(targets))

	var wg sync.WaitGroup
	wg.Add(parallelism)
	for i := 0; i < parallelism; i++ {
		go func() {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case target, ok := <-jobs:
					if !ok {
						return
					}
					err := retry(ctx, step.RetryLimit(), func() error {
						return step.Delete(ctx, target, e.Wait)
					})
					if err != nil {
						if step.ContinueOnError() {
							logger.Info(
								"Deletion failed but continuing",
								"step", step.Name(),
								"resource", target.Name,
								"type", target.Type,
								"error", err,
							)
							continue
						}
						errs <- fmt.Errorf("%s: failed deleting %s (%s): %w", step.Name(), target.Name, target.Type, err)
						continue
					}
					logger.Info(
						"Deleted resource",
						"step", step.Name(),
						"resource", target.Name,
						"type", target.Type,
						"id", target.ID,
					)
				}
			}
		}()
	}

	enqueueTargets := func() {
		defer close(jobs)
		for _, target := range targets {
			select {
			case <-ctx.Done():
				return
			case jobs <- target:
			}
		}
	}
	enqueueTargets()
	wg.Wait()
	close(errs)

	collectedErrs := make([]error, 0)
	for err := range errs {
		collectedErrs = append(collectedErrs, err)
	}
	if len(collectedErrs) > 0 {
		return errors.Join(collectedErrs...)
	}

	verifyErr := retry(ctx, step.RetryLimit(), func() error {
		return step.Verify(ctx)
	})
	if verifyErr == nil {
		return nil
	}
	if step.ContinueOnError() {
		logger.Info(
			"Verification failed in best-effort mode; continuing",
			"step", step.Name(),
			"error", verifyErr,
		)
		return nil
	}
	return fmt.Errorf("%s: verification failed: %w", step.Name(), verifyErr)
}

func retry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < DefaultRetries {
		maxAttempts = DefaultRetries
	}

	var lastErr error

	backoff := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   2,
		Jitter:   0.5,
		Steps:    maxAttempts,
		Cap:      30 * time.Second,
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := fn(); err != nil {
			lastErr = err
			// Continue retrying until backoff steps are exhausted.
			return false, nil
		}
		return true, nil
	})
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if wait.Interrupted(err) && lastErr != nil {
		return lastErr
	}
	return err
}
