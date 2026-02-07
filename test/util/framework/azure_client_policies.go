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

package framework

import (
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// armSystemDataPolicy adds ARM system data headers for direct RP requests.
// This is used in development environments where requests go directly to the
// RP frontend without going through ARM.
type armSystemDataPolicy struct{}

func (p *armSystemDataPolicy) Do(req *policy.Request) (*http.Response, error) {
	frontendURL, err := url.Parse(frontendAddress())
	if err != nil {
		return nil, fmt.Errorf("failed to parse frontend address: %w", err)
	}

	if req.Raw().URL.Host == frontendURL.Host {
		systemData := fmt.Sprintf(`{"createdBy": "e2e-test", "createdByType": "Application", "createdAt": "%s"}`, time.Now().UTC().Format(time.RFC3339))
		req.Raw().Header.Set("X-Ms-Arm-Resource-System-Data", systemData)
		req.Raw().Header.Set("X-Ms-Identity-Url", "https://dummyhost.identity.azure.net")
	}
	return req.Next()
}

// lroPollerRetryDeploymentNotFoundPolicy is a pipeline policy that retries transient
// 404 DeploymentNotFound errors during ARM deployment LRO polling.
//
// This addresses Azure Resource Manager's eventual consistency behavior where
// the operationStatuses endpoint may return 404 immediately after a deployment
// is created, even though the deployment will eventually succeed.
//
// The policy only activates for GET requests to URLs matching:
//
//	/providers/Microsoft.Resources/deployments/{name}/operationStatuses/{id}
//
// All other requests pass through unmodified.
type lroPollerRetryDeploymentNotFoundPolicy struct {
	MaxRetries     int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	MaxRetryWindow time.Duration
}

func NewLROPollerRetryDeploymentNotFoundPolicy() *lroPollerRetryDeploymentNotFoundPolicy {
	return &lroPollerRetryDeploymentNotFoundPolicy{
		MaxRetries:     5,
		BaseBackoff:    2 * time.Second,
		MaxBackoff:     10 * time.Second,
		MaxRetryWindow: 90 * time.Second,
	}
}

func (p *lroPollerRetryDeploymentNotFoundPolicy) Do(req *policy.Request) (*http.Response, error) {
	if !strings.EqualFold(req.Raw().Method, http.MethodGet) {
		return req.Next()
	}
	path := req.Raw().URL.Path
	if !strings.Contains(path, "/providers/Microsoft.Resources/deployments/") || !strings.Contains(path, "/operationStatuses/") {
		return req.Next()
	}

	start := time.Now()
	attempt := 0

	for {

		resp, err, retry := func(req *policy.Request) (resp *http.Response, err error, retry bool) {
			retryReq := req.Clone(req.Raw().Context())
			if err := retryReq.RewindBody(); err != nil {
				return nil, err, false
			}

			resp, err = retryReq.Next()
			if err == nil {
				return resp, nil, false
			}
			defer func(resp *http.Response) {
				if resp != nil {
					if err := resp.Body.Close(); err != nil {
						ginkgo.GinkgoLogr.Error(err, "failed to close response body")
					}
				}
			}(resp)

			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) &&
				respErr.StatusCode == http.StatusNotFound &&
				strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound") {
				return nil, err, true
			}
			// don't retry on any other error
			return nil, err, false
		}(req)

		if !retry {
			return resp, err
		}

		if attempt >= p.MaxRetries || time.Since(start) >= p.MaxRetryWindow {
			err := fmt.Errorf("max retries or max retry window reached: %w", err)
			return resp, err
		}

		sleep := p.backoff(attempt)

		ginkgo.GinkgoLogr.Info("transient 404 DeploymentNotFound on operationStatuses",
			"attempt", attempt+1,
			"sleep", sleep.String(),
			"url", req.Raw().URL.String())

		select {
		case <-time.After(sleep):
			// retry
		case <-req.Raw().Context().Done():
			return nil, req.Raw().Context().Err()
		}

		attempt++
	}
}

func (p *lroPollerRetryDeploymentNotFoundPolicy) backoff(attempt int) time.Duration {
	sleep := min(p.BaseBackoff<<attempt, p.MaxBackoff)
	jitter := time.Duration(rand.Int63n(int64(max(p.BaseBackoff/2, time.Millisecond))))
	return sleep + jitter
}
