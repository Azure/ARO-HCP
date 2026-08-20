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
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

// metricNamespaceCosmosDB is the Azure Monitor metric namespace for Cosmos DB
// database accounts. It scopes the platform metrics (NormalizedRUConsumption,
// AutoscaledRU, ...) queried against the RP Cosmos DB account.
const metricNamespaceCosmosDB = "Microsoft.DocumentDB/databaseAccounts"

// metricNamespaceForResource maps an azureMonitor query "resource" selector to
// the Azure Monitor metric namespace used when querying it.
var metricNamespaceForResource = map[string]string{
	resourceCosmosDB: metricNamespaceCosmosDB,
}

// metricValueSelector returns a function extracting the requested aggregation
// value from an Azure Monitor MetricValue. It also validates the aggregation
// string, returning an error for unsupported aggregations.
func metricValueSelector(aggregation string) (func(*armmonitor.MetricValue) *float64, error) {
	switch armmonitor.AggregationType(aggregation) {
	case armmonitor.AggregationTypeAverage:
		return func(v *armmonitor.MetricValue) *float64 { return v.Average }, nil
	case armmonitor.AggregationTypeCount:
		return func(v *armmonitor.MetricValue) *float64 { return v.Count }, nil
	case armmonitor.AggregationTypeMaximum:
		return func(v *armmonitor.MetricValue) *float64 { return v.Maximum }, nil
	case armmonitor.AggregationTypeMinimum:
		return func(v *armmonitor.MetricValue) *float64 { return v.Minimum }, nil
	case armmonitor.AggregationTypeTotal:
		return func(v *armmonitor.MetricValue) *float64 { return v.Total }, nil
	default:
		return nil, fmt.Errorf("unsupported aggregation %q (want one of Average, Count, Maximum, Minimum, Total)", aggregation)
	}
}

// stepToISO8601 converts a Prometheus-style step string (e.g. "60s", "5m") into
// an ISO 8601 duration understood by the Azure Monitor metrics API (e.g.
// "PT1M", "PT5M"). It falls back to "PT1M" when the step cannot be parsed.
func stepToISO8601(step string) string {
	d, err := time.ParseDuration(step)
	if err != nil || d <= 0 {
		return "PT1M"
	}
	// Azure metric intervals have minute granularity at the finest for the
	// metrics we plot; round up to whole minutes with a 1-minute floor.
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("PT%dM", minutes)
}

// queryAzureMonitorMetrics fetches the metrics named in the query from the
// Azure Monitor platform-metrics API for the given resource and converts each
// metric's timeseries into a PrometheusResult, so the existing chart-building
// pipeline can render them (one series per metric on a single plot). Each
// metric is fetched in its own request so per-metric errors do not fail the
// whole query and so metrics with differing supported granularities can each
// use the requested interval independently.
func queryAzureMonitorMetrics(ctx context.Context, cred azcore.TokenCredential, resourceID azcorearm.ResourceID, q QuerySpec, start, end time.Time) ([]PrometheusResult, error) {
	client, err := armmonitor.NewMetricsClient(resourceID.SubscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %w", err)
	}

	selectValue, err := metricValueSelector(q.Aggregation)
	if err != nil {
		return nil, err
	}

	namespace := metricNamespaceForResource[q.Resource]
	timespan := fmt.Sprintf("%s/%s", start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
	interval := stepToISO8601(q.Step)
	multiMetric := len(q.Metrics) > 1

	var results []PrometheusResult
	var errs []string
	for _, m := range q.Metrics {
		label := m.Label
		if label == "" {
			label = m.Name
		}

		opts := &armmonitor.MetricsClientListOptions{
			Timespan:        &timespan,
			Interval:        &interval,
			Metricnames:     &m.Name,
			Aggregation:     &q.Aggregation,
			Metricnamespace: &namespace,
		}
		if filter := buildMetricFilter(m); filter != "" {
			opts.Filter = &filter
		}
		resp, err := client.List(ctx, resourceID.String(), opts)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}

		for _, metric := range resp.Value {
			if metric.ErrorCode != nil && *metric.ErrorCode != "" && *metric.ErrorCode != "Success" {
				msg := ""
				if metric.ErrorMessage != nil {
					msg = *metric.ErrorMessage
				}
				errs = append(errs, fmt.Sprintf("%s: %s: %s", m.Name, *metric.ErrorCode, msg))
				continue
			}
			for _, r := range metricToResults(metric, m, label, multiMetric, selectValue) {
				if len(r.Values) > 0 {
					results = append(results, r)
				}
			}
		}
	}

	if len(results) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("all metric queries failed: %s", strings.Join(errs, "; "))
	}
	return results, nil
}

