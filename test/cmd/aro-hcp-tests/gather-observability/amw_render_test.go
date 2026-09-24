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

package gatherobservability

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

var amwRenderStart = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func amwRenderTestSeries(dimensions map[string]string, values ...*float64) *armmonitor.TimeSeriesElement {
	series := &armmonitor.TimeSeriesElement{}
	for name, value := range dimensions {
		series.Metadatavalues = append(series.Metadatavalues, &armmonitor.MetadataValue{Name: &armmonitor.LocalizableString{Value: to.Ptr(name)}, Value: to.Ptr(value)})
	}
	for i, value := range values {
		series.Data = append(series.Data, &armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(time.Duration(i) * time.Minute)), Maximum: value, Total: value})
	}
	return series
}

func amwRenderTestMetric(name string, series ...*armmonitor.TimeSeriesElement) amwMetric {
	metric := amwMetric{Name: name, Aggregation: "Maximum", Response: &armmonitor.MetricsClientListResponse{}}
	if name == "MetricIngestionRequest_Count" {
		metric.Aggregation = "Total"
	}
	metric.Response.Interval = to.Ptr("PT1M")
	metric.Response.Timespan = to.Ptr(amwRenderStart.Format(time.RFC3339) + "/" + amwRenderStart.Add(5*time.Minute).Format(time.RFC3339))
	metric.Response.Value = []*armmonitor.Metric{{Name: &armmonitor.LocalizableString{Value: to.Ptr(name)}, ErrorCode: to.Ptr("Success"), Timeseries: series}}
	return metric
}

func TestAMWRenderCapacityPairing(t *testing.T) {
	blue, green := map[string]string{"StampColor": "blue"}, map[string]string{"StampColor": "green"}
	usage := amwRenderTestMetric("ActiveTimeSeries",
		amwRenderTestSeries(blue, to.Ptr(10.0), to.Ptr(20.0), nil, to.Ptr(40.0), to.Ptr(99.0)),
		amwRenderTestSeries(green, nil, to.Ptr(70.0), to.Ptr(80.0)),
		amwRenderTestSeries(map[string]string{"StampColor": "unpaired"}, to.Ptr(1.0)))
	limit := amwRenderTestMetric("ActiveTimeSeriesLimit",
		amwRenderTestSeries(blue, to.Ptr(100.0), nil, to.Ptr(100.0), to.Ptr(80.0)),
		amwRenderTestSeries(green, to.Ptr(200.0), to.Ptr(200.0)),
		amwRenderTestSeries(map[string]string{"StampColor": "limit-only"}, to.Ptr(1000.0)))
	// A duplicated latest blue timestamp invalidates that minute, not the older pair.
	usage.Response.Value[0].Timeseries[0].Data = append(usage.Response.Value[0].Timeseries[0].Data,
		&armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(3 * time.Minute)), Maximum: to.Ptr(40.0)})
	u, warnings := amwReadRenderSeries(usage, amwRenderStart, amwRenderStart.Add(4*time.Minute))
	l, _ := amwReadRenderSeries(limit, amwRenderStart, amwRenderStart.Add(4*time.Minute))
	if !strings.Contains(strings.Join(warnings, ";"), "duplicate timestamps") {
		t.Fatalf("duplicate timestamp warning missing: %v", warnings)
	}
	rows := amwCapacityRows(u, l, amwRenderStart)
	if len(rows) != 4 {
		t.Fatalf("want separate rows for both stamps and unmatched dimensions, got %+v", rows)
	}
	for _, row := range rows {
		switch row.Label {
		case `StampColor="blue"`:
			if row.Usage != "10" || row.Limit != "100" || row.Utilization != "10%" || row.Headroom != "90" || row.At != "2026-09-20T12:00:00Z" {
				t.Errorf("blue must use its latest exact unambiguous pair: %+v", row)
			}
		case `StampColor="green"`:
			if row.Usage != "70" || row.Limit != "200" || row.Utilization != "35%" || row.Headroom != "130" || row.At != "2026-09-20T12:01:00Z" {
				t.Errorf("green must retain its independent latest pair: %+v", row)
			}
		default:
			if row.Usage != "unknown" || row.Limit != "unknown" || row.At != "unknown" {
				t.Errorf("unmatched stamps must not borrow a pair: %+v", row)
			}
		}
	}
}

