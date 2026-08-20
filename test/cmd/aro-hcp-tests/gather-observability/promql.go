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

package gatherobservability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "embed"

	"sigs.k8s.io/yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// QueriesConfig holds panels of grouped PromQL queries to run against Azure
// Monitor Prometheus workspaces. Each panel produces one HTML page with
// multiple charts.
type QueriesConfig struct {
	Panels []PanelSpec `json:"panels" yaml:"panels"`
}

// PanelSpec groups related queries that should be rendered together on a
// single HTML page.
type PanelSpec struct {
	Title   string      `json:"title" yaml:"title"`
	Queries []QuerySpec `json:"queries" yaml:"queries"`
}

const (
	chartTypeLine               = "line"
	chartTypeFacetedStackedArea = "faceted-stacked-area"
)

const (
	// sourcePrometheus queries an Azure Monitor managed Prometheus workspace
	// (the "svc"/"hcp" workspaces) with a PromQL expression. This is the
	// default when no source is specified.
	sourcePrometheus = "prometheus"
	// sourceAzureMonitor queries the Azure Monitor platform metrics API
	// (Microsoft.Insights/metrics) for a named Azure resource. Used for
	// infrastructure metrics that are not scraped into Prometheus, e.g.
	// CosmosDB standard metrics.
	sourceAzureMonitor = "azureMonitor"
)

// resourceCosmosDB selects the RP Cosmos DB account as the target of an
// azureMonitor query. Additional resources can be added here as new
// infrastructure metric panels are introduced.
const resourceCosmosDB = "cosmosdb"

// knownMetricResources is the set of resource selectors accepted in the
// azureMonitor query "resource" field. The values are resolved to concrete
// Azure resource IDs at runtime (see options.go).
var knownMetricResources = map[string]bool{
	resourceCosmosDB: true,
}

// MetricSpec names a single Azure Monitor platform metric to plot within an
// azureMonitor query. Multiple metrics are rendered as separate series on the
// same chart.
type MetricSpec struct {
	// Name is the metric name as referred to in the Azure Monitor REST API
	// (e.g. "NormalizedRUConsumption", "AutoscaledRU").
	Name string `json:"name" yaml:"name"`
	// Label is the human-readable series label shown on the chart. Defaults
	// to Name when empty. When SplitBy is set, the split dimension value is
	// used as the series label instead (or as a suffix when the query plots
	// more than one metric).
	Label string `json:"label,omitempty" yaml:"label,omitempty"`
	// SplitBy is an Azure Monitor dimension (e.g. "CollectionName") to split
	// the metric into one series per dimension value. When empty the metric
	// is returned as a single series aggregated across all dimensions.
	SplitBy string `json:"splitBy,omitempty" yaml:"splitBy,omitempty"`
	// Filter restricts the metric to matching dimension values (e.g.
	// {"StatusCode": "429"}). Combined with SplitBy via logical AND.
	Filter map[string]string `json:"filter,omitempty" yaml:"filter,omitempty"`
}

// QuerySpec describes a single query to execute and chart. Depending on
// Source, it is either a PromQL query against a Prometheus workspace or an
// Azure Monitor platform-metrics query against a named Azure resource.
type QuerySpec struct {
	Title       string `json:"title" yaml:"title"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Source selects the query backend: "prometheus" (default) or
	// "azureMonitor".
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// Prometheus-source fields.
	Query     string `json:"query,omitempty" yaml:"query,omitempty"`
	Workspace string `json:"workspace,omitempty" yaml:"workspace,omitempty"` // "svc" or "hcp"

	// azureMonitor-source fields.
	Resource    string       `json:"resource,omitempty" yaml:"resource,omitempty"`
	Aggregation string       `json:"aggregation,omitempty" yaml:"aggregation,omitempty"`
	Metrics     []MetricSpec `json:"metrics,omitempty" yaml:"metrics,omitempty"`

	Unit             string            `json:"unit,omitempty" yaml:"unit,omitempty"`
	Step             string            `json:"step,omitempty" yaml:"step,omitempty"`
	MinPeakThreshold float64           `json:"minPeakThreshold,omitempty" yaml:"minPeakThreshold,omitempty"`
	ChartType        string            `json:"chartType,omitempty" yaml:"chartType,omitempty"`
	FacetBy          string            `json:"facetBy,omitempty" yaml:"facetBy,omitempty"`
	StackBy          string            `json:"stackBy,omitempty" yaml:"stackBy,omitempty"`
	Colors           map[string]string `json:"colors,omitempty" yaml:"colors,omitempty"`
}

// PrometheusResponse is the top-level Prometheus HTTP API response.
type PrometheusResponse struct {
	Status    string         `json:"status"`
	Data      PrometheusData `json:"data"`
	ErrorType string         `json:"errorType,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// PrometheusData holds the result set from a query_range call.
type PrometheusData struct {
	ResultType string             `json:"resultType"`
	Result     []PrometheusResult `json:"result"`
}

// PrometheusResult is a single timeseries returned by query_range.
type PrometheusResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]any           `json:"values"` // each element is [unix_timestamp_float, "string_value"]
}

//go:embed queries.yaml
var defaultQueriesYAML []byte

func loadQueriesConfig() (*QueriesConfig, error) {
	return parseQueriesConfig(defaultQueriesYAML)
}

