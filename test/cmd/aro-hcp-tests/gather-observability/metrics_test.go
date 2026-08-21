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
	"strings"
	"testing"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestStepToISO8601(t *testing.T) {
	t.Parallel()
	tests := []struct {
		step string
		want string
	}{
		{"60s", "PT1M"},
		{"5m", "PT5M"},
		{"90s", "PT2M"}, // rounds up to whole minutes
		{"1h", "PT60M"},
		{"", "PT1M"},        // unparseable falls back
		{"garbage", "PT1M"}, // unparseable falls back
		{"0s", "PT1M"},      // non-positive falls back
	}
	for _, tt := range tests {
		if got := stepToISO8601(tt.step); got != tt.want {
			t.Errorf("stepToISO8601(%q) = %q, want %q", tt.step, got, tt.want)
		}
	}
}

func TestMetricValueSelector(t *testing.T) {
	t.Parallel()
	v := &armmonitor.MetricValue{
		Average: ptr.To(1.0),
		Count:   ptr.To(2.0),
		Maximum: ptr.To(3.0),
		Minimum: ptr.To(4.0),
		Total:   ptr.To(5.0),
	}
	tests := []struct {
		aggregation string
		want        float64
	}{
		{"Average", 1.0},
		{"Count", 2.0},
		{"Maximum", 3.0},
		{"Minimum", 4.0},
		{"Total", 5.0},
	}
	for _, tt := range tests {
		sel, err := metricValueSelector(tt.aggregation)
		if err != nil {
			t.Fatalf("metricValueSelector(%q) unexpected error: %v", tt.aggregation, err)
		}
		if got := sel(v); got == nil || *got != tt.want {
			t.Errorf("metricValueSelector(%q) selected %v, want %v", tt.aggregation, got, tt.want)
		}
	}
	if _, err := metricValueSelector("Median"); err == nil {
		t.Error("metricValueSelector(\"Median\") expected error, got nil")
	}
}

func TestMetricToResultsMerged(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	selectMax, err := metricValueSelector("Maximum")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metric := &armmonitor.Metric{
		Timeseries: []*armmonitor.TimeSeriesElement{
			{
				Data: []*armmonitor.MetricValue{
					// intentionally out of order to exercise sorting
					{TimeStamp: ptr.To(t2), Maximum: ptr.To(30.0)},
					{TimeStamp: ptr.To(t0), Maximum: ptr.To(10.0)},
					// gap in aggregation value (nil Maximum) is skipped
					{TimeStamp: ptr.To(t1)},
				},
			},
		},
	}

	// No SplitBy -> single merged result labeled by the metric label.
	results := metricToResults(metric, MetricSpec{Name: "NormalizedRUConsumption"}, "Normalized RU", false, selectMax, nil)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	result := results[0]
	if got := result.Metric["metric"]; got != "Normalized RU" {
		t.Errorf("label = %q, want %q", got, "Normalized RU")
	}
	if len(result.Values) != 2 {
		t.Fatalf("got %d values, want 2 (nil aggregation skipped): %+v", len(result.Values), result.Values)
	}
	// Ascending order after sort.
	if ts := result.Values[0][0].(float64); int64(ts) != t0.Unix() {
		t.Errorf("first ts = %v, want %v", int64(ts), t0.Unix())
	}
	if ts := result.Values[1][0].(float64); int64(ts) != t2.Unix() {
		t.Errorf("second ts = %v, want %v", int64(ts), t2.Unix())
	}
	if v := result.Values[0][1].(string); v != "10" {
		t.Errorf("first value = %q, want %q", v, "10")
	}

	// The converted result flows through the existing series parser.
	series := parseResultsToSeries(results)
	if len(series) != 1 {
		t.Fatalf("parseResultsToSeries returned %d series, want 1", len(series))
	}
	if len(series[0].data) != 2 {
		t.Errorf("series has %d points, want 2", len(series[0].data))
	}
}

