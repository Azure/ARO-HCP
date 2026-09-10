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

package azclient

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

// lroPollerRetryPolicy handles two transient ARM deployment LRO poll responses:
// DeploymentNotFound while a new deployment becomes visible, and one
// Unauthorized response after azcore expires the rejected cached token.
type lroPollerRetryPolicy struct {
	backoff wait.Backoff
}

// LROPollerRetryPolicyOptions configures the LRO poller retry policy.
type LROPollerRetryPolicyOptions struct {
	Backoff wait.Backoff
}

// NewLROPollerRetryPolicy returns a policy scoped to GET requests for ARM
// deployment operationStatuses endpoints. DeploymentNotFound and Unauthorized
// responses share one retry budget, but only the first Unauthorized response is
// retried so persistent authentication and authorization failures surface
// quickly.
func NewLROPollerRetryPolicy(options *LROPollerRetryPolicyOptions) policy.Policy {
	backoff := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   2,
		Jitter:   0.1,
		Steps:    6,
	}
	if options != nil {
		backoff = options.Backoff
	}
	return &lroPollerRetryPolicy{
		backoff: backoff,
	}
}

type retryReason int

const (
	retryNone retryReason = iota
	retryDeploymentNotFound
	retryUnauthorized
)

func (p *lroPollerRetryPolicy) Do(req *policy.Request) (*http.Response, error) {
	if !strings.EqualFold(req.Raw().Method, http.MethodGet) {
		return req.Next()
	}
	path := req.Raw().URL.Path
	if !strings.Contains(path, "/providers/Microsoft.Resources/deployments/") || !strings.Contains(path, "/operationStatuses/") {
		return req.Next()
	}

	logger := logr.FromContextOrDiscard(req.Raw().Context())
	var resp *http.Response
	var lastRetryableErr error
	authRetried := false

	err := wait.ExponentialBackoffWithContext(req.Raw().Context(), p.backoff, func(ctx context.Context) (bool, error) {
		closeResponse(resp, logger)
		if resp == nil {
			closeResponse(responseFromError(lastRetryableErr), logger)
		}
		resp = nil
		lastRetryableErr = nil

		retryReq := req.Clone(ctx)
		if err := retryReq.RewindBody(); err != nil {
			return false, err
		}

		var requestErr error
		resp, requestErr = retryReq.Next()
		switch retryReasonForResponse(resp, requestErr) {
		case retryDeploymentNotFound:
			lastRetryableErr = requestErr
			logger.Info("transient 404 DeploymentNotFound on operationStatuses",
				"url", req.Raw().URL.String())
			return false, nil
		case retryUnauthorized:
			if authRetried {
				if requestErr != nil {
					return false, requestErr
				}
				return true, nil
			}
			authRetried = true
			lastRetryableErr = requestErr
			logger.Info("transient 401 Unauthorized on operationStatuses, retrying with a refreshed token",
				"url", req.Raw().URL.String())
			return false, nil
		default:
			if requestErr != nil {
				return false, requestErr
			}
			return true, nil
		}
	})
	if err == nil {
		return resp, nil
	}
	if req.Raw().Context().Err() != nil {
		closeResponse(resp, logger)
		if resp == nil {
			closeResponse(responseFromError(lastRetryableErr), logger)
		}
		return nil, req.Raw().Context().Err()
	}
	if wait.Interrupted(err) {
		if resp != nil {
			// Return the final response unchanged so the SDK poller can create
			// its normal ResponseError after the policy pipeline has completed.
			return resp, nil
		}
		if lastRetryableErr != nil {
			return nil, lastRetryableErr
		}
	}
	return resp, err
}

func retryReasonForResponse(resp *http.Response, err error) retryReason {
	if err == nil {
		switch resp.StatusCode {
		case http.StatusNotFound:
			var respErr *azcore.ResponseError
			if errors.As(runtime.NewResponseError(resp), &respErr) &&
				strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound") {
				return retryDeploymentNotFound
			}
		case http.StatusUnauthorized:
			return retryUnauthorized
		}
		return retryNone
	}

	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch {
		case respErr.StatusCode == http.StatusNotFound && strings.EqualFold(respErr.ErrorCode, "DeploymentNotFound"):
			return retryDeploymentNotFound
		case respErr.StatusCode == http.StatusUnauthorized:
			return retryUnauthorized
		}
	}
	return retryNone
}

func responseFromError(err error) *http.Response {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.RawResponse
	}
	return nil
}

func closeResponse(resp *http.Response, logger logr.Logger) {
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		logger.Error(err, "failed to close response body")
	}
}
