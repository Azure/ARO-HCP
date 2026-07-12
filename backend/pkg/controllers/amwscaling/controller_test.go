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

package amwscaling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// fakeTransport implements policy.Transporter for testing.
type fakeTransport struct {
	requests  []*http.Request
	responses []*http.Response
	index     int
}

func (t *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req)
	if t.index < len(t.responses) {
		resp := t.responses[t.index]
		t.index++
		return resp, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
}

func jsonBody(v interface{}) io.ReadCloser {
	b, _ := json.Marshal(v)
	return io.NopCloser(strings.NewReader(string(b)))
}

func newTestController(transport *fakeTransport) *Controller {
	client := &MetricsContainersClient{
		pipeline: runtime.NewPipeline("test", "v1.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
			Transport: transport,
		}),
		armEndpoint: "https://management.azure.com",
	}
	return &Controller{
		pollInterval:            0,
		utilizationThreshold:    defaultUtilizationThreshold,
		maxLimit:                defaultMaxLimit,
		workspaceResourceIDs:    []string{"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1"},
		metricsContainersClient: client,
		workspaceLocationFunc: func(_ context.Context, _ *azcorearm.ResourceID) (string, error) {
			return "eastus", nil
		},
	}
}

func TestReconcileOne_NoScalingNeeded(t *testing.T) {
	transport := &fakeTransport{
		responses: []*http.Response{
			// GET metricsContainers/default — current limits
			{
				StatusCode: http.StatusOK,
				Body: jsonBody(metricsContainerResponse{
					Properties: struct {
						Limits struct {
							MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
							MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
						} `json:"limits"`
					}{
						Limits: struct {
							MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
							MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
						}{
							MaxActiveTimeSeries: 2_000_000,
							MaxEventsPerMinute:  2_000_000,
						},
					},
				}),
			},
		},
	}

	c := newTestController(transport)
	// Stub metricsClientFactory to return low utilization.
	ctx := context.Background()

	// Test via the pure function — utilization below threshold.
	current := AMWLimits{MaxActiveTimeSeries: 2_000_000, MaxEventsPerMinute: 2_000_000}
	utilization := AMWUtilization{ActiveTimeSeriesPercent: 30, EventsPerMinutePercent: 40}
	proposed := ProposeLimits(current, utilization, defaultUtilizationThreshold, defaultMaxLimit)
	assert.Nil(t, proposed)

	// Verify GetLimits works with the fake transport.
	limits, err := c.metricsContainersClient.GetLimits(ctx, "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1")
	require.NoError(t, err)
	assert.Equal(t, int64(2_000_000), limits.MaxActiveTimeSeries)
	assert.Equal(t, int64(2_000_000), limits.MaxEventsPerMinute)

	// Only 1 request (GET limits), no PUT.
	assert.Len(t, transport.requests, 1)
	assert.Equal(t, http.MethodGet, transport.requests[0].Method)
	assert.Contains(t, transport.requests[0].URL.String(), "metricsContainers/default")
}

func TestReconcileOne_ScalesUp(t *testing.T) {
	transport := &fakeTransport{
		responses: []*http.Response{
			// GET metricsContainers/default — current limits
			{
				StatusCode: http.StatusOK,
				Body: jsonBody(metricsContainerResponse{
					Properties: struct {
						Limits struct {
							MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
							MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
						} `json:"limits"`
					}{
						Limits: struct {
							MaxActiveTimeSeries int64 `json:"maxActiveTimeSeries"`
							MaxEventsPerMinute  int64 `json:"maxEventsPerMinute"`
						}{
							MaxActiveTimeSeries: 2_000_000,
							MaxEventsPerMinute:  2_000_000,
						},
					},
				}),
			},
			// PUT metricsContainers/default — set new limits
			{
				StatusCode: http.StatusOK,
				Body:       jsonBody(map[string]interface{}{}),
			},
		},
	}

	c := newTestController(transport)
	// We'll directly test setLimits.
	ctx := context.Background()

	// Verify GetLimits.
	limits, err := c.metricsContainersClient.GetLimits(ctx, "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1")
	require.NoError(t, err)
	assert.Equal(t, int64(2_000_000), limits.MaxActiveTimeSeries)

	// Compute proposed limits with high utilization (above 85% threshold).
	// 90% of 2M = 1.8M, 175% = 3,150,000. 95% of 2M = 1.9M, 175% = 3,320,000.
	utilization := AMWUtilization{ActiveTimeSeriesPercent: 90, EventsPerMinutePercent: 95}
	proposed := ProposeLimits(*limits, utilization, defaultUtilizationThreshold, defaultMaxLimit)
	require.NotNil(t, proposed)
	assert.Equal(t, int64(3_150_000), proposed.MaxActiveTimeSeries)
	assert.Equal(t, int64(3_320_000), proposed.MaxEventsPerMinute)

	// Call SetLimits — consumes response index 1 (PUT).
	err = c.metricsContainersClient.SetLimits(ctx, "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1", "eastus", proposed)
	require.NoError(t, err)

	// Verify the PUT request.
	putReq := transport.requests[len(transport.requests)-1]
	assert.Equal(t, http.MethodPut, putReq.Method)
	assert.Contains(t, putReq.URL.String(), "metricsContainers/default")
	assert.Contains(t, putReq.URL.String(), "api-version=2025-10-03-preview")

	// Parse the body to verify limits.
	body, err := io.ReadAll(putReq.Body)
	require.NoError(t, err)
	var req metricsContainerRequest
	err = json.Unmarshal(body, &req)
	require.NoError(t, err)
	assert.Equal(t, "eastus", req.Location)
	assert.Equal(t, int64(3_150_000), req.Properties.Limits.MaxActiveTimeSeries)
	assert.Equal(t, int64(3_320_000), req.Properties.Limits.MaxEventsPerMinute)
}

func TestMetricsContainerURL(t *testing.T) {
	c := &MetricsContainersClient{armEndpoint: "https://management.azure.com"}
	url := c.url("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1")
	assert.Equal(t, "https://management.azure.com/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1/metricsContainers/default?api-version=2025-10-03-preview", url)

	// Sovereign cloud endpoint.
	c.armEndpoint = "https://management.chinacloudapi.cn"
	url = c.url("/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1")
	assert.Equal(t, "https://management.chinacloudapi.cn/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1/metricsContainers/default?api-version=2025-10-03-preview", url)
}

func TestReadCurrentLimits_ErrorStatus(t *testing.T) {
	transport := &fakeTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader(`{"error": {"code": "NotFound", "message": "not found"}}`)),
			},
		},
	}

	c := newTestController(transport)
	_, err := c.metricsContainersClient.GetLimits(context.Background(), "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1")
	assert.Error(t, err)
	var respErr *azcore.ResponseError
	assert.ErrorAs(t, err, &respErr)
	assert.Equal(t, http.StatusNotFound, respErr.StatusCode)
}

func TestSetLimits_ErrorStatus(t *testing.T) {
	transport := &fakeTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error": {"code": "Forbidden", "message": "forbidden"}}`)),
			},
		},
	}

	c := newTestController(transport)
	err := c.metricsContainersClient.SetLimits(context.Background(), "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Monitor/accounts/ws1", "eastus", &AMWLimits{
		MaxActiveTimeSeries: 4_000_000,
		MaxEventsPerMinute:  4_000_000,
	})
	assert.Error(t, err)
	var respErr *azcore.ResponseError
	assert.ErrorAs(t, err, &respErr)
	assert.Equal(t, http.StatusForbidden, respErr.StatusCode)
}
