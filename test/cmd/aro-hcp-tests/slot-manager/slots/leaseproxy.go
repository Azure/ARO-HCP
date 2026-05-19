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

const DefaultLeaseProxyTimeout = 2 * time.Minute

var ErrLeasePoolExhausted = errors.New("lease pool exhausted")

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

type LeasePoolExhaustedError struct {
	ResourceType string
	Message      string
}

func (e *LeasePoolExhaustedError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = ErrLeasePoolExhausted.Error()
	}
	return fmt.Sprintf("failed to acquire lease type %q: %s", e.ResourceType, message)
}

func (e *LeasePoolExhaustedError) Is(target error) bool {
	return target == ErrLeasePoolExhausted
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
			if isLeasePoolExhaustedResponse(response.StatusCode, responseBody) {
				return false, &LeasePoolExhaustedError{
					ResourceType: resourceType,
					Message:      strings.TrimSpace(string(responseBody)),
				}
			}
			if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
				return false, nil // retryable
			}
			return false, fmt.Errorf("failed to acquire lease type %q: %s", resourceType, leaseProxyResponseMessage(response.Status, responseBody))
		},
	)
	if err != nil {
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
// backoff. The classifyResponse callback inspects each response and signals
// one of three outcomes:
//
//   - success  (success=true,  fatal=nil)  → stop retrying, return the response
//   - retry    (success=false, fatal=nil)  → wait and try again (transient: 429, 5xx, network errors)
//   - fatal    (success=false, fatal=err)  → stop retrying, propagate the error immediately
//
// Network-level errors from requestFunc (connection refused, DNS failure, etc.)
// are always treated as retryable. If all retry attempts are exhausted, the
// last observed error is returned.
func doLeaseProxyRequestWithRetry(
	ctx context.Context,
	timeout time.Duration,
	requestFunc func(context.Context, *http.Client) (*http.Response, error),
	classifyResponse func(*http.Response, []byte) (success bool, fatal error),
) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}

	var response *http.Response
	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, leaseProxyRequestBackoff, func(ctx context.Context) (bool, error) {
		currentResponse, err := requestFunc(ctx, client)
		if err != nil {
			lastErr = err
			return false, nil // retry
		}
		responseBody, err := io.ReadAll(currentResponse.Body)
		currentResponse.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read lease proxy response body: %w", err)
			return false, nil // retry
		}
		currentResponse.Body = io.NopCloser(bytes.NewReader(responseBody))

		success, fatal := classifyResponse(currentResponse, responseBody)
		if fatal != nil {
			lastErr = fatal
			return false, fatal // stop immediately
		}
		if !success {
			lastErr = fmt.Errorf("retryable status %s", currentResponse.Status)
			return false, nil // retry
		}

		response = currentResponse
		lastErr = nil
		return true, nil // done
	})
	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	return response, nil
}

// isLeasePoolExhaustedResponse determines whether a proxy response indicates
// that all resources of the requested type are currently leased (pool
// exhaustion) as opposed to the resource type not existing at all, or some
// unrelated server error.
//
// The ci-tools lease proxy (pkg/lease/proxy) already encodes the distinction
// via HTTP status codes:
//   - 404 → lease.ErrTypeNotFound ("resource type not found")
//   - 500 → lease.ErrNotFound     ("resources not found") or other errors
//
// For 500 responses, we additionally check the body against the known error
// strings produced by the Boskos ecosystem to avoid false positives from
// unrelated 500 errors (e.g. "Failed to get lease client"). The strings
// originate from:
//   - Boskos client sentinel: "resources not found"          (sigs.k8s.io/boskos/client.ErrNotFound)
//   - Boskos server ranch:    "no available resource <name>" (sigs.k8s.io/boskos/ranch.ResourceNotFound)
func isLeasePoolExhaustedResponse(statusCode int, responseBody []byte) bool {
	if statusCode == http.StatusNotFound {
		return false
	}
	if statusCode < http.StatusInternalServerError {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(string(responseBody)))

	if strings.Contains(message, "resources not found") {
		return true
	}
	if strings.Contains(message, "no available resource") {
		return true
	}

	return false
}

func leaseProxyResponseMessage(status string, responseBody []byte) string {
	message := strings.TrimSpace(string(responseBody))
	if message == "" {
		return fmt.Sprintf("unexpected status %s", status)
	}
	return fmt.Sprintf("unexpected status %s: %s", status, message)
}
