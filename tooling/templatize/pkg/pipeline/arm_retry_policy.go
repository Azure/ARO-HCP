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

package pipeline

import (
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// deploymentNotFoundRetryPolicy is a pipeline policy that retries transient
// 404 DeploymentNotFound errors during ARM deployment LRO polling.
//
// Azure Resource Manager has eventual consistency: after creating an LRO,
// GET requests to the operationStatuses endpoint or the deployment itself
// may briefly return 404 DeploymentNotFound. This policy transparently
// retries those transient failures with exponential backoff.
//
// The policy only activates for GET requests whose URL path contains
// "/providers/microsoft.resources/deployments/" (case-insensitive),
// which matches deployment GETs, LRO operationStatuses polling, and
// deployment operations listing.
type deploymentNotFoundRetryPolicy struct {
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

func newDeploymentNotFoundRetryPolicy() *deploymentNotFoundRetryPolicy {
	return &deploymentNotFoundRetryPolicy{
		maxRetries:  5,
		baseBackoff: 1 * time.Second,
		maxBackoff:  10 * time.Second,
	}
}

func (p *deploymentNotFoundRetryPolicy) Do(req *policy.Request) (*http.Response, error) {
	if !strings.EqualFold(req.Raw().Method, http.MethodGet) {
		return req.Next()
	}

	path := strings.ToLower(req.Raw().URL.Path)
	if !strings.Contains(path, "/providers/microsoft.resources/deployments/") {
		return req.Next()
	}

	logger := logr.FromContextOrDiscard(req.Raw().Context())

	attempt := 0
	for {
		resp, err, retry := func() (*http.Response, error, bool) {
			retryReq := req.Clone(req.Raw().Context())
			if err := retryReq.RewindBody(); err != nil {
				return nil, err, false
			}

			resp, err := retryReq.Next()
			if err == nil {
				return resp, nil, false
			}

			var respErr *azcore.ResponseError
			if errors.As(err, &respErr) &&
				respErr.StatusCode == http.StatusNotFound &&
				strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound") {
				if resp != nil {
					resp.Body.Close()
				}
				return nil, err, true
			}
			return resp, err, false
		}()

		if !retry {
			return resp, err
		}

		if attempt >= p.maxRetries {
			return nil, fmt.Errorf("deployment not found after %d retries: %w", p.maxRetries, err)
		}

		sleep := p.backoff(attempt)

		logger.V(1).Info("retrying transient 404 DeploymentNotFound",
			"attempt", attempt+1,
			"sleep", sleep.String(),
			"url", req.Raw().URL.String(),
		)

		select {
		case <-time.After(sleep):
			// continue to retry
		case <-req.Raw().Context().Done():
			return nil, req.Raw().Context().Err()
		}

		attempt++
	}
}

func (p *deploymentNotFoundRetryPolicy) backoff(attempt int) time.Duration {
	sleep := min(p.baseBackoff<<uint(attempt), p.maxBackoff)
	jitter := time.Duration(rand.Int63n(int64(max(p.baseBackoff/2, time.Millisecond))))
	return sleep + jitter
}