func parseQueriesConfig(data []byte) (*QueriesConfig, error) {
	var cfg QueriesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse queries config: %w", err)
	}
	for pi, p := range cfg.Panels {
		if p.Title == "" {
			return nil, fmt.Errorf("panel %d: title is required", pi)
		}
		if len(p.Queries) == 0 {
			return nil, fmt.Errorf("panel %d (%s): at least one query is required", pi, p.Title)
		}
		for qi, q := range p.Queries {
			if q.Title == "" {
				return nil, fmt.Errorf("panel %d (%s), query %d: title is required", pi, p.Title, qi)
			}

			if q.Source == "" {
				cfg.Panels[pi].Queries[qi].Source = sourcePrometheus
			}
			switch cfg.Panels[pi].Queries[qi].Source {
			case sourcePrometheus:
				if err := validatePrometheusQuery(pi, qi, p.Title, q); err != nil {
					return nil, err
				}
			case sourceAzureMonitor:
				if err := validateAzureMonitorQuery(pi, qi, p.Title, q); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("panel %d (%s), query %d (%s): source must be %q or %q, got %q", pi, p.Title, qi, q.Title, sourcePrometheus, sourceAzureMonitor, cfg.Panels[pi].Queries[qi].Source)
			}

			if q.Step == "" {
				cfg.Panels[pi].Queries[qi].Step = "60s"
			}
			if q.ChartType == "" {
				cfg.Panels[pi].Queries[qi].ChartType = chartTypeLine
			}
			if cfg.Panels[pi].Queries[qi].ChartType != chartTypeLine && cfg.Panels[pi].Queries[qi].ChartType != chartTypeFacetedStackedArea {
				return nil, fmt.Errorf("panel %d (%s), query %d (%s): chartType must be %q or %q, got %q", pi, p.Title, qi, q.Title, chartTypeLine, chartTypeFacetedStackedArea, cfg.Panels[pi].Queries[qi].ChartType)
			}
			if cfg.Panels[pi].Queries[qi].ChartType == chartTypeFacetedStackedArea && q.FacetBy == "" {
				return nil, fmt.Errorf("panel %d (%s), query %d (%s): facetBy is required when chartType is %q", pi, p.Title, qi, q.Title, chartTypeFacetedStackedArea)
			}
			if cfg.Panels[pi].Queries[qi].ChartType != chartTypeFacetedStackedArea && q.FacetBy != "" {
				return nil, fmt.Errorf("panel %d (%s), query %d (%s): facetBy is only valid with chartType %q", pi, p.Title, qi, q.Title, chartTypeFacetedStackedArea)
			}
		}
	}
	return &cfg, nil
}

// validatePrometheusQuery validates the fields required for a PromQL query and
// rejects fields that only apply to the azureMonitor source.
func validatePrometheusQuery(pi, qi int, panelTitle string, q QuerySpec) error {
	if q.Query == "" {
		return fmt.Errorf("panel %d (%s), query %d (%s): query is required", pi, panelTitle, qi, q.Title)
	}
	if q.Workspace != workspaceSvc && q.Workspace != workspaceHcp {
		return fmt.Errorf("panel %d (%s), query %d (%s): workspace must be \"svc\" or \"hcp\", got %q", pi, panelTitle, qi, q.Title, q.Workspace)
	}
	if q.Resource != "" || q.Aggregation != "" || len(q.Metrics) > 0 {
		return fmt.Errorf("panel %d (%s), query %d (%s): resource/aggregation/metrics are only valid with source %q", pi, panelTitle, qi, q.Title, sourceAzureMonitor)
	}
	return nil
}

// validateAzureMonitorQuery validates the fields required for an Azure Monitor
// platform-metrics query and rejects fields that only apply to the prometheus
// source.
func validateAzureMonitorQuery(pi, qi int, panelTitle string, q QuerySpec) error {
	if q.Query != "" || q.Workspace != "" {
		return fmt.Errorf("panel %d (%s), query %d (%s): query/workspace are only valid with source %q", pi, panelTitle, qi, q.Title, sourcePrometheus)
	}
	if !knownMetricResources[q.Resource] {
		return fmt.Errorf("panel %d (%s), query %d (%s): resource must be one of %v, got %q", pi, panelTitle, qi, q.Title, sortedMetricResources(), q.Resource)
	}
	if q.Aggregation == "" {
		return fmt.Errorf("panel %d (%s), query %d (%s): aggregation is required for source %q", pi, panelTitle, qi, q.Title, sourceAzureMonitor)
	}
	if _, err := metricValueSelector(q.Aggregation); err != nil {
		return fmt.Errorf("panel %d (%s), query %d (%s): %w", pi, panelTitle, qi, q.Title, err)
	}
	if len(q.Metrics) == 0 {
		return fmt.Errorf("panel %d (%s), query %d (%s): at least one metric is required for source %q", pi, panelTitle, qi, q.Title, sourceAzureMonitor)
	}
	for mi, m := range q.Metrics {
		if m.Name == "" {
			return fmt.Errorf("panel %d (%s), query %d (%s), metric %d: name is required", pi, panelTitle, qi, q.Title, mi)
		}
	}
	if q.FacetBy != "" {
		return fmt.Errorf("panel %d (%s), query %d (%s): facetBy is not supported with source %q", pi, panelTitle, qi, q.Title, sourceAzureMonitor)
	}
	return nil
}

func sortedMetricResources() []string {
	out := make([]string, 0, len(knownMetricResources))
	for k := range knownMetricResources {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// lookupPrometheusEndpoint retrieves the Prometheus query endpoint for an
// Azure Monitor workspace using the ARM SDK.
func lookupPrometheusEndpoint(ctx context.Context, cred azcore.TokenCredential, subscriptionID, resourceGroup, workspaceName string) (string, error) {
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

// queryRange executes a Prometheus query_range request against an Azure Monitor
// Prometheus endpoint using bearer token authentication. The caller should pass
// a shared *http.Client to amortize connection setup across multiple queries.
func queryRange(ctx context.Context, httpClient *http.Client, cred azcore.TokenCredential, endpoint, query string, start, end time.Time, step string) (*PrometheusResponse, error) {
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

	var promResp PrometheusResponse
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