func makeDimTimeseries(dimName, dimValue string, ts time.Time, val float64) *armmonitor.TimeSeriesElement {
	return &armmonitor.TimeSeriesElement{
		Metadatavalues: []*armmonitor.MetadataValue{
			{Name: &armmonitor.LocalizableString{Value: ptr.To(dimName)}, Value: ptr.To(dimValue)},
		},
		Data: []*armmonitor.MetricValue{
			{TimeStamp: ptr.To(ts), Maximum: ptr.To(val), Count: ptr.To(val)},
		},
	}
}

func TestMetricToResultsSplitByDimension(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	selectMax, _ := metricValueSelector("Maximum")

	metric := &armmonitor.Metric{
		Timeseries: []*armmonitor.TimeSeriesElement{
			// Azure lowercases the dimension name in metadata.
			makeDimTimeseries("collectionname", "Resources", t0, 90.0),
			makeDimTimeseries("collectionname", "Billing", t0, 40.0),
		},
	}

	// Single metric split by container -> one series per container, labeled by
	// the dimension value only (no metric-label prefix).
	results := metricToResults(metric, MetricSpec{Name: "NormalizedRUConsumption", SplitBy: "CollectionName"}, "Normalized RU", false, selectMax, nil)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	labels := map[string]bool{
		results[0].Metric["metric"]: true,
		results[1].Metric["metric"]: true,
	}
	if !labels["Resources"] || !labels["Billing"] {
		t.Errorf("labels = %v, want Resources and Billing", labels)
	}

	// With multiple metrics in the query, the metric label prefixes the
	// dimension value to disambiguate.
	prefixed := metricToResults(metric, MetricSpec{Name: "NormalizedRUConsumption", SplitBy: "CollectionName"}, "Normalized RU", true, selectMax, nil)
	if prefixed[0].Metric["metric"] != "Normalized RU: Resources" {
		t.Errorf("prefixed label = %q, want %q", prefixed[0].Metric["metric"], "Normalized RU: Resources")
	}
}

func TestMetricToResultsNormalizeByAutoscaleMax(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	selectMax, _ := metricValueSelector("Maximum")

	metric := &armmonitor.Metric{
		Timeseries: []*armmonitor.TimeSeriesElement{
			makeDimTimeseries("collectionname", "Resources", t0, 4000.0),     // ceiling 8000 -> 50%
			makeDimTimeseries("collectionname", "Billing", t0, 1000.0),       // ceiling 4000 -> 25%
			makeDimTimeseries("collectionname", "Manifests-MC-2", t0, 800.0), // prefix ceiling 8000 -> 10%
			makeDimTimeseries("collectionname", "Unknown", t0, 1234.0),       // unknown ceiling -> unscaled
		},
	}
	maxFor := func(container string) float64 {
		switch {
		case container == "Resources":
			return 8000
		case container == "Billing":
			return 4000
		case strings.HasPrefix(container, "Manifests-MC-"):
			return 8000
		default:
			return 0
		}
	}

	results := metricToResults(metric, MetricSpec{Name: "AutoscaledRU", SplitBy: "CollectionName", NormalizeByAutoscaleMax: true}, "Autoscaled RU", true, selectMax, maxFor)

	got := map[string]string{}
	for _, r := range results {
		got[r.Metric["metric"]] = r.Values[0][1].(string)
	}
	want := map[string]string{
		"Autoscaled RU: Resources":      "50",
		"Autoscaled RU: Billing":        "25",
		"Autoscaled RU: Manifests-MC-2": "10",
		"Autoscaled RU: Unknown":        "1234", // ceiling unknown -> left as-is
	}
	for label, wantVal := range want {
		if got[label] != wantVal {
			t.Errorf("series %q = %q, want %q", label, got[label], wantVal)
		}
	}
}

func TestBuildMetricFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		m    MetricSpec
		want string
	}{
		{"none", MetricSpec{}, ""},
		{"split only", MetricSpec{SplitBy: "CollectionName"}, "CollectionName eq '*'"},
		{"filter only", MetricSpec{Filter: map[string]string{"StatusCode": "429"}}, "StatusCode eq '429'"},
		{
			"split and filter",
			MetricSpec{SplitBy: "CollectionName", Filter: map[string]string{"StatusCode": "429"}},
			"StatusCode eq '429' and CollectionName eq '*'",
		},
		{
			"multiple filters sorted",
			MetricSpec{Filter: map[string]string{"StatusCode": "429", "Region": "eastus"}},
			"Region eq 'eastus' and StatusCode eq '429'",
		},
	}
	for _, tt := range tests {
		if got := buildMetricFilter(tt.m); got != tt.want {
			t.Errorf("%s: buildMetricFilter = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMetricToResultsEmpty(t *testing.T) {
	t.Parallel()
	selectMax, _ := metricValueSelector("Maximum")
	results := metricToResults(&armmonitor.Metric{}, MetricSpec{Name: "X"}, "Empty", false, selectMax, nil)
	if len(results) != 1 || len(results[0].Values) != 0 {
		t.Errorf("empty metric produced %+v, want single result with 0 values", results)
	}
}

func TestAzMetricsCommand(t *testing.T) {
	tw := timing.TimeWindow{
		Start: time.Date(2026, 4, 13, 6, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 4, 13, 8, 0, 0, 0, time.UTC),
	}
	const resourceID = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.DocumentDB/databaseAccounts/acct"

	t.Run("split metric with dimension filter", func(t *testing.T) {
		q := QuerySpec{
			Source:      sourceAzureMonitor,
			Resource:    resourceCosmosDB,
			Aggregation: "Count",
			Step:        "60s",
			Metrics: []MetricSpec{{
				Name:    "TotalRequests",
				SplitBy: "CollectionName",
				Filter:  map[string]string{"StatusCode": "429"},
			}},
		}
		got := azMetricsCommand(q, resourceID, tw)
		for _, want := range []string{
			"az monitor metrics list",
			"--resource " + resourceID,
			"--namespace Microsoft.DocumentDB/databaseAccounts",
			"--metrics TotalRequests",
			"--aggregation Count",
			"--interval PT1M",
			`--filter "StatusCode eq '429' and CollectionName eq '*'"`,
			"--start-time 2026-04-13T06:00:00Z",
			"--end-time 2026-04-13T08:00:00Z",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("command missing %q\n---\n%s", want, got)
			}
		}
	})

	t.Run("split metric without filter still filters by dimension wildcard", func(t *testing.T) {
		q := QuerySpec{
			Source:      sourceAzureMonitor,
			Resource:    resourceCosmosDB,
			Aggregation: "Maximum",
			Step:        "60s",
			Metrics:     []MetricSpec{{Name: "NormalizedRUConsumption", SplitBy: "CollectionName"}},
		}
		got := azMetricsCommand(q, resourceID, tw)
		if !strings.Contains(got, `--filter "CollectionName eq '*'"`) {
			t.Errorf("expected splitBy wildcard filter, got:\n%s", got)
		}
		if !strings.Contains(got, "--metrics NormalizedRUConsumption") {
			t.Errorf("expected metric name, got:\n%s", got)
		}
	})

	t.Run("promql source returns the query verbatim", func(t *testing.T) {
		lang, body := queryFooter(QuerySpec{Source: sourcePrometheus, Query: "up == 1"}, "", tw)
		if lang != "PromQL" || body != "up == 1" {
			t.Errorf("queryFooter = (%q, %q), want (PromQL, up == 1)", lang, body)
		}
	})

	t.Run("azure source is labeled Azure Monitor", func(t *testing.T) {
		q := QuerySpec{
			Source:      sourceAzureMonitor,
			Resource:    resourceCosmosDB,
			Aggregation: "Maximum",
			Step:        "60s",
			Metrics:     []MetricSpec{{Name: "AutoscaledRU", SplitBy: "CollectionName"}},
		}
		lang, _ := queryFooter(q, resourceID, tw)
		if lang != "Azure Monitor" {
			t.Errorf("lang = %q, want Azure Monitor", lang)
		}
	})
}
