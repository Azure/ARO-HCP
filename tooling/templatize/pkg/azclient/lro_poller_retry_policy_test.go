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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const deploymentOperationStatusPath = "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy/operationStatuses/op-id"

type fakeTransport struct {
	interceptor func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	return f.interceptor(req)
}

func newRetryPolicyTestPipeline(pol policy.Policy, transport policy.Transporter) runtime.Pipeline {
	return runtime.NewPipeline("test", "v0.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{pol},
		},
		&policy.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	)
}

func successfulResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
	}, nil
}

func deploymentNotFoundResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":"DeploymentNotFound","message":"Deployment could not be found."}}`,
		)),
	}, nil
}

func unauthorizedResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":"AuthenticationFailed","message":"Authentication failed."}}`,
		)),
	}, nil
}

func TestLROPollerRetryPolicy(t *testing.T) {
	t.Parallel()

	t.Run("passes through non-matching requests", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			method string
			path   string
		}{
			{
				name:   "non-GET request",
				method: http.MethodPost,
				path:   deploymentOperationStatusPath,
			},
			{
				name:   "deployment resource GET",
				method: http.MethodGet,
				path:   "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy",
			},
			{
				name:   "unrelated resource GET",
				method: http.MethodGet,
				path:   "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				callCount := 0
				transport := &fakeTransport{
					interceptor: func(_ *http.Request) (*http.Response, error) {
						callCount++
						return successfulResponse()
					},
				}
				pipeline := newRetryPolicyTestPipeline(NewLROPollerRetryPolicy(nil), transport)
				req, err := runtime.NewRequest(t.Context(), tt.method, "https://management.azure.com"+tt.path)
				require.NoError(t, err)

				resp, err := pipeline.Do(req)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				assert.Equal(t, 1, callCount)
			})
		}
	})

	t.Run("retries DeploymentNotFound then succeeds", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		pol := &lroPollerRetryPolicy{
			backoff: wait.Backoff{
				Duration: time.Millisecond,
				Steps:    3,
			},
		}
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				callCount++
				if callCount <= 2 {
					return deploymentNotFoundResponse()
				}
				return successfulResponse()
			},
		}
		pipeline := newRetryPolicyTestPipeline(pol, transport)
		req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 3, callCount)
	})

	t.Run("exhausts retries on persistent DeploymentNotFound", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		pol := &lroPollerRetryPolicy{
			backoff: wait.Backoff{
				Duration: time.Millisecond,
				Steps:    3,
			},
		}
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				callCount++
				return deploymentNotFoundResponse()
			},
		}
		pipeline := newRetryPolicyTestPipeline(pol, transport)
		req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		var responseErr *azcore.ResponseError
		require.ErrorAs(t, runtime.NewResponseError(resp), &responseErr)
		assert.Equal(t, "DeploymentNotFound", responseErr.ErrorCode)
		assert.Equal(t, 3, callCount)
	})

	t.Run("does not retry other 404 errors", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		pol := &lroPollerRetryPolicy{
			backoff: wait.Backoff{
				Duration: time.Millisecond,
				Steps:    3,
			},
		}
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				callCount++
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(
						`{"error":{"code":"ResourceGroupNotFound","message":"Resource group could not be found."}}`,
					)),
				}, nil
			},
		}
		pipeline := newRetryPolicyTestPipeline(pol, transport)
		req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, 1, callCount)
	})

	t.Run("does not retry other errors", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		pol := &lroPollerRetryPolicy{
			backoff: wait.Backoff{
				Duration: time.Millisecond,
				Steps:    3,
			},
		}
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				callCount++
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Header:     http.Header{},
					Body:       http.NoBody,
				}, nil
			},
		}
		pipeline := newRetryPolicyTestPipeline(pol, transport)
		req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, 1, callCount)
	})

	t.Run("retries one Unauthorized response", func(t *testing.T) {
		t.Parallel()

		t.Run("then succeeds", func(t *testing.T) {
			callCount := 0
			transport := &fakeTransport{
				interceptor: func(_ *http.Request) (*http.Response, error) {
					callCount++
					if callCount == 1 {
						return unauthorizedResponse()
					}
					return successfulResponse()
				},
			}
			pipeline := newRetryPolicyTestPipeline(&lroPollerRetryPolicy{
				backoff: wait.Backoff{Duration: time.Millisecond, Steps: 6},
			}, transport)
			req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
			require.NoError(t, err)

			resp, err := pipeline.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, 2, callCount)
		})

		t.Run("then returns persistent Unauthorized", func(t *testing.T) {
			callCount := 0
			transport := &fakeTransport{
				interceptor: func(_ *http.Request) (*http.Response, error) {
					callCount++
					return unauthorizedResponse()
				},
			}
			pipeline := newRetryPolicyTestPipeline(&lroPollerRetryPolicy{
				backoff: wait.Backoff{Duration: time.Millisecond, Steps: 6},
			}, transport)
			req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
			require.NoError(t, err)

			resp, err := pipeline.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Equal(t, 2, callCount)
		})
	})

	t.Run("shares the retry budget across transient responses", func(t *testing.T) {
		t.Parallel()

		callCount := 0
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				callCount++
				switch callCount {
				case 1:
					return unauthorizedResponse()
				case 2, 3:
					return deploymentNotFoundResponse()
				default:
					return successfulResponse()
				}
			},
		}
		pipeline := newRetryPolicyTestPipeline(&lroPollerRetryPolicy{
			backoff: wait.Backoff{Duration: time.Millisecond, Steps: 4},
		}, transport)
		req, err := runtime.NewRequest(t.Context(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 4, callCount)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		pol := &lroPollerRetryPolicy{
			backoff: wait.Backoff{
				Duration: time.Millisecond,
				Steps:    3,
			},
		}
		transport := &fakeTransport{
			interceptor: func(_ *http.Request) (*http.Response, error) {
				cancel()
				return deploymentNotFoundResponse()
			},
		}
		pipeline := newRetryPolicyTestPipeline(pol, transport)
		req, err := runtime.NewRequest(ctx, http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
