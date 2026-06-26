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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type retryPolicyTestTransport struct {
	do func(*http.Request) (*http.Response, error)
}

func (f *retryPolicyTestTransport) Do(req *http.Request) (*http.Response, error) {
	return f.do(req)
}

func newRetryPolicyTestPipeline(pol policy.Policy, transport *retryPolicyTestTransport) runtime.Pipeline {
	return runtime.NewPipeline("test", "v0.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{pol},
		},
		&policy.ClientOptions{
			Transport: transport,
			// MaxRetries: -1 disables the SDK's built-in retry (0 is "use default 3")
			Retry: policy.RetryOptions{MaxRetries: -1},
		},
	)
}

func retryPolicyOkResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
	}, nil
}

// newTestRetryPolicy creates a policy with fast backoff for testing.
func newTestRetryPolicy() *deploymentNotFoundRetryPolicy {
	return &deploymentNotFoundRetryPolicy{
		maxRetries:  5,
		baseBackoff: time.Millisecond,
		maxBackoff:  5 * time.Millisecond,
	}
}

const deploymentOperationStatusPath = "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy/operationStatuses/op-id"
const deploymentPath = "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy"
const deploymentOperationsPath = "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy/operations"
const subscriptionScopeDeploymentPath = "/subscriptions/sub-id/providers/Microsoft.Resources/deployments/my-deploy"

func TestDeploymentNotFoundRetryPolicy_NonGETPassthrough(t *testing.T) {
	t.Parallel()
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			req, err := runtime.NewRequest(context.Background(), method, "https://management.azure.com"+deploymentOperationStatusPath)
			require.NoError(t, err)

			resp, err := pipeline.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestDeploymentNotFoundRetryPolicy_Non404Passthrough(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			return nil, &azcore.ResponseError{
				StatusCode: http.StatusInternalServerError,
				ErrorCode:  "InternalServerError",
			}
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	assert.Error(t, err)
	assert.Equal(t, 1, callCount, "should not retry non-404 errors")
}

func TestDeploymentNotFoundRetryPolicy_404NonDeploymentNotFoundPassthrough(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			return nil, &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "ResourceNotFound",
			}
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	assert.Error(t, err)
	assert.Equal(t, 1, callCount, "should not retry 404 with non-DeploymentNotFound error code")
}

func TestDeploymentNotFoundRetryPolicy_RetriesDeploymentNotFound(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 2 {
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			}
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 3, callCount, "should have retried twice then succeeded")
}

func TestDeploymentNotFoundRetryPolicy_RetriesOnDeploymentPath(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 1 {
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			}
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, callCount, "should retry on /deployments/ path")
}

func TestDeploymentNotFoundRetryPolicy_RetriesOnOperationStatusesPath(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 1 {
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			}
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, callCount, "should retry on /deployments/.../operationStatuses/ path")
}

func TestDeploymentNotFoundRetryPolicy_RetriesOnOperationsPath(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 1 {
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			}
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationsPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, callCount, "should retry on /deployments/.../operations path")
}

func TestDeploymentNotFoundRetryPolicy_RetriesOnSubscriptionScopeDeployment(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			if callCount <= 1 {
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			}
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+subscriptionScopeDeploymentPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, callCount, "should retry on subscription-scope deployment path")
}

func TestDeploymentNotFoundRetryPolicy_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	pol := &deploymentNotFoundRetryPolicy{
		maxRetries:  10,
		baseBackoff: time.Millisecond,
		maxBackoff:  5 * time.Millisecond,
	}
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			cancel()
			return nil, &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "DeploymentNotFound",
			}
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(ctx, http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDeploymentNotFoundRetryPolicy_MaxRetriesExhausted(t *testing.T) {
	t.Parallel()
	callCount := 0
	pol := &deploymentNotFoundRetryPolicy{
		maxRetries:  3,
		baseBackoff: time.Millisecond,
		maxBackoff:  5 * time.Millisecond,
	}
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			callCount++
			return nil, &azcore.ResponseError{
				StatusCode: http.StatusNotFound,
				ErrorCode:  "DeploymentNotFound",
			}
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deployment not found after 3 retries")
	assert.Equal(t, 4, callCount, "should have tried 1 initial + 3 retries = 4 total")
}

func TestDeploymentNotFoundRetryPolicy_NonMatchingPath(t *testing.T) {
	t.Parallel()
	pol := newTestRetryPolicy()

	tests := []struct {
		name string
		path string
	}{
		{
			name: "compute VM path",
			path: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm",
		},
		{
			name: "storage account path",
			path: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/sa",
		},
		{
			name: "root subscriptions path",
			path: "/subscriptions/sub-id",
		},
		{
			name: "bare deployments path without provider prefix",
			path: "/subscriptions/sub-id/resourceGroups/rg/deployments/my-deploy",
		},
		{
			name: "different provider deployments path",
			path: "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Compute/deployments/my-deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			callCount := 0
			transport := &retryPolicyTestTransport{
				do: func(r *http.Request) (*http.Response, error) {
					callCount++
					return nil, &azcore.ResponseError{
						StatusCode: http.StatusNotFound,
						ErrorCode:  "DeploymentNotFound",
					}
				},
			}
			pipeline := newRetryPolicyTestPipeline(pol, transport)
			req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+tt.path)
			require.NoError(t, err)

			_, err = pipeline.Do(req)
			assert.Error(t, err)
			assert.Equal(t, 1, callCount, "should not retry on non-matching paths")
		})
	}
}

func TestDeploymentNotFoundRetryPolicy_SuccessOnFirstAttempt(t *testing.T) {
	t.Parallel()
	pol := newTestRetryPolicy()
	transport := &retryPolicyTestTransport{
		do: func(r *http.Request) (*http.Response, error) {
			return retryPolicyOkResponse()
		},
	}
	pipeline := newRetryPolicyTestPipeline(pol, transport)
	req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+deploymentOperationStatusPath)
	require.NoError(t, err)

	resp, err := pipeline.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDeploymentNotFoundRetryPolicy_BackoffBounds(t *testing.T) {
	t.Parallel()
	pol := &deploymentNotFoundRetryPolicy{
		baseBackoff: 2 * time.Second,
		maxBackoff:  10 * time.Second,
	}

	for attempt := 0; attempt < 10; attempt++ {
		d := pol.backoff(attempt)
		expectedSleep := min(pol.baseBackoff<<uint(attempt), pol.maxBackoff)
		assert.GreaterOrEqual(t, d, expectedSleep,
			"attempt %d: backoff should be at least the base sleep", attempt)
		assert.Less(t, d, expectedSleep+pol.baseBackoff/2,
			"attempt %d: backoff should be less than base sleep + max jitter", attempt)
	}
}

func TestNewDeploymentNotFoundRetryPolicy_Defaults(t *testing.T) {
	t.Parallel()
	pol := newDeploymentNotFoundRetryPolicy()
	assert.Equal(t, 5, pol.maxRetries)
	assert.Equal(t, 1*time.Second, pol.baseBackoff)
	assert.Equal(t, 10*time.Second, pol.maxBackoff)
}