func TestAMWRenderCapacityLimits(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		usage, limit          float64
		utilization, headroom string
	}{
		{"zero limit", 10, 0, "unknown", "-10"},
		{"zero usage", 0, 10, "0%", "10"},
		{"over limit", 12, 10, "120%", "-2"},
		{"overflow ratio", math.MaxFloat64, math.SmallestNonzeroFloat64, "unknown", "-1.79769e+308"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dimensions := map[string]string{"StampColor": "blue"}
			u, _ := amwReadRenderSeries(amwRenderTestMetric("ActiveTimeSeries", amwRenderTestSeries(dimensions, &tc.usage)), amwRenderStart, amwRenderStart.Add(time.Minute))
			l, _ := amwReadRenderSeries(amwRenderTestMetric("ActiveTimeSeriesLimit", amwRenderTestSeries(dimensions, &tc.limit)), amwRenderStart, amwRenderStart.Add(time.Minute))
			rows := amwCapacityRows(u, l, amwRenderStart)
			if len(rows) != 1 || rows[0].Utilization != tc.utilization || rows[0].Headroom != tc.headroom {
				t.Fatalf("unexpected derived values: %+v", rows)
			}
		})
	}
}

func TestAMWRenderValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*amwMetric)
		usable bool
		warn   string
	}{
		{"partial response", func(m *amwMetric) { m.Error = "partial collection" }, true, ""},
		{"error message", func(m *amwMetric) { m.Response.Value[0].ErrorMessage = to.Ptr("raw body must not appear") }, true, "partial data"},
		{"failed metric", func(m *amwMetric) { m.Response.Value[0].ErrorCode = to.Ptr("BadRequest") }, false, "not Success"},
		{"case insensitive success", func(m *amwMetric) { m.Response.Value[0].ErrorCode = to.Ptr("success") }, true, ""},
		{"nil response", func(m *amwMetric) { m.Response = nil }, false, "missing response"},
		{"wrong interval", func(m *amwMetric) { m.Response.Interval = to.Ptr("PT5M") }, false, "not PT1M"},
		{"nil interval", func(m *amwMetric) { m.Response.Interval = nil }, false, "not PT1M"},
		{"wrong name", func(m *amwMetric) { m.Response.Value[0].Name.Value = to.Ptr("different") }, false, "exactly once"},
		{"nil metric", func(m *amwMetric) { m.Response.Value = []*armmonitor.Metric{nil} }, false, "exactly once"},
		{"duplicate metric", func(m *amwMetric) { m.Response.Value = append(m.Response.Value, m.Response.Value[0]) }, false, "exactly once"},
		{"wrong aggregation", func(m *amwMetric) { m.Aggregation = "Total" }, false, "aggregation"},
		{"missing stamp", func(m *amwMetric) { m.Response.Value[0].Timeseries[0].Metadatavalues = nil }, false, "dimensions"},
		{"duplicate stamp", func(m *amwMetric) {
			s := m.Response.Value[0].Timeseries[0]
			s.Metadatavalues = append(s.Metadatavalues, &armmonitor.MetadataValue{Name: &armmonitor.LocalizableString{Value: to.Ptr("stampcolor")}, Value: to.Ptr("green")})
		}, false, "dimensions"},
		{"duplicate series", func(m *amwMetric) {
			m.Response.Value[0].Timeseries = append(m.Response.Value[0].Timeseries, m.Response.Value[0].Timeseries[0])
		}, false, "dimension sets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metric := amwRenderTestMetric("ActiveTimeSeries", amwRenderTestSeries(map[string]string{"StampColor": "blue"}, to.Ptr(7.0)))
			tc.mutate(&metric)
			series, warnings := amwReadRenderSeries(metric, amwRenderStart, amwRenderStart.Add(time.Minute))
			if (len(series) > 0) != tc.usable || !strings.Contains(strings.Join(warnings, ";"), tc.warn) {
				t.Fatalf("usable=%v, warnings=%v; expected usable=%v and warning %q", len(series) > 0, warnings, tc.usable, tc.warn)
			}
		})
	}
}

