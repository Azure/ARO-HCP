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

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

func TestAzureMetricsToSeries(t *testing.T) {
	t.Parallel()

	ts1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC)
	ts3 := time.Date(2025, 1, 1, 0, 2, 0, 0, time.UTC)

	tests := []struct {
		name        string
		resp        *armmonitor.Response
		aggregation string
		wantLen     int
		checkFirst  func(t *testing.T, series parsedSeries)
	}{
		{
			name:        "nil response",
			resp:        nil,
			aggregation: "Maximum",
			wantLen:     0,
		},
		{
			name: "empty value slice",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{},
			},
			aggregation: "Maximum",
			wantLen:     0,
		},
		{
			name: "single timeseries with maximum aggregation",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Metadatavalues: []*armmonitor.MetadataValue{
									{
										Name:  &armmonitor.LocalizableString{Value: ptr.To("CollectionName")},
										Value: ptr.To("Resources"),
									},
								},
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Maximum: ptr.To(42.5)},
									{TimeStamp: &ts2, Maximum: ptr.To(55.0)},
									{TimeStamp: &ts3, Maximum: ptr.To(30.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Maximum",
			wantLen:     1,
			checkFirst: func(t *testing.T, series parsedSeries) {
				if series.metric["CollectionName"] != "Resources" {
					t.Errorf("label CollectionName = %q, want %q", series.metric["CollectionName"], "Resources")
				}
				if len(series.data) != 3 {
					t.Fatalf("data points = %d, want 3", len(series.data))
				}
				arr, ok := series.data[1].Value.([]any)
				if !ok || len(arr) != 2 {
					t.Fatalf("data[1].Value not []any of len 2")
				}
				if v, ok := arr[1].(float64); !ok || v != 55.0 {
					t.Errorf("data[1] value = %v, want 55.0", arr[1])
				}
			},
		},
		{
			name: "average aggregation",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Average: ptr.To(10.0), Maximum: ptr.To(20.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Average",
			wantLen:     1,
			checkFirst: func(t *testing.T, series parsedSeries) {
				arr := series.data[0].Value.([]any)
				if v := arr[1].(float64); v != 10.0 {
					t.Errorf("value = %v, want 10.0 (average, not maximum)", v)
				}
			},
		},
		{
			name: "count aggregation",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Count: ptr.To(100.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Count",
			wantLen:     1,
			checkFirst: func(t *testing.T, series parsedSeries) {
				arr := series.data[0].Value.([]any)
				if v := arr[1].(float64); v != 100.0 {
					t.Errorf("value = %v, want 100.0", v)
				}
			},
		},
		{
			name: "multiple timeseries",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Metadatavalues: []*armmonitor.MetadataValue{
									{
										Name:  &armmonitor.LocalizableString{Value: ptr.To("CollectionName")},
										Value: ptr.To("Resources"),
									},
								},
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Maximum: ptr.To(10.0)},
								},
							},
							{
								Metadatavalues: []*armmonitor.MetadataValue{
									{
										Name:  &armmonitor.LocalizableString{Value: ptr.To("CollectionName")},
										Value: ptr.To("Billing"),
									},
								},
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Maximum: ptr.To(20.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Maximum",
			wantLen:     2,
		},
		{
			name: "nil data points skipped",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Maximum: ptr.To(10.0)},
									nil,
									{TimeStamp: &ts2, Maximum: ptr.To(20.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Maximum",
			wantLen:     1,
			checkFirst: func(t *testing.T, series parsedSeries) {
				if len(series.data) != 2 {
					t.Errorf("data points = %d, want 2 (nil skipped)", len(series.data))
				}
			},
		},
		{
			name: "missing aggregation value skipped",
			resp: &armmonitor.Response{
				Value: []*armmonitor.Metric{
					{
						Timeseries: []*armmonitor.TimeSeriesElement{
							{
								Data: []*armmonitor.MetricValue{
									{TimeStamp: &ts1, Average: ptr.To(10.0)},
								},
							},
						},
					},
				},
			},
			aggregation: "Maximum",
			wantLen:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			series := azureMetricsToSeries(tt.resp, tt.aggregation)
			if len(series) != tt.wantLen {
				t.Fatalf("series count = %d, want %d", len(series), tt.wantLen)
			}
			if tt.checkFirst != nil && len(series) > 0 {
				tt.checkFirst(t, series[0])
			}
		})
	}
}

func TestExtractAggregationValue(t *testing.T) {
	t.Parallel()

	point := &armmonitor.MetricValue{
		Average: ptr.To(10.0),
		Maximum: ptr.To(20.0),
		Minimum: ptr.To(5.0),
		Total:   ptr.To(100.0),
		Count:   ptr.To(50.0),
	}

	tests := []struct {
		name        string
		aggregation string
		want        float64
	}{
		{name: "average", aggregation: "Average", want: 10.0},
		{name: "maximum", aggregation: "Maximum", want: 20.0},
		{name: "minimum", aggregation: "Minimum", want: 5.0},
		{name: "total", aggregation: "Total", want: 100.0},
		{name: "count", aggregation: "Count", want: 50.0},
		{name: "case insensitive", aggregation: "maximum", want: 20.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAggregationValue(point, tt.aggregation)
			if got == nil {
				t.Fatal("got nil, want non-nil")
			}
			if *got != tt.want {
				t.Errorf("got %v, want %v", *got, tt.want)
			}
		})
	}
}

func TestExtractAggregationValueNil(t *testing.T) {
	t.Parallel()

	point := &armmonitor.MetricValue{}

	tests := []struct {
		name        string
		aggregation string
	}{
		{name: "nil average", aggregation: "Average"},
		{name: "nil maximum", aggregation: "Maximum"},
		{name: "nil minimum", aggregation: "Minimum"},
		{name: "nil total", aggregation: "Total"},
		{name: "nil count", aggregation: "Count"},
		{name: "unknown type returns nil", aggregation: "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAggregationValue(point, tt.aggregation)
			if got != nil {
				t.Errorf("got %v, want nil", *got)
			}
		})
	}
}

func TestQueryDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query QuerySpec
		want  string
	}{
		{
			name:  "promql query",
			query: QuerySpec{Type: queryTypePromQL, Query: "rate(foo[5m])"},
			want:  "rate(foo[5m])",
		},
		{
			name:  "azure metric without filter",
			query: QuerySpec{Type: queryTypeAzureMetric, MetricName: "NormalizedRuConsumption", Aggregation: "Maximum"},
			want:  "Maximum(NormalizedRuConsumption)",
		},
		{
			name:  "azure metric with filter",
			query: QuerySpec{Type: queryTypeAzureMetric, MetricName: "TotalRequests", Aggregation: "Count", Filter: "StatusCode eq '429'"},
			want:  "Count(TotalRequests) | StatusCode eq '429'",
		},
		{
			name:  "default type treated as promql",
			query: QuerySpec{Query: "up"},
			want:  "up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.query.queryDisplay()
			if got != tt.want {
				t.Errorf("queryDisplay() = %q, want %q", got, tt.want)
			}
		})
	}
}
