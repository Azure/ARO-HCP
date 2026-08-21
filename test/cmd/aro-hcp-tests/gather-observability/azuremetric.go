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
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/opts"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// fetchAzureMetrics queries Azure Monitor platform metrics for a resource.
func fetchAzureMetrics(ctx context.Context, cred azcore.TokenCredential, subscriptionID, resourceURI string, query QuerySpec, start, end time.Time) (*armmonitor.Response, error) {
	client, err := armmonitor.NewMetricsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	timespan := fmt.Sprintf("%s/%s", start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	listOptions := &armmonitor.MetricsClientListOptions{
		Timespan:    &timespan,
		Interval:    &query.Interval,
		Metricnames: &query.MetricName,
		Aggregation: &query.Aggregation,
	}
	if len(query.Filter) > 0 {
		listOptions.Filter = &query.Filter
	}

	resp, err := client.List(ctx, resourceURI, listOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to list metrics for %s: %w", query.MetricName, err)
	}
	return &resp.Response, nil
}

// azureMetricsToSeries converts an Azure Monitor metrics response into
// parsedSeries for chart rendering. Each TimeSeriesElement becomes one series,
// labeled by its dimension metadata values. The aggregation parameter selects
// which value (Average, Maximum, etc.) to extract from each MetricValue.
func azureMetricsToSeries(resp *armmonitor.Response, aggregation string) []parsedSeries {
	if resp == nil {
		return nil
	}

	var result []parsedSeries
	for _, metric := range resp.Value {
		if metric == nil {
			continue
		}
		for _, ts := range metric.Timeseries {
			if ts == nil || len(ts.Data) == 0 {
				continue
			}

			labels := make(map[string]string)
			for _, md := range ts.Metadatavalues {
				if md != nil && md.Name != nil && md.Name.Value != nil && md.Value != nil {
					labels[*md.Name.Value] = *md.Value
				}
			}

			var data []opts.LineData
			for _, point := range ts.Data {
				if point == nil || point.TimeStamp == nil {
					continue
				}
				value := extractAggregationValue(point, aggregation)
				if value == nil {
					continue
				}
				data = append(data, opts.LineData{
					Value: []any{point.TimeStamp.UnixMilli(), *value},
				})
			}
			if len(data) == 0 {
				continue
			}

			result = append(result, parsedSeries{
				metric: labels,
				data:   data,
			})
		}
	}
	return result
}

func extractAggregationValue(point *armmonitor.MetricValue, aggregation string) *float64 {
	switch strings.ToLower(aggregation) {
	case "average":
		return point.Average
	case "maximum":
		return point.Maximum
	case "minimum":
		return point.Minimum
	case "total":
		return point.Total
	case "count":
		return point.Count
	default:
		return nil
	}
}