func TestAMWRenderSamplesAndGaps(t *testing.T) {
	raw := amwRenderTestSeries(map[string]string{"StampColor": "blue"}, to.Ptr(0.0), nil, to.Ptr(2.0), to.Ptr(math.NaN()), to.Ptr(math.Inf(1)), to.Ptr(-1.0), to.Ptr(6.0), to.Ptr(7.0), to.Ptr(999.0))
	raw.Data = append(raw.Data,
		&armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(6 * time.Minute)), Maximum: to.Ptr(6.0)},
		&armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(6 * time.Minute)), Maximum: to.Ptr(6.0)},
		&armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(30 * time.Second)), Maximum: to.Ptr(999.0)},
		&armmonitor.MetricValue{TimeStamp: to.Ptr(amwRenderStart.Add(-time.Minute)), Maximum: to.Ptr(999.0)}, nil, &armmonitor.MetricValue{})
	series, warnings := amwReadRenderSeries(amwRenderTestMetric("ActiveTimeSeries", raw), amwRenderStart, amwRenderStart.Add(8*time.Minute))
	if len(series) != 1 || len(series[0].Values) != 8 {
		t.Fatalf("expected one bounded minute-grid series: %+v", series)
	}
	for i, value := range series[0].Values {
		if want := i == 0 || i == 2 || i == 7; (value != nil) != want {
			t.Errorf("minute %d: value=%v, want numeric=%v", i, value, want)
		}
	}
	for _, warning := range []string{"duplicate timestamps", "off-minute", "non-finite or negative", "missing timestamps"} {
		if !strings.Contains(strings.Join(warnings, ";"), warning) {
			t.Errorf("missing %q warning: %v", warning, warnings)
		}
	}
	chart, budget := amwRenderChart{Series: series}, 96
	amwPrepareChart(&chart, amwRenderStart, &budget)
	if path := chart.Series[0].Path; strings.Count(path, "M") != 3 || strings.Contains(path, "L") || strings.Contains(path, "NaN") || strings.Contains(path, "Inf") {
		t.Fatalf("isolated numeric observations must remain visible without bridging gaps: %q", path)
	}
	if chart.Rows[0].Usage != "7" || chart.Rows[0].At != "2026-09-20T12:07:00Z" {
		t.Fatalf("latest must exclude the End sample: %+v", chart.Rows)
	}
}

func TestAMWRenderExactDimensions(t *testing.T) {
	u, _ := amwReadRenderSeries(amwRenderTestMetric("ActiveTimeSeries", amwRenderTestSeries(map[string]string{"StampColor": "blue", "Extra": "a"}, to.Ptr(5.0))), amwRenderStart, amwRenderStart.Add(time.Minute))
	l, _ := amwReadRenderSeries(amwRenderTestMetric("ActiveTimeSeriesLimit", amwRenderTestSeries(map[string]string{"StampColor": "blue", "Extra": "b"}, to.Ptr(10.0))), amwRenderStart, amwRenderStart.Add(time.Minute))
	for _, row := range amwCapacityRows(u, l, amwRenderStart) {
		if row.Utilization != "unknown" || row.At != "unknown" {
			t.Fatalf("same stamp alone does not establish an exact dimension pair: %+v", row)
		}
	}
	// Keys must not collide when labels themselves contain delimiters.
	metric := amwRenderTestMetric("ActiveTimeSeries",
		amwRenderTestSeries(map[string]string{"StampColor": "blue,Extra=a"}, to.Ptr(1.0)),
		amwRenderTestSeries(map[string]string{"StampColor": "blue", "Extra": "a"}, to.Ptr(2.0)))
	if series, _ := amwReadRenderSeries(metric, amwRenderStart, amwRenderStart.Add(time.Minute)); len(series) != 2 {
		t.Fatalf("dimension key delimiter collision: %+v", series)
	}
}

