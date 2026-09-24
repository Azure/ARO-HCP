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
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

const amwPlotSeriesLimit = 12

type amwRenderSeries struct {
	Key, Label, Metric, Path, Color string
	Dimensions                      map[string]string
	Values                          []*float64
	Limit                           bool
}

type amwRenderRow struct {
	Label, Usage, Limit, Utilization, Headroom, At string
}

type amwRenderChart struct {
	Title, Unit, Maximum string
	Start, End           string
	Capacity             bool
	Series               []amwRenderSeries
	Rows                 []amwRenderRow
	Warnings             []string
}

type amwRenderResource struct {
	ID, Name, Kind string
	Workspaces     []string
	Charts         []amwRenderChart
	Warnings       []string
	Metadata       []string
}

func renderAMWHTML(report amwReport) ([]byte, error) {
	if report.Start.IsZero() || report.End.IsZero() || !report.Start.Before(report.End) || report.End.Sub(report.Start) > 24*time.Hour ||
		!report.Start.Equal(report.Start.Truncate(time.Minute)) || !report.End.Equal(report.End.Truncate(time.Minute)) {
		return nil, fmt.Errorf("AMW report requires minute-aligned nonzero Start < End and a window of at most 24 hours")
	}
	page := struct {
		Start, End string
		Resources  []amwRenderResource
		Warnings   []string
	}{Start: report.Start.UTC().Format(time.RFC3339), End: report.End.UTC().Format(time.RFC3339), Warnings: slices.Clone(report.Errors)}
	for _, discovery := range report.Discovery {
		if discovery.Error != "" {
			page.Warnings = append(page.Warnings, discovery.SubscriptionID+": "+discovery.Error)
		}
	}
	// Bound SVG output across all DCRs as well as within each chart. Tables remain
	// available when this budget is exhausted; raw evidence is never embedded.
	plotBudget := 96
	for _, resource := range report.Resources {
		view := amwRenderResource{ID: resource.ID, Name: resource.Name, Kind: resource.Kind, Workspaces: resource.Workspaces}
		metrics := map[string][]amwRenderSeries{}
		counts := map[string]int{}
		for _, metric := range resource.Metrics {
			counts[metric.Name]++
		}
		for _, metric := range resource.Metrics {
			interval, span := "unknown", "unknown"
			if metric.Response != nil {
				interval = ptr.Deref(metric.Response.Interval, "unknown")
				span = ptr.Deref(metric.Response.Timespan, "unknown")
			}
			view.Metadata = append(view.Metadata, fmt.Sprintf("%s | aggregation=%s | interval=%s | timespan=%s", metric.Name, metric.Aggregation, interval, span))
			if metric.Error != "" {
				view.Warnings = append(view.Warnings, metric.Name+": "+metric.Error)
			}
			if counts[metric.Name] != 1 {
				view.Warnings = append(view.Warnings, metric.Name+": duplicate metric responses withheld")
				continue
			}
			series, warnings := amwReadRenderSeries(metric, report.Start, report.End)
			metrics[metric.Name] = series
			for _, warning := range warnings {
				view.Warnings = append(view.Warnings, metric.Name+": "+warning)
			}
		}
		switch resource.Kind {
		case "workspace":
			for _, name := range []string{"ActiveTimeSeries", "EventsPerMinuteIngested"} {
				unit := "active time series (Maximum)"
				if name == "EventsPerMinuteIngested" {
					unit = "events / minute (Maximum)"
				}
				chart := amwRenderChart{Title: name + " + limit", Unit: unit, Capacity: true}
				chart.Series = append(chart.Series, metrics[name]...)
				chart.Series = append(chart.Series, metrics[name+"Limit"]...)
				chart.Rows = amwCapacityRows(metrics[name], metrics[name+"Limit"], report.Start)
				view.Charts = append(view.Charts, chart)
			}
			for _, name := range []string{"EventsDropped", "TimeSeriesSamplesDropped"} {
				unit := "events / minute (Maximum)"
				if name == "TimeSeriesSamplesDropped" {
					unit = "samples / minute (Maximum)"
				}
				for _, throttled := range []bool{true, false} {
					title := name + " | other reasons"
					if throttled {
						title = name + " | Reason=LimitThrottling"
					}
					chart := amwRenderChart{Title: title, Unit: unit}
					for _, series := range metrics[name] {
						if (series.Dimensions["reason"] == "LimitThrottling") == throttled {
							chart.Series = append(chart.Series, series)
						}
					}
					view.Charts = append(view.Charts, chart)
				}
			}
		case "dcr":
			for _, only429 := range []bool{false, true} {
				title := "MetricIngestionRequest_Count | all response codes"
				if only429 {
					title = "MetricIngestionRequest_Count | ResponseCode=429"
				}
				chart := amwRenderChart{Title: title, Unit: "requests / minute (Total), split by InputStreamId + ResponseCode"}
				for _, series := range metrics["MetricIngestionRequest_Count"] {
					if !only429 || series.Dimensions["responsecode"] == "429" {
						chart.Series = append(chart.Series, series)
					}
				}
				view.Charts = append(view.Charts, chart)
			}
		default:
			view.Warnings = append(view.Warnings, "Unknown resource kind; see amw.json for evidence")
		}
		for i := range view.Charts {
			amwPrepareChart(&view.Charts[i], report.Start, &plotBudget)
		}
		slices.Sort(view.Warnings)
		view.Warnings = slices.Compact(view.Warnings)
		page.Resources = append(page.Resources, view)
	}
	slices.Sort(page.Warnings)
	page.Warnings = slices.Compact(page.Warnings)
	tmpl, err := template.New("amw.html.tmpl").ParseFS(templatesFS, "artifacts/amw.html.tmpl")
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func amwReadRenderSeries(metric amwMetric, start, end time.Time) ([]amwRenderSeries, []string) {
	warnings := map[string]bool{}
	warn := func(message string) { warnings[message] = true }
	if metric.Response == nil || ptr.Deref(metric.Response.Interval, "") != "PT1M" {
		return nil, []string{"missing response or interval is not PT1M; data withheld"}
	}
	var matches []*armmonitor.Metric
	for _, value := range metric.Response.Value {
		if value != nil && value.Name != nil && ptr.Deref(value.Name.Value, "") == metric.Name {
			matches = append(matches, value)
		}
	}
	if len(matches) != 1 {
		return nil, []string{"requested metric must occur exactly once; data withheld"}
	}
	value := matches[0]
	if code := ptr.Deref(value.ErrorCode, ""); code != "" && !strings.EqualFold(code, "Success") {
		return nil, []string{"Azure metric errorCode is not Success; data withheld (see amw.json)"}
	}
	if ptr.Deref(value.ErrorMessage, "") != "" {
		warn("Azure metric error message present; partial data shown (see amw.json)")
	}
	required := []string{"stampcolor"}
	aggregation := "Maximum"
	if strings.HasSuffix(metric.Name, "Dropped") {
		required = append(required, "reason")
	}
	if metric.Name == "MetricIngestionRequest_Count" {
		required, aggregation = []string{"inputstreamid", "responsecode"}, "Total"
	}
	if metric.Aggregation != aggregation {
		return nil, []string{"unexpected aggregation; data withheld"}
	}
	seriesByKey := map[string]amwRenderSeries{}
	duplicates := map[string]bool{}
	for _, raw := range value.Timeseries {
		if raw == nil {
			warn("nil timeseries withheld")
			continue
		}
		dimensions := map[string]string{}
		var labels []string
		var keys [][2]string
		valid := true
		for _, metadata := range raw.Metadatavalues {
			if metadata == nil || metadata.Name == nil || ptr.Deref(metadata.Name.Value, "") == "" || ptr.Deref(metadata.Value, "") == "" {
				valid = false
				continue
			}
			name, val := *metadata.Name.Value, *metadata.Value
			key := strings.ToLower(name)
			if _, exists := dimensions[key]; exists {
				valid = false
			}
			dimensions[key] = val
			keys = append(keys, [2]string{key, val})
			labels = append(labels, name+"="+strconv.Quote(val))
		}
		for _, key := range required {
			if dimensions[key] == "" {
				valid = false
			}
		}
		if !valid {
			warn("missing or duplicate dimensions; ambiguous series withheld")
			continue
		}
		slices.Sort(labels)
		slices.SortFunc(keys, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
		encoded, _ := json.Marshal(keys)
		key := string(encoded)
		if _, exists := seriesByKey[key]; exists {
			duplicates[key] = true
			warn("duplicate dimension sets; ambiguous series withheld")
			continue
		}
		series := amwRenderSeries{Key: key, Label: strings.Join(labels, ", "), Metric: metric.Name, Dimensions: dimensions,
			Values: make([]*float64, int(end.Sub(start)/time.Minute)), Limit: strings.HasSuffix(metric.Name, "Limit")}
		seen := map[int]bool{}
		for _, point := range raw.Data {
			if point == nil || point.TimeStamp == nil {
				warn("missing timestamps ignored")
				continue
			}
			timestamp := *point.TimeStamp
			if !timestamp.Equal(timestamp.Truncate(time.Minute)) {
				warn("off-minute timestamps ignored")
				continue
			}
			if timestamp.Before(start) || !timestamp.Before(end) {
				continue
			}
			index := int(timestamp.Sub(start) / time.Minute)
			if seen[index] {
				series.Values[index] = nil
				warn("duplicate timestamps are unknown, not zero")
				continue
			}
			seen[index] = true
			number := point.Maximum
			if aggregation == "Total" {
				number = point.Total
			}
			if number == nil {
				continue
			}
			if math.IsNaN(*number) || math.IsInf(*number, 0) || *number < 0 {
				warn("non-finite or negative samples are unknown, not zero")
				continue
			}
			series.Values[index] = number
		}
		seriesByKey[key] = series
	}
	var result []amwRenderSeries
	for key, series := range seriesByKey {
		if !duplicates[key] {
			result = append(result, series)
		}
	}
	slices.SortFunc(result, func(a, b amwRenderSeries) int { return strings.Compare(a.Key, b.Key) })
	var messages []string
	for message := range warnings {
		messages = append(messages, message)
	}
	slices.Sort(messages)
	return result, messages
}

func amwCapacityRows(usage, limits []amwRenderSeries, start time.Time) []amwRenderRow {
	byKey := map[string]amwRenderSeries{}
	all := map[string]amwRenderSeries{}
	for _, series := range limits {
		byKey[series.Key], all[series.Key] = series, series
	}
	for _, series := range usage {
		all[series.Key] = series
	}
	rows := map[string]amwRenderRow{}
	for key, series := range all {
		rows[key] = amwRenderRow{Label: series.Label, Usage: "unknown", Limit: "unknown", Utilization: "unknown", Headroom: "unknown", At: "unknown"}
	}
	for _, series := range usage {
		limit, exists := byKey[series.Key]
		if !exists {
			continue
		}
		for i := len(series.Values) - 1; i >= 0; i-- {
			u, l := series.Values[i], limit.Values[i]
			if u == nil || l == nil {
				continue
			}
			row := rows[series.Key]
			row.Usage, row.Limit, row.Headroom = amwNumber(*u), amwNumber(*l), amwNumber(*l-*u)
			row.At = start.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)
			if *l > 0 {
				percentage := *u / *l * 100
				if !math.IsInf(percentage, 0) && !math.IsNaN(percentage) {
					row.Utilization = amwNumber(percentage) + "%"
				}
			}
			rows[series.Key] = row
			break
		}
	}
	var result []amwRenderRow
	for _, row := range rows {
		result = append(result, row)
	}
	slices.SortFunc(result, func(a, b amwRenderRow) int { return strings.Compare(a.Label, b.Label) })
	return result
}

func amwNumber(number float64) string { return strconv.FormatFloat(number, 'g', 6, 64) }

func amwPrepareChart(chart *amwRenderChart, start time.Time, budget *int) {
	chart.Start = start.UTC().Format("15:04")
	if len(chart.Series) > 0 {
		chart.End = start.Add(time.Duration(len(chart.Series[0].Values)) * time.Minute).UTC().Format("15:04")
	}
	if !chart.Capacity {
		for _, series := range chart.Series {
			row := amwRenderRow{Label: series.Label, Usage: "unknown", At: "unknown"}
			for i := len(series.Values) - 1; i >= 0; i-- {
				if value := series.Values[i]; value != nil {
					row.Usage, row.At = amwNumber(*value), start.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339)
					break
				}
			}
			chart.Rows = append(chart.Rows, row)
		}
	}
	if len(chart.Rows) > 100 {
		chart.Warnings = append(chart.Warnings, fmt.Sprintf("Summary limited to 100 of %d dimension sets; all labels and raw series remain in amw.json", len(chart.Rows)))
		chart.Rows = chart.Rows[:100]
	}
	count := min(len(chart.Series), amwPlotSeriesLimit, *budget)
	if count < len(chart.Series) {
		chart.Warnings = append(chart.Warnings, fmt.Sprintf("Showing %d of %d visual series (12 per plot, 96 per report maximum); omitted series remain in amw.json", count, len(chart.Series)))
	}
	chart.Series = chart.Series[:count]
	*budget -= count
	maximum := 0.0
	for _, series := range chart.Series {
		for _, value := range series.Values {
			if value != nil {
				maximum = max(maximum, *value)
			}
		}
	}
	if maximum == 0 {
		maximum = 1
	}
	chart.Maximum = amwNumber(maximum)
	colors := []string{"#4E9AF1", "#80cbc4", "#FFB300", "#ce93d8", "#ef9a9a", "#c5e1a5"}
	for i := range chart.Series {
		series := &chart.Series[i]
		series.Color = colors[i%len(colors)]
		var path strings.Builder
		connected := false
		for minute, value := range series.Values {
			if value == nil {
				connected = false
				continue
			}
			x, y := 64+680*float64(minute)/float64(len(series.Values)), 190-170*(*value/maximum)
			if connected {
				fmt.Fprintf(&path, "L%.2f %.2f", x, y)
			} else {
				// A zero-length stroke makes isolated observations visible without
				// connecting across missing minutes or inventing zero samples.
				fmt.Fprintf(&path, "M%.2f %.2fl0 0", x, y)
			}
			connected = true
		}
		series.Path = path.String()
	}
	chart.Series = slices.DeleteFunc(chart.Series, func(series amwRenderSeries) bool { return series.Path == "" })
}
