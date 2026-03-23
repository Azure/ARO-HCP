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

package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Target struct {
	ID   string
	Name string
	Type string
}

type Step interface {
	Name() string
	Discover(ctx context.Context) ([]Target, error)
	Delete(ctx context.Context, target Target, wait bool) error
	Verify(ctx context.Context) error
	RetryLimit() int
	ContinueOnError() bool
}

type Engine struct {
	Steps       []Step
	Parallelism int
	DryRun      bool
	Wait        bool
}

func (e *Engine) Run(ctx context.Context) error {
	if e.Parallelism < 1 {
		e.Parallelism = 4
	}
	for _, step := range e.Steps {
		if err := e.runStep(ctx, step); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) runStep(ctx context.Context, step Step) error {
	logger := LoggerFromContext(ctx)
	targets, err := step.Discover(ctx)
	if err != nil {
		if step.ContinueOnError() {
			logger.Info("Discovery failed but continuing", "step", step.Name(), "error", err)
			return nil
		}
		return fmt.Errorf("%s: discovery failed: %w", step.Name(), err)
	}
	if len(targets) == 0 {
		return nil
	}

	if e.DryRun {
		logger.Info("Dry-run deletion step", "step", step.Name(), "targets", len(targets))
		for _, target := range targets {
			logger.V(1).Info(
				"Dry-run deletion target",
				"step", step.Name(),
				"resource", target.Name,
				"type", target.Type,
				"id", target.ID,
			)
		}
		return nil
	}

	sem := make(chan struct{}, e.Parallelism)
	group, groupCtx := errgroup.WithContext(ctx)
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			continue
		}
		group.Go(func() error {
			defer func() { <-sem }()
			err := retry(groupCtx, step.RetryLimit(), func() error {
				return step.Delete(groupCtx, target, e.Wait)
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
					return nil
				}
				return fmt.Errorf("%s: failed deleting %s (%s): %w", step.Name(), target.Name, target.Type, err)
			}
			logger.V(1).Info(
				"Deleted resource",
				"step", step.Name(),
				"resource", target.Name,
				"type", target.Type,
				"id", target.ID,
			)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}

	if err := step.Verify(ctx); err != nil {
		if step.ContinueOnError() {
			logger.Info("Verification failed but continuing", "step", step.Name(), "error", err)
			return nil
		}
		return fmt.Errorf("%s: verification failed: %w", step.Name(), err)
	}
	return nil
}

type DiscoverFn func(ctx context.Context, resourceType string) ([]Target, error)
type DeleteFn func(ctx context.Context, target Target, wait bool) error
type VerifyFn func(ctx context.Context) error
type SkipFn func(ctx context.Context, target Target) (skip bool, reason string, err error)

type DeletionStep struct {
	ResourceType string
	DiscoverFn   DiscoverFn
	DeleteFn     DeleteFn
	VerifyFn     VerifyFn
	SkipFn       SkipFn
	Options      StepOptions
}

func (ds DeletionStep) Name() string {
	return ds.Options.Name
}

func (ds DeletionStep) RetryLimit() int {
	if ds.Options.Retries < 1 {
		return 1
	}
	return ds.Options.Retries
}

func (ds DeletionStep) ContinueOnError() bool {
	return ds.Options.ContinueOnError
}

func (ds DeletionStep) Discover(ctx context.Context) ([]Target, error) {
	logger := LoggerFromContext(ctx)
	if ds.DiscoverFn == nil {
		return nil, fmt.Errorf("DiscoverFn is required")
	}
	targets, err := ds.DiscoverFn(ctx, ds.ResourceType)
	if err != nil {
		return nil, err
	}
	filtered := make([]Target, 0, len(targets))
	for _, target := range targets {
		if ds.SkipFn != nil {
			skip, reason, err := ds.SkipFn(ctx, target)
			if err != nil {
				return nil, err
			}
			if skip {
				logger.Info("Skipping deletion target", "step", ds.Name(), "resource", target.Name, "reason", reason)
				continue
			}
		}
		filtered = append(filtered, target)
	}
	return filtered, nil
}

func (ds DeletionStep) Delete(ctx context.Context, target Target, wait bool) error {
	if ds.DeleteFn == nil {
		return fmt.Errorf("DeleteFn is required")
	}
	return ds.DeleteFn(ctx, target, wait)
}

func (ds DeletionStep) Verify(ctx context.Context) error {
	if ds.VerifyFn == nil {
		return nil
	}
	return ds.VerifyFn(ctx)
}

func retry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
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

func ContextWithLogger(ctx context.Context, logger logr.Logger) context.Context {
	return logr.NewContext(ctx, logger)
}

func LoggerFromContext(ctx context.Context) logr.Logger {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		panic(fmt.Sprintf("logger missing from context: %v", err))
	}
	return logger
}
