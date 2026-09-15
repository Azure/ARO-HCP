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

package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

const (
	// CopilotSessionErrorTypeRateLimit identifies a Copilot rate-limit response.
	CopilotSessionErrorTypeRateLimit = "rate_limit"

	// The Copilot runtime emits auto_mode_switch.requested immediately after an
	// eligible rate-limit error. Keep the error path bounded if that follow-up
	// event is absent because of a runtime or connection failure.
	copilotRetryAfterWait = time.Second
)

// CopilotSessionError preserves structured error details reported by the
// Copilot SDK. Callers can inspect it with errors.As while continuing to handle
// it as a standard error.
type CopilotSessionError struct {
	Provider              string
	ErrorType             string
	ErrorCode             *string
	StatusCode            *int32
	Message               string
	ProviderCallID        *string
	ServiceRequestID      *string
	Stack                 *string
	URL                   *string
	EligibleForAutoSwitch *bool
	// RetryAfterSeconds is the number of seconds until the rate limit resets,
	// when reported by the Copilot runtime.
	RetryAfterSeconds *int64
	Err               error
}

// Error returns the underlying provider error text when available.
func (e *CopilotSessionError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the original provider error.
func (e *CopilotSessionError) Unwrap() error {
	return e.Err
}

type copilotSessionErrorCapture struct {
	provider string

	mu    sync.Mutex
	first *CopilotSessionError
	// retryAfterReady is closed once no retry metadata is expected or the
	// follow-up auto-mode-switch event has been captured.
	retryAfterReady chan struct{}
	retryAfterOnce  sync.Once
}

func newCopilotSessionErrorCapture(provider string) *copilotSessionErrorCapture {
	return &copilotSessionErrorCapture{
		provider:        provider,
		retryAfterReady: make(chan struct{}),
	}
}

func (c *copilotSessionErrorCapture) record(event copilot.SessionEvent) {
	switch data := event.Data.(type) {
	case *copilot.SessionErrorData:
		captured := &CopilotSessionError{
			Provider:              c.provider,
			ErrorType:             data.ErrorType,
			ErrorCode:             clonePointer(data.ErrorCode),
			StatusCode:            clonePointer(data.StatusCode),
			Message:               data.Message,
			ProviderCallID:        clonePointer(data.ProviderCallID),
			ServiceRequestID:      clonePointer(data.ServiceRequestID),
			Stack:                 clonePointer(data.Stack),
			URL:                   clonePointer(data.URL),
			EligibleForAutoSwitch: clonePointer(data.EligibleForAutoSwitch),
		}

		c.mu.Lock()
		if c.first != nil {
			c.mu.Unlock()
			return
		}
		c.first = captured
		c.mu.Unlock()

		if data.EligibleForAutoSwitch == nil || !*data.EligibleForAutoSwitch {
			c.markRetryAfterReady()
		}
	case *copilot.AutoModeSwitchRequestedData:
		c.mu.Lock()
		matched := c.first != nil &&
			c.first.EligibleForAutoSwitch != nil &&
			*c.first.EligibleForAutoSwitch &&
			sameOptionalString(c.first.ErrorCode, data.ErrorCode)
		if matched {
			c.first.RetryAfterSeconds = clonePointer(data.RetryAfterSeconds)
		}
		c.mu.Unlock()
		if matched {
			c.markRetryAfterReady()
		}
	}
}

func sameOptionalString(left, right *string) bool {
	return left == nil || right == nil || *left == *right
}

func (c *copilotSessionErrorCapture) withCause(ctx context.Context, cause error) *CopilotSessionError {
	c.waitForRetryAfter(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.first == nil {
		return nil
	}

	captured := *c.first
	captured.Err = cause
	return &captured
}

func (c *copilotSessionErrorCapture) waitForRetryAfter(ctx context.Context) {
	c.mu.Lock()
	first := c.first
	c.mu.Unlock()
	if first == nil || first.EligibleForAutoSwitch == nil || !*first.EligibleForAutoSwitch {
		return
	}

	timer := time.NewTimer(copilotRetryAfterWait)
	defer timer.Stop()
	select {
	case <-c.retryAfterReady:
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (c *copilotSessionErrorCapture) markRetryAfterReady() {
	c.retryAfterOnce.Do(func() {
		close(c.retryAfterReady)
	})
}

func wrapCopilotSessionError(ctx context.Context, capture *copilotSessionErrorCapture, cause error) error {
	if sessionErr := capture.withCause(ctx, cause); sessionErr != nil {
		return fmt.Errorf("copilot session failed: %w", sessionErr)
	}
	return fmt.Errorf("copilot session failed: %w", cause)
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