func TestAMWRenderUnknownData(t *testing.T) {
	for _, name := range []string{"EventsDropped", "TimeSeriesSamplesDropped", "MetricIngestionRequest_Count"} {
		metric := amwRenderTestMetric(name, amwRenderTestSeries(map[string]string{"StampColor": "blue"}, to.Ptr(1.0)))
		if series, warnings := amwReadRenderSeries(metric, amwRenderStart, amwRenderStart.Add(time.Minute)); len(series) != 0 || len(warnings) == 0 {
			t.Errorf("%s: missing Reason or DCR dimensions must be withheld with a warning", name)
		}
	}
	metric := amwRenderTestMetric("ActiveTimeSeries", amwRenderTestSeries(map[string]string{"StampColor": "blue"}, nil, nil))
	series, _ := amwReadRenderSeries(metric, amwRenderStart, amwRenderStart.Add(24*time.Hour))
	chart, budget := amwRenderChart{Series: series}, 96
	amwPrepareChart(&chart, amwRenderStart, &budget)
	if len(chart.Series) != 0 || len(chart.Rows) != 1 || chart.Rows[0].Usage != "unknown" || chart.Rows[0].At != "unknown" {
		t.Fatalf("all-null data must provide an unknown table row, not a zero chart: %+v", chart)
	}
}

func amwRenderFixture() amwReport {
	report := amwReport{Start: amwRenderStart, End: amwRenderStart.Add(90 * time.Minute)}
	workspace := amwResource{Kind: "workspace", Name: "int-uksouth-monitor", ID: "/subscriptions/synthetic/resourceGroups/observability/providers/Microsoft.Monitor/accounts/int-uksouth-monitor"}
	for _, name := range []string{"ActiveTimeSeries", "ActiveTimeSeriesLimit", "EventsPerMinuteIngested", "EventsPerMinuteIngestedLimit", "EventsDropped", "TimeSeriesSamplesDropped"} {
		var all []*armmonitor.TimeSeriesElement
		for stampIndex, stamp := range []string{"blue", "green"} {
			reasons := []string{""}
			if strings.HasSuffix(name, "Dropped") {
				reasons = []string{"LimitThrottling", "InvalidLabel", "FutureUnknownReason"}
			}
			for _, reason := range reasons {
				dimensions := map[string]string{"StampColor": stamp}
				if reason != "" {
					dimensions["Reason"] = reason
				}
				var values []*float64
				for minute := 0; minute < 90; minute++ {
					v := 1200000 + float64(minute)*7000 + 180000*math.Sin(float64(minute)/8) + float64(stampIndex)*400000
					if strings.HasSuffix(name, "Limit") {
						v = 3000000
					}
					if strings.HasPrefix(name, "EventsPerMinute") {
						v *= 15
					}
					if reason != "" {
						v = 0
						if minute > 55 && minute < 70 {
							v = float64((minute-55)*(70-minute) + stampIndex*10)
						}
					}
					if minute > 30 && minute < 35 {
						values = append(values, nil)
					} else {
						values = append(values, &v)
					}
				}
				all = append(all, amwRenderTestSeries(dimensions, values...))
			}
		}
		metric := amwRenderTestMetric(name, all...)
		metric.Response.Timespan = to.Ptr(report.Start.Format(time.RFC3339) + "/" + report.End.Format(time.RFC3339))
		workspace.Metrics = append(workspace.Metrics, metric)
	}
	workspace.Metrics[0].Error = "Synthetic partial response: minutes 31-34 unavailable"
	dcr := amwResource{Kind: "dcr", Name: "int-uksouth-ingestion", ID: "/subscriptions/synthetic/resourceGroups/observability/providers/Microsoft.Insights/dataCollectionRules/int-uksouth-ingestion", Workspaces: []string{workspace.ID}}
	var requests []*armmonitor.TimeSeriesElement
	for _, code := range []string{"204", "429", "503"} {
		for _, stream := range []string{"Microsoft-PrometheusMetrics", "secondary-stream"} {
			requests = append(requests, amwRenderTestSeries(map[string]string{"InputStreamId": stream, "ResponseCode": code}, to.Ptr(10.0), nil, to.Ptr(20.0), to.Ptr(30.0)))
		}
	}
	dcr.Metrics = []amwMetric{amwRenderTestMetric("MetricIngestionRequest_Count", requests...)}
	report.Resources = []amwResource{workspace, dcr}
	return report
}

