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

package framework

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type fakeTransport struct {
	do func(*http.Request) (*http.Response, error)
}

func (f *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	return f.do(req)
}

func newTestPipeline(pol policy.Policy, transport *fakeTransport) runtime.Pipeline {
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

func okResponse() (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       http.NoBody,
	}, nil
}

func TestParseResourceGroupFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantSubID  string
		wantRGName string
	}{
		{
			name:       "standard ARM path",
			path:       "/subscriptions/sub-id/resourceGroups/rg-name/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/my-cluster",
			wantSubID:  "sub-id",
			wantRGName: "rg-name",
		},
		{
			name:       "uppercase segments",
			path:       "/SUBSCRIPTIONS/sub-id/RESOURCEGROUPS/rg-name/providers/foo",
			wantSubID:  "sub-id",
			wantRGName: "rg-name",
		},
		{
			name:       "mixed case segments",
			path:       "/Subscriptions/sub-id/ResourceGroups/rg-name",
			wantSubID:  "sub-id",
			wantRGName: "rg-name",
		},
		{
			name:       "no resourceGroups segment",
			path:       "/subscriptions/sub-id/providers/Microsoft.RedHatOpenShift",
			wantSubID:  "sub-id",
			wantRGName: "",
		},
		{
			name:       "no subscriptions segment",
			path:       "/resourceGroups/rg-name/providers/foo",
			wantSubID:  "",
			wantRGName: "rg-name",
		},
		{
			name:       "empty path",
			path:       "",
			wantSubID:  "",
			wantRGName: "",
		},
		{
			name:       "root path",
			path:       "/",
			wantSubID:  "",
			wantRGName: "",
		},
		{
			name:       "subscriptions keyword without trailing value",
			path:       "/subscriptions",
			wantSubID:  "",
			wantRGName: "",
		},
		{
			name:       "subscriptions keyword with trailing slash only",
			path:       "/subscriptions/",
			wantSubID:  "",
			wantRGName: "",
		},
		{
			name:       "UUID subscription ID",
			path:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/my-rg",
			wantSubID:  "00000000-0000-0000-0000-000000000000",
			wantRGName: "my-rg",
		},
		{
			name:       "resourceGroups keyword without trailing value",
			path:       "/subscriptions/sub-id/resourceGroups",
			wantSubID:  "sub-id",
			wantRGName: "",
		},
		{
			name:       "resourceGroups keyword with trailing slash only",
			path:       "/subscriptions/sub-id/resourceGroups/",
			wantSubID:  "sub-id",
			wantRGName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			subID, rgName := parseResourceGroupFromPath(tt.path)
			assert.Equal(t, tt.wantSubID, subID)
			assert.Equal(t, tt.wantRGName, rgName)
		})
	}
}

func TestArmSystemDataPolicy(t *testing.T) {
	const frontendHost = "my-frontend.example.com:8443"
	t.Setenv("FRONTEND_ADDRESS", "https://"+frontendHost)

	pol := &armSystemDataPolicy{}

	t.Run("sets headers for frontend requests", func(t *testing.T) {
		var capturedHeaders http.Header
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				capturedHeaders = r.Header.Clone()
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://"+frontendHost+"/subscriptions/sub-id/resourceGroups/rg")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		systemData := capturedHeaders.Get(arm.HeaderNameARMResourceSystemData)
		assert.NotEmpty(t, systemData)
		assert.Contains(t, systemData, `"createdBy": "e2e-test"`)
		assert.Contains(t, systemData, `"createdByType": "Application"`)
		assert.Contains(t, systemData, `"createdAt":`)

		identityURL := capturedHeaders.Get(arm.HeaderNameIdentityURL)
		assert.Equal(t, "https://dummyhost.identity.azure.net", identityURL)
	})

	t.Run("does not set headers for non-frontend requests", func(t *testing.T) {
		var capturedHeaders http.Header
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				capturedHeaders = r.Header.Clone()
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://other-host.example.com/foo")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, capturedHeaders.Get(arm.HeaderNameARMResourceSystemData))
		assert.Empty(t, capturedHeaders.Get(arm.HeaderNameIdentityURL))
	})
}

func TestArmResourceGroupValidationPolicy(t *testing.T) {
	const frontendHost = "my-frontend.example.com:8443"
	t.Setenv("FRONTEND_ADDRESS", "https://"+frontendHost)

	t.Run("passes through for non-frontend host", func(t *testing.T) {
		pol := &armResourceGroupValidationPolicy{cred: nil}
		called := false
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				called = true
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://other.example.com/subscriptions/sub-id/resourceGroups/rg-name")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, called, "transport should have been called")
	})

	t.Run("passes through when path has no resource group", func(t *testing.T) {
		pol := &armResourceGroupValidationPolicy{cred: nil}
		called := false
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				called = true
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://"+frontendHost+"/providers/Microsoft.RedHatOpenShift")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, called, "transport should have been called")
	})

	t.Run("passes through when path has subscription but no resource group", func(t *testing.T) {
		pol := &armResourceGroupValidationPolicy{cred: nil}
		called := false
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				called = true
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://"+frontendHost+"/subscriptions/sub-id/providers/Microsoft.RedHatOpenShift")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, called, "transport should have been called")
	})
}

