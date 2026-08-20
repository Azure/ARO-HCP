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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"k8s.io/utils/ptr"
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

func TestMetricToResult(t *testing.T) {
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

	result := metricToResult(metric, "Normalized RU", selectMax)
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
	series := parseResultsToSeries([]PrometheusResult{result})
	if len(series) != 1 {
		t.Fatalf("parseResultsToSeries returned %d series, want 1", len(series))
	}
	if len(series[0].data) != 2 {
		t.Errorf("series has %d points, want 2", len(series[0].data))
	}
}

func TestMetricToResultEmpty(t *testing.T) {
	t.Parallel()
	selectMax, _ := metricValueSelector("Maximum")
	result := metricToResult(&armmonitor.Metric{}, "Empty", selectMax)
	if len(result.Values) != 0 {
		t.Errorf("empty metric produced %d values, want 0", len(result.Values))
	}
}