// buildMetricFilter constructs the Azure Monitor $filter expression that splits
// a metric by a dimension (SplitBy) and/or restricts it to specific dimension
// values (Filter). Returns "" when neither is set. Filter keys are sorted for
// deterministic output.
func buildMetricFilter(m MetricSpec) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(m.Filter)) {
		parts = append(parts, fmt.Sprintf("%s eq '%s'", k, m.Filter[k]))
	}
	if m.SplitBy != "" {
		parts = append(parts, fmt.Sprintf("%s eq '*'", m.SplitBy))
	}
	return strings.Join(parts, " and ")
}

// metricToResults converts a single Azure Monitor metric into PrometheusResults.
// When the metric is split by a dimension it produces one result per dimension
// value (labeled by that value); otherwise it merges all timeseries into a
// single result labeled by the metric label.
func metricToResults(metric *armmonitor.Metric, m MetricSpec, label string, multiMetric bool, selectValue func(*armmonitor.MetricValue) *float64) []PrometheusResult {
	if m.SplitBy == "" {
		return []PrometheusResult{mergeTimeseries(metric.Timeseries, label, selectValue)}
	}
	var results []PrometheusResult
	for _, ts := range metric.Timeseries {
		seriesLabel := dimensionValue(ts, m.SplitBy)
		if seriesLabel == "" {
			seriesLabel = label
		} else if multiMetric {
			seriesLabel = fmt.Sprintf("%s: %s", label, seriesLabel)
		}
		results = append(results, mergeTimeseries([]*armmonitor.TimeSeriesElement{ts}, seriesLabel, selectValue))
	}
	return results
}

// dimensionValue returns the value of the named dimension from a timeseries
// element's metadata, matching the dimension name case-insensitively (Azure
// lowercases dimension names in the metadata). Returns "" when not found.
func dimensionValue(ts *armmonitor.TimeSeriesElement, dimension string) string {
	for _, md := range ts.Metadatavalues {
		if md.Name == nil || md.Name.Value == nil || md.Value == nil {
			continue
		}
		if strings.EqualFold(*md.Name.Value, dimension) {
			return *md.Value
		}
	}
	return ""
}

// mergeTimeseries flattens the given timeseries elements into a single
// PrometheusResult labeled by label, reading the selected aggregation value and
// sorting points by timestamp.
func mergeTimeseries(elements []*armmonitor.TimeSeriesElement, label string, selectValue func(*armmonitor.MetricValue) *float64) PrometheusResult {
	type point struct {
		ts  int64
		val float64
	}
	var points []point
	for _, ts := range elements {
		for _, v := range ts.Data {
			if v == nil || v.TimeStamp == nil {
				continue
			}
			val := selectValue(v)
			if val == nil {
				continue
			}
			points = append(points, point{ts: v.TimeStamp.Unix(), val: *val})
		}
	}
	// Azure may return timeseries elements unordered; the chart gap-marker
	// logic assumes ascending timestamps.
	slices.SortFunc(points, func(a, b point) int { return cmp.Compare(a.ts, b.ts) })

	result := PrometheusResult{Metric: map[string]string{"metric": label}}
	for _, p := range points {
		// Match the Prometheus [unix_seconds, "value"] tuple shape that
		// parsePrometheusValue expects.
		result.Values = append(result.Values, []any{
			float64(p.ts),
			strconv.FormatFloat(p.val, 'f', -1, 64),
		})
	}
	return result
}
