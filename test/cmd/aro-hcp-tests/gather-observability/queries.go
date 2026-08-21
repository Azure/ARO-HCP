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
	"fmt"

	_ "embed"

	"sigs.k8s.io/yaml"
)

const (
	chartTypeLine               = "line"
	chartTypeFacetedStackedArea = "faceted-stacked-area"

	queryTypePromQL      = "promql"
	queryTypeAzureMetric = "azureMetric"
)

// QueriesConfig holds panels of grouped queries to run against Azure Monitor
// workspaces and platform metrics. Each panel produces one HTML page with
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

// QuerySpec describes a single query to execute and chart. The Type field
// discriminates between PromQL queries (default) and Azure Monitor platform
// metric queries.
type QuerySpec struct {
	Title            string            `json:"title" yaml:"title"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	Type             string            `json:"type,omitempty" yaml:"type,omitempty"`
	Unit             string            `json:"unit,omitempty" yaml:"unit,omitempty"`
	MinPeakThreshold float64           `json:"minPeakThreshold,omitempty" yaml:"minPeakThreshold,omitempty"`
	ChartType        string            `json:"chartType,omitempty" yaml:"chartType,omitempty"`
	FacetBy          string            `json:"facetBy,omitempty" yaml:"facetBy,omitempty"`
	StackBy          string            `json:"stackBy,omitempty" yaml:"stackBy,omitempty"`
	Colors           map[string]string `json:"colors,omitempty" yaml:"colors,omitempty"`

	// PromQL fields
	Query     string `json:"query,omitempty" yaml:"query,omitempty"`
	Workspace string `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Step      string `json:"step,omitempty" yaml:"step,omitempty"`

	// Azure Monitor metric fields
	Resource    string `json:"resource,omitempty" yaml:"resource,omitempty"`
	MetricName  string `json:"metricName,omitempty" yaml:"metricName,omitempty"`
	Aggregation string `json:"aggregation,omitempty" yaml:"aggregation,omitempty"`
	Interval    string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Filter      string `json:"filter,omitempty" yaml:"filter,omitempty"`
}

// queryDisplay returns a human-readable string for the query, used in chart
// metadata. For PromQL it's the query string; for Azure metrics it describes
// the metric and aggregation.
func (q QuerySpec) queryDisplay() string {
	if q.Type == queryTypeAzureMetric {
		display := fmt.Sprintf("%s(%s)", q.Aggregation, q.MetricName)
		if len(q.Filter) > 0 {
			display += fmt.Sprintf(" | %s", q.Filter)
		}
		return display
	}
	return q.Query
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

			if q.Type == "" {
				cfg.Panels[pi].Queries[qi].Type = queryTypePromQL
			}
			qType := cfg.Panels[pi].Queries[qi].Type

			switch qType {
			case queryTypePromQL:
				if q.Query == "" {
					return nil, fmt.Errorf("panel %d (%s), query %d (%s): query is required", pi, p.Title, qi, q.Title)
				}
				if q.Workspace != workspaceSvc && q.Workspace != workspaceHcp {
					return nil, fmt.Errorf("panel %d (%s), query %d (%s): workspace must be \"svc\" or \"hcp\", got %q", pi, p.Title, qi, q.Title, q.Workspace)
				}
				if q.Step == "" {
					cfg.Panels[pi].Queries[qi].Step = "60s"
				}
			case queryTypeAzureMetric:
				if q.Resource == "" {
					return nil, fmt.Errorf("panel %d (%s), query %d (%s): resource is required for azureMetric queries", pi, p.Title, qi, q.Title)
				}
				if q.MetricName == "" {
					return nil, fmt.Errorf("panel %d (%s), query %d (%s): metricName is required for azureMetric queries", pi, p.Title, qi, q.Title)
				}
				if q.Aggregation == "" {
					return nil, fmt.Errorf("panel %d (%s), query %d (%s): aggregation is required for azureMetric queries", pi, p.Title, qi, q.Title)
				}
				if q.Interval == "" {
					cfg.Panels[pi].Queries[qi].Interval = "PT1M"
				}
			default:
				return nil, fmt.Errorf("panel %d (%s), query %d (%s): type must be %q or %q, got %q", pi, p.Title, qi, q.Title, queryTypePromQL, queryTypeAzureMetric, qType)
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
