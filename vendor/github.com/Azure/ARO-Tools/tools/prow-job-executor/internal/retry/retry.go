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

// Package retry provides a small, generic exponential-backoff retry helper shared
// by the prow-job-executor's transient-failure paths (Gangway job submission, job
// status polling, and the Key Vault prow-token lookup).
package retry

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"
)

// WithValue invokes fn with exponential backoff and returns its value once it
// succeeds, retrying only errors that isRetryable classifies as transient.
//
// Behavior:
//   - fn is called at least once. On success its value is returned immediately.
//   - When fn returns an error that isRetryable reports as false (a permanent or
//     deterministic failure), WithValue stops immediately and propagates that error
//     unchanged, without consuming the remaining backoff budget.
//   - When isRetryable reports true, the error is logged at info level (if a logr
//     logger is present on ctx) and the call is retried until the backoff budget is
//     exhausted, after which the last transient error is wrapped and returned.
//   - A cancelled or expired parent context always takes precedence: its error is
//     returned as-is rather than masked behind the last transient error.
//
// fn must respect ctx for cancellation. The parent context bounds the total runtime
// regardless of the backoff schedule.
func WithValue[T any](ctx context.Context, backoff wait.Backoff, isRetryable func(error) bool, fn func(ctx context.Context) (T, error)) (T, error) {
	logger, err := logr.FromContext(ctx)
	if err != nil {
		logger = logr.Discard()
	}

	var result T
	var lastErr error
	condition := func(ctx context.Context) (bool, error) {
		v, err := fn(ctx)
		if err != nil {
			// A cancelled/expired parent context is terminal: stop immediately,
			// without logging a misleading "will retry" or recording it as the last
			// transient error. (Some callers' isRetryable does not special-case
			// context errors, e.g. GetJobStatus.)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, ctxErr
			}

			// Permanent/deterministic failures surface immediately instead of
			// after a long backoff.
			if !isRetryable(err) {
				return false, err // Stop retrying and propagate the error as-is.
			}

			lastErr = err
			logger.Info("Operation failed with a transient error, will retry", "error", err.Error())
			return false, nil
		}

		result = v
		return true, nil // Success, stop retrying.
	}

	if err := wait.ExponentialBackoffWithContext(ctx, backoff, condition); err != nil {
		// A cancelled/expired parent context takes precedence: report it as-is
		// rather than masking it behind the last transient error.
		if ctxErr := ctx.Err(); ctxErr != nil {
			var zero T
			return zero, ctxErr
		}
		// Retries were exhausted: surface the last transient error for context.
		if lastErr != nil {
			var zero T
			return zero, fmt.Errorf("retry budget exhausted after transient errors: %w", lastErr)
		}
		// A permanent error returned by the condition propagates unchanged.
		var zero T
		return zero, err
	}

	return result, nil
}
