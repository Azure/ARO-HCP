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

package prometheus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// Response is the top-level Prometheus HTTP API response.
type Response struct {
	Status    string `json:"status"`
	Data      Data   `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Data holds the result set from a query_range call.
type Data struct {
	ResultType string   `json:"resultType"`
	Result     []Result `json:"result"`
}

// Result is a single timeseries returned by query_range.
type Result struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"` // each element is [unix_timestamp_float, "string_value"]
}

// LookupPrometheusEndpoint retrieves the Prometheus query endpoint for an
// Azure Monitor workspace using the ARM SDK.
func LookupPrometheusEndpoint(ctx context.Context, cred azcore.TokenCredential, subscriptionID, resourceGroup, workspaceName string) (string, error) {
	client, err := armmonitor.NewAzureMonitorWorkspacesClient(subscriptionID, cred, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create monitor workspaces client: %w", err)
	}
	resp, err := client.Get(ctx, resourceGroup, workspaceName, nil)
	if err != nil {
		return "", fmt.Errorf("failed to get workspace %s: %w", workspaceName, err)
	}
	if resp.Properties == nil || resp.Properties.Metrics == nil || resp.Properties.Metrics.PrometheusQueryEndpoint == nil {
		return "", fmt.Errorf("workspace %s has no Prometheus query endpoint", workspaceName)
	}
	return *resp.Properties.Metrics.PrometheusQueryEndpoint, nil
}

// QueryRange executes a Prometheus query_range request against an Azure Monitor
// Prometheus endpoint using bearer token authentication. The caller should pass
// a shared *http.Client to amortize connection setup across multiple queries.
func QueryRange(ctx context.Context, httpClient *http.Client, cred azcore.TokenCredential, endpoint, query string, start, end time.Time, step string) (*Response, error) {
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://prometheus.monitor.azure.com/.default"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get Prometheus token: %w", err)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint URL %q: %w", endpoint, err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/query_range"

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", strconv.FormatInt(start.Unix(), 10))
	params.Set("end", strconv.FormatInt(end.Unix(), 10))
	params.Set("step", step)
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus query_range request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus query_range returned %d: %s", resp.StatusCode, string(body))
	}

	var promResp Response
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&promResp); err != nil {
		return nil, fmt.Errorf("failed to parse Prometheus response: %w", err)
	}
	if promResp.Status != "success" {
		return nil, fmt.Errorf("prometheus query error (%s): %s", promResp.ErrorType, promResp.Error)
	}
	return &promResp, nil
}
