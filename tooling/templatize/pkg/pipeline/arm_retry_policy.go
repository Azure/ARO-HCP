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
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// lroPollerRetryDeploymentNotFoundPolicy retries transient 404
// DeploymentNotFound errors from ARM deployment LRO operation status polling.
type lroPollerRetryDeploymentNotFoundPolicy struct {
	backoff wait.Backoff
}

func newLROPollerRetryDeploymentNotFoundPolicy() *lroPollerRetryDeploymentNotFoundPolicy {
	return &lroPollerRetryDeploymentNotFoundPolicy{
		backoff: wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Jitter:   0.1,
			Steps:    6,
		},
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

	logger := logr.FromContextOrDiscard(req.Raw().Context())
	var resp *http.Response

	err := wait.ExponentialBackoffWithContext(req.Raw().Context(), p.backoff, func(ctx context.Context) (bool, error) {
		if resp != nil {
			if resp.Body != nil {
				if closeErr := resp.Body.Close(); closeErr != nil {
					logger.Error(closeErr, "failed to close response body")
				}
			}
			resp = nil
		}

		retryReq := req.Clone(ctx)
		if err := retryReq.RewindBody(); err != nil {
			return false, err
		}

		var err error
		resp, err = retryReq.Next()
		if err != nil {
			return false, err
		}

		if resp.StatusCode != http.StatusNotFound {
			return true, nil
		}

		var respErr *azcore.ResponseError
		if errors.As(runtime.NewResponseError(resp), &respErr) &&
			strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound") {
			return false, nil
		}
		return true, nil
	})
	if err == nil {
		return resp, nil
	}
	if req.Raw().Context().Err() != nil {
		if resp != nil && resp.Body != nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				logger.Error(closeErr, "failed to close response body")
			}
		}
		return nil, req.Raw().Context().Err()
	}
	if wait.Interrupted(err) && resp != nil {
		// Return the final response unchanged so the SDK poller can create its
		// normal ResponseError after the policy pipeline has completed.
		return resp, nil
	}
	return resp, err
}
