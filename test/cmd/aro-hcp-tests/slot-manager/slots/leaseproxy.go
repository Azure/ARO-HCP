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
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

const DefaultLeaseProxyTimeout = 2 * time.Minute

var leaseProxyRequestBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2,
	Jitter:   0.1,
	Steps:    5,
	Cap:      15 * time.Second,
}

type acquireLeaseResponse struct {
	Names []string `json:"names"`
}

type releaseLeaseRequest struct {
	Names []string `json:"names"`
}

func AcquireLease(ctx context.Context, leaseProxyServerURL, resourceType string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(leaseProxyServerURL) == "" {
		return "", fmt.Errorf("LEASE_PROXY_SERVER_URL must be set")
	}
	if strings.TrimSpace(resourceType) == "" {
		return "", fmt.Errorf("resource type must not be empty")
	}
	if timeout <= 0 {
		return "", fmt.Errorf("timeout must be greater than zero")
	}

	query := url.Values{}
	query.Set("type", resourceType)
	query.Set("count", "1")

	response, err := doLeaseProxyRequestWithRetry(ctx, timeout, func(ctx context.Context, client *http.Client) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(leaseProxyServerURL, "/")+"/lease/acquire?"+query.Encode(), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create lease acquire request: %w", err)
		}
		return client.Do(request)
	})
	if err != nil {
		return "", fmt.Errorf("failed to acquire lease type %q: %w", resourceType, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("failed to acquire lease type %q: unexpected status %s", resourceType, response.Status)
	}

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
	if strings.TrimSpace(leaseProxyServerURL) == "" {
		return fmt.Errorf("LEASE_PROXY_SERVER_URL must be set")
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("lease name must not be empty")
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}

	requestBody, err := json.Marshal(releaseLeaseRequest{Names: []string{name}})
	if err != nil {
		return fmt.Errorf("failed to marshal lease release request: %w", err)
	}

	response, err := doLeaseProxyRequestWithRetry(ctx, timeout, func(ctx context.Context, client *http.Client) (*http.Response, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(leaseProxyServerURL, "/")+"/lease/release", bytes.NewReader(requestBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create lease release request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		return client.Do(request)
	})
	if err != nil {
		return fmt.Errorf("failed to release lease %q: %w", name, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("failed to release lease %q: unexpected status %s", name, response.Status)
	}

	return nil
}

func doLeaseProxyRequestWithRetry(ctx context.Context, timeout time.Duration, requestFunc func(context.Context, *http.Client) (*http.Response, error)) (*http.Response, error) {
	client := &http.Client{Timeout: timeout}

	var response *http.Response
	var lastErr error
	err := wait.ExponentialBackoffWithContext(ctx, leaseProxyRequestBackoff, func(ctx context.Context) (bool, error) {
		currentResponse, err := requestFunc(ctx, client)
		if err != nil {
			lastErr = err
			return false, nil
		}
		if currentResponse.StatusCode == http.StatusTooManyRequests || currentResponse.StatusCode >= 500 {
			lastErr = fmt.Errorf("unexpected retryable status %s", currentResponse.Status)
			currentResponse.Body.Close()
			return false, nil
		}

		response = currentResponse
		lastErr = nil
		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	return response, nil
}
