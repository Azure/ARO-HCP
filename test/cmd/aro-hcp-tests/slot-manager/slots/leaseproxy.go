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

package slots

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const DefaultLeaseProxyTimeout = 30 * time.Second

var ErrLeasePoolUnavailableNow = errors.New("lease pool temporarily unavailable")

var leaseProxyRequestBackoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    6,
	Cap:      15 * time.Second,
}

type acquireLeaseResponse struct {
	Names []string `json:"names"`
}

type releaseLeaseRequest struct {
	Names []string `json:"names"`
}

type LeasePoolUnavailableError struct {
	ResourceType string
	Cause        error
}

func (e *LeasePoolUnavailableError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("failed to immediately acquire lease type %q: %s", e.ResourceType, ErrLeasePoolUnavailableNow.Error())
	}
	return fmt.Sprintf("failed to immediately acquire lease type %q within lease-proxy timeout budget: %v", e.ResourceType, e.Cause)
}

func (e *LeasePoolUnavailableError) Is(target error) bool {
	return target == ErrLeasePoolUnavailableNow
}

func (e *LeasePoolUnavailableError) Unwrap() error {
	return e.Cause
}

type retryableLeaseProxyError struct {
	Cause error
}

func (e *retryableLeaseProxyError) Error() string {
	if e.Cause == nil {
		return "lease proxy request did not succeed before retry budget expired"
	}
	return e.Cause.Error()
}

func (e *retryableLeaseProxyError) Unwrap() error {
	return e.Cause
}

func AcquireLease(ctx context.Context, leaseProxyServerURL, resourceType string, timeout time.Duration) (string, error) {
	query := url.Values{}
	query.Set("type", resourceType)
	query.Set("count", "1")

	response, err := doLeaseProxyRequestWithRetry(
		ctx,
		timeout,
		func(ctx context.Context, client *http.Client) (*http.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(leaseProxyServerURL, "/")+"/lease/acquire?"+query.Encode(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create lease acquire request: %w", err)
			}
			return client.Do(request)
		},
		func(response *http.Response, responseBody []byte) (success bool, fatal error) {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return true, nil
			}
			if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
				return false, nil // retryable
			}
			return false, fmt.Errorf("failed to acquire lease type %q: %s", resourceType, leaseProxyResponseMessage(response.Status, responseBody))
		},
	)
	if err != nil {
		var retryableErr *retryableLeaseProxyError
		if errors.As(err, &retryableErr) {
			return "", &LeasePoolUnavailableError{
				ResourceType: resourceType,
				Cause:        retryableErr.Unwrap(),
			}
		}
		return "", err
	}
	defer response.Body.Close()

	acquireResponse := &acquireLeaseResponse{}
	if err := json.NewDecoder(response.Body).Decode(acquireResponse); err != nil {
		return "", fmt.Errorf("failed to decode lease acquire response for type %q: %w", resourceType, err)
	}
	if len(acquireResponse.Names) != 1 {
		return "", fmt.Errorf("expected exactly one leased resource name for type %q, got %d", resourceType, len(acquireResponse.Names))
	}
	return acquireResponse.Names[0], nil
}

func ReleaseLease(ctx context.Context, leaseProxyServerURL, name string, timeout time.Duration) error {
	requestBody, err := json.Marshal(releaseLeaseRequest{Names: []string{name}})
	if err != nil {
		return fmt.Errorf("failed to marshal lease release request: %w", err)
	}

	response, err := doLeaseProxyRequestWithRetry(
		ctx,
		timeout,
		func(ctx context.Context, client *http.Client) (*http.Response, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(leaseProxyServerURL, "/")+"/lease/release", bytes.NewReader(requestBody))
			if err != nil {
				return nil, fmt.Errorf("failed to create lease release request: %w", err)
			}
			request.Header.Set("Content-Type", "application/json")
			return client.Do(request)
		},
		func(response *http.Response, responseBody []byte) (success bool, fatal error) {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return true, nil
			}
			if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
				return false, nil // retryable
			}
			return false, fmt.Errorf("failed to release lease %q: %s", name, leaseProxyResponseMessage(response.Status, responseBody))
		},
	)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	return nil
}

// doLeaseProxyRequestWithRetry executes an HTTP request with exponential
// backoff until it succeeds, returns a fatal response classification, the
// parent context is canceled, or the provided timeout budget is exhausted.
// The classifyResponse callback inspects each response and signals one of
// three outcomes:
//
//   - success  (success=true,  fatal=nil)  → stop retrying, return the response
//   - retry    (success=false, fatal=nil)  → wait and try again (transient: 429, 5xx, network errors)
//   - fatal    (success=false, fatal=err)  → stop retrying, propagate the error immediately
//
// Network-level errors from requestFunc (connection refused, DNS failure, etc.)
// are always treated as retryable. If the retry budget is exhausted, the
// helper returns the last observed retryable error wrapped in
// retryableLeaseProxyError so callers can distinguish "did not succeed in
// time" from fatal responses like type-not-found.
func doLeaseProxyRequestWithRetry(
	ctx context.Context,
	timeout time.Duration,
	requestFunc func(context.Context, *http.Client) (*http.Response, error),
	classifyResponse func(*http.Response, []byte) (success bool, fatal error),
) (*http.Response, error) {
	requestCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	client := &http.Client{}

	var response *http.Response
	var lastErr error
	retryableFailure := false
	err := wait.ExponentialBackoffWithContext(requestCtx, leaseProxyRequestBackoff, func(attemptCtx context.Context) (bool, error) {
		currentResponse, err := requestFunc(attemptCtx, client)
		if err != nil {
			lastErr = err
			retryableFailure = true
			return false, nil // retry
		}
		responseBody, err := io.ReadAll(currentResponse.Body)
		currentResponse.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read lease proxy response body: %w", err)
			retryableFailure = true
			return false, nil // retry
		}
		currentResponse.Body = io.NopCloser(bytes.NewReader(responseBody))

		success, fatal := classifyResponse(currentResponse, responseBody)
		if fatal != nil {
			lastErr = fatal
			retryableFailure = false
			return false, fatal // stop immediately
		}
		if !success {
			lastErr = fmt.Errorf("retryable status %s", currentResponse.Status)
			retryableFailure = true
			return false, nil // retry
		}

		response = currentResponse
		lastErr = nil
		retryableFailure = false
		return true, nil // done
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if retryableFailure {
			if lastErr == nil {
				lastErr = err
			}
			return nil, &retryableLeaseProxyError{Cause: lastErr}
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	return response, nil
}

func leaseProxyResponseMessage(status string, responseBody []byte) string {
	message := strings.TrimSpace(string(responseBody))
	if message == "" {
		return fmt.Sprintf("unexpected status %s", status)
	}
	return fmt.Sprintf("unexpected status %s: %s", status, message)
}