func TestAMWRenderHTML(t *testing.T) {
	report := amwRenderFixture()
	output, err := renderAMWHTML(report)
	if err != nil {
		t.Fatalf("render synthetic report: %v", err)
	}
	for _, want := range []string{"ActiveTimeSeries + limit", "EventsPerMinuteIngested + limit", "EventsDropped | Reason=LimitThrottling", "TimeSeriesSamplesDropped | Reason=LimitThrottling", "FutureUnknownReason", "InvalidLabel", "InputStreamId=", "ResponseCode=429", "15,000 requests/min", "50 GB/min", "Original requested bounds", "Synthetic partial response", `href="amw.json"`, "aggregation=Maximum", "aggregation=Total", "Table summary remains available", "stroke-dasharray", `name="viewport"`, "grid-template-columns: minmax(0, 1fr)"} {
		if !strings.Contains(html.UnescapeString(string(output)), want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(string(output), "<script") || strings.Contains(string(output), "echarts") || strings.Contains(string(output), "ZgotmplZ") {
		t.Fatal("offline rendering must not require scripts or contain rejected template values")
	}
	doc, err := html.Parse(strings.NewReader(string(output)))
	if err != nil {
		t.Fatal(err)
	}
	articles := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "article" {
			articles++
			var text strings.Builder
			var collect func(*html.Node)
			collect = func(n *html.Node) {
				if n.Type == html.TextNode {
					text.WriteString(n.Data)
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					collect(c)
				}
			}
			collect(node)
			content := text.String()
			if strings.Contains(content, "| Reason=LimitThrottling") && (strings.Contains(content, "FutureUnknownReason") || strings.Contains(content, "InvalidLabel")) {
				t.Error("throttling chart mixed in other drop reasons")
			}
			if strings.Contains(content, "| ResponseCode=429") && (strings.Contains(content, `ResponseCode="204"`) || strings.Contains(content, `ResponseCode="503"`)) {
				t.Error("429 chart mixed in other response codes")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if articles != 8 {
		t.Errorf("want six workspace and two DCR plots, got %d", articles)
	}
	if directory := os.Getenv("AMW_RENDER_FIXTURE"); directory != "" {
		if err := os.WriteFile(filepath.Join(directory, "amw.html"), output, 0600); err != nil {
			t.Fatalf("write optional synthetic screenshot fixture: %v", err)
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "amw.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAMWRenderEscapingAndErrors(t *testing.T) {
	malicious := `<script>alert("x")</script><img src=x onerror=alert(1)> & ' "`
	report := amwRenderFixture()
	report.Resources[0].Name = malicious
	report.Resources[0].ID = malicious
	report.Resources[0].Metrics[0].Error = malicious
	report.Resources[0].Metrics[0].Response.Value[0].Timeseries[0].Metadatavalues[0].Value = to.Ptr(malicious)
	report.Resources[0].Metrics[0].Response.Value[0].ErrorMessage = to.Ptr("RAW-SDK-BODY-NOT-FOR-HTML")
	report.Errors = []string{malicious}
	report.Discovery = []amwDiscovery{{SubscriptionID: malicious, Error: "discovery unavailable"}}
	output, err := renderAMWHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<script>", "<img", "RAW-SDK-BODY-NOT-FOR-HTML"} {
		if strings.Contains(string(output), forbidden) {
			t.Errorf("HTML contains unsafe or raw content %q", forbidden)
		}
	}
	if !strings.Contains(string(output), "&lt;script&gt;") || !strings.Contains(string(output), "discovery unavailable") {
		t.Fatal("escaped labels and exported errors must remain visible")
	}
}

func TestAMWRenderWindow(t *testing.T) {
	for _, duration := range []time.Duration{time.Minute, 24 * time.Hour, 24*time.Hour + time.Minute, 0, -time.Minute} {
		report := amwReport{Start: amwRenderStart, End: amwRenderStart.Add(duration)}
		_, err := renderAMWHTML(report)
		if valid := duration > 0 && duration <= 24*time.Hour; (err == nil) != valid {
			t.Errorf("duration %v: error=%v, want valid=%v", duration, err, valid)
		}
	}
	for _, report := range []amwReport{{}, {Start: amwRenderStart.Add(time.Second), End: amwRenderStart.Add(time.Minute)}, {Start: amwRenderStart, End: amwRenderStart.Add(time.Minute + time.Second)}} {
		if _, err := renderAMWHTML(report); err == nil {
			t.Errorf("invalid report window accepted: %+v", report)
		}
	}
}

func TestAMWRenderBoundsAndFallback(t *testing.T) {
	var raw []*armmonitor.TimeSeriesElement
	for i := 0; i < 110; i++ {
		raw = append(raw, amwRenderTestSeries(map[string]string{"InputStreamId": fmt.Sprintf("stream-%03d", i), "ResponseCode": "429"}, to.Ptr(1.0)))
	}
	metric := amwRenderTestMetric("MetricIngestionRequest_Count", raw...)
	series, _ := amwReadRenderSeries(metric, amwRenderStart, amwRenderStart.Add(time.Minute))
	chart, budget := amwRenderChart{Series: series}, 0
	amwPrepareChart(&chart, amwRenderStart, &budget)
	if len(chart.Series) != 0 || len(chart.Rows) != 100 || len(chart.Warnings) != 2 || chart.Rows[0].Usage != "1" {
		t.Fatalf("exhausted visual budget must preserve bounded summary and warn: %+v", chart)
	}
	report := amwReport{Start: amwRenderStart, End: amwRenderStart.Add(time.Minute)}
	for i := 0; i < 32; i++ {
		report.Resources = append(report.Resources, amwResource{Kind: "dcr", Name: fmt.Sprint(i), Metrics: []amwMetric{metric}})
	}
	output, err := renderAMWHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "Chart unavailable") || !strings.Contains(string(output), "Showing 0 of 110") || len(output) > 2000000 {
		t.Fatalf("many DCRs must have bounded output and explicit fallback, size=%d", len(output))
	}
	// Repeated metric responses are ambiguous even if each response is valid.
	report.Resources = report.Resources[:1]
	report.Resources[0].Metrics = append(report.Resources[0].Metrics, metric)
	output, err = renderAMWHTML(report)
	if err != nil || !strings.Contains(string(output), "duplicate metric responses withheld") || strings.Contains(string(output), "stream-000") {
		t.Fatalf("duplicate responses must be withheld, error=%v", err)
	}
	// Input order must not select which duplicate dimension series survives.
	duplicate := amwRenderTestMetric("MetricIngestionRequest_Count", raw[0], raw[1], raw[0])
	a, _ := amwReadRenderSeries(duplicate, amwRenderStart, amwRenderStart.Add(time.Minute))
	slices.Reverse(duplicate.Response.Value[0].Timeseries)
	b, _ := amwReadRenderSeries(duplicate, amwRenderStart, amwRenderStart.Add(time.Minute))
	if len(a) != 1 || len(b) != 1 || a[0].Key != b[0].Key {
		t.Fatalf("duplicate suppression must be order independent: %v, %v", a, b)
	}
}