func TestCorrelationRequestIDPolicy(t *testing.T) {
	const frontendHost = "my-frontend.example.com:8443"
	t.Setenv("FRONTEND_ADDRESS", "https://"+frontendHost)

	pol := &correlationRequestIDPolicy{}

	t.Run("generates UUID when header is absent", func(t *testing.T) {
		var capturedHeaders http.Header
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				capturedHeaders = r.Header.Clone()
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://"+frontendHost+"/foo")
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		require.NoError(t, err)

		correlationID := capturedHeaders.Get(arm.HeaderNameCorrelationRequestID)
		assert.NotEmpty(t, correlationID)
		_, uuidErr := uuid.Parse(correlationID)
		assert.NoError(t, uuidErr, "correlation ID should be a valid UUID")
	})

	t.Run("preserves existing header", func(t *testing.T) {
		existingID := "existing-correlation-id"
		var capturedHeaders http.Header
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				capturedHeaders = r.Header.Clone()
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://"+frontendHost+"/foo")
		require.NoError(t, err)
		req.Raw().Header.Set(arm.HeaderNameCorrelationRequestID, existingID)

		_, err = pipeline.Do(req)
		require.NoError(t, err)

		assert.Equal(t, existingID, capturedHeaders.Get(arm.HeaderNameCorrelationRequestID))
	})

	t.Run("does not add header for non-frontend requests", func(t *testing.T) {
		var capturedHeaders http.Header
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				capturedHeaders = r.Header.Clone()
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://other.example.com/foo")
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		require.NoError(t, err)

		assert.Empty(t, capturedHeaders.Get(arm.HeaderNameCorrelationRequestID))
	})
}

func TestSanitizeAuthHeaderPolicy(t *testing.T) {
	t.Parallel()

	pol := &sanitizeAuthHeaderPolicy{}

	t.Run("redacts Authorization header on response", func(t *testing.T) {
		t.Parallel()
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{},
					Body:       http.NoBody,
					Request: &http.Request{
						Header: http.Header{
							"Authorization": []string{"Bearer super-secret-token"},
						},
					},
				}, nil
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://example.com/foo")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Request)
		assert.Equal(t, []string{"redacted"}, resp.Request.Header["Authorization"])
	})

	t.Run("handles nil response from transport error", func(t *testing.T) {
		t.Parallel()
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://example.com/foo")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("handles response with nil Request", func(t *testing.T) {
		t.Parallel()
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{},
					Body:       http.NoBody,
					Request:    nil,
				}, nil
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://example.com/foo")
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Nil(t, resp.Request)
	})
}

func TestLROPollerRetryDeploymentNotFoundPolicy(t *testing.T) {
	t.Parallel()

	lroPath := "/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy/operationStatuses/op-id"

	t.Run("passes through non-GET requests", func(t *testing.T) {
		t.Parallel()
		pol := NewLROPollerRetryDeploymentNotFoundPolicy()
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodPost, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("passes through non-matching paths", func(t *testing.T) {
		t.Parallel()
		pol := NewLROPollerRetryDeploymentNotFoundPolicy()
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)

		t.Run("no deployments segment", func(t *testing.T) {
			req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm")
			require.NoError(t, err)

			resp, err := pipeline.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})

		t.Run("no operationStatuses segment", func(t *testing.T) {
			req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com/subscriptions/sub-id/resourceGroups/rg/providers/Microsoft.Resources/deployments/my-deploy")
			require.NoError(t, err)

			resp, err := pipeline.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	})

	t.Run("returns success on first attempt", func(t *testing.T) {
		t.Parallel()
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			MaxRetries:     5,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			MaxRetryWindow: time.Second,
		}
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("retries DeploymentNotFound then succeeds", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			MaxRetries:     5,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			MaxRetryWindow: time.Second,
		}
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				callCount++
				if callCount <= 2 {
					return nil, &azcore.ResponseError{
						StatusCode: http.StatusNotFound,
						ErrorCode:  "DeploymentNotFound",
					}
				}
				return okResponse()
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		resp, err := pipeline.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 3, callCount)
	})

	t.Run("exhausts retries on persistent DeploymentNotFound", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			MaxRetries:     3,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			MaxRetryWindow: time.Second,
		}
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				callCount++
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "max retries or max retry window reached")
		assert.Equal(t, 4, callCount)
	})

	t.Run("does not retry non-DeploymentNotFound errors", func(t *testing.T) {
		t.Parallel()
		callCount := 0
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			MaxRetries:     5,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			MaxRetryWindow: time.Second,
		}
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				callCount++
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusInternalServerError,
					ErrorCode:  "InternalServerError",
				}
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(context.Background(), http.MethodGet, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		assert.Error(t, err)
		assert.Equal(t, 1, callCount)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			MaxRetries:     10,
			BaseBackoff:    time.Millisecond,
			MaxBackoff:     5 * time.Millisecond,
			MaxRetryWindow: 10 * time.Second,
		}
		transport := &fakeTransport{
			do: func(r *http.Request) (*http.Response, error) {
				cancel()
				return nil, &azcore.ResponseError{
					StatusCode: http.StatusNotFound,
					ErrorCode:  "DeploymentNotFound",
				}
			},
		}
		pipeline := newTestPipeline(pol, transport)
		req, err := runtime.NewRequest(ctx, http.MethodGet, "https://management.azure.com"+lroPath)
		require.NoError(t, err)

		_, err = pipeline.Do(req)
		assert.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("backoff respects bounds", func(t *testing.T) {
		t.Parallel()
		pol := &lroPollerRetryDeploymentNotFoundPolicy{
			BaseBackoff: 2 * time.Second,
			MaxBackoff:  10 * time.Second,
		}

		for attempt := 0; attempt < 10; attempt++ {
			d := pol.backoff(attempt)
			expectedSleep := min(pol.BaseBackoff<<uint(attempt), pol.MaxBackoff)
			assert.GreaterOrEqual(t, d, expectedSleep,
				"attempt %d: backoff should be at least the base sleep", attempt)
			assert.Less(t, d, expectedSleep+pol.BaseBackoff/2,
				"attempt %d: backoff should be less than base sleep + max jitter", attempt)
		}
	})
}
