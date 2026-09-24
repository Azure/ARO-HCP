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

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

const amwPlotSeriesLimit = 12

type amwRenderSeries struct {
	Key, Label, Metric string
	Dimensions         map[string]string
	Values             []*float64
	Limit              bool
}

type amwRenderRow struct {
	Label, Usage, Limit, Utilization, Headroom, At string
}

type amwRenderChart struct {
	Title, Unit string
	Options     template.JS
	Capacity    bool
	Series      []amwRenderSeries
	Rows        []amwRenderRow
	Warnings    []string
}

type amwRenderResource struct {
	Name, Kind string
	Open       bool
	Charts     []amwRenderChart
	Drops      []amwRenderChart
	Warnings   []string
}

func renderAMWHTML(report amwReport) ([]byte, error) {
	return renderAMWHTMLWithEvidence(report, "amw.json")
}

func renderAMWHTMLWithEvidence(report amwReport, evidenceURL string) ([]byte, error) {
	if report.Start.IsZero() || report.End.IsZero() || !report.Start.Before(report.End) || report.End.Sub(report.Start) > 24*time.Hour ||
		!report.Start.Equal(report.Start.Truncate(time.Minute)) || !report.End.Equal(report.End.Truncate(time.Minute)) {
		return nil, fmt.Errorf("AMW report requires minute-aligned nonzero Start < End and a window of at most 24 hours")
	}
	page := struct {
		Start, End  string
		EvidenceURL string
		Resources   []amwRenderResource
		Warnings    []string
	}{Start: report.Start.UTC().Format(time.RFC3339), End: report.End.UTC().Format(time.RFC3339), EvidenceURL: evidenceURL, Warnings: slices.Clone(report.Errors)}
	for _, discovery := range report.Discovery {
		if discovery.Error != "" {
			page.Warnings = append(page.Warnings, discovery.SubscriptionID+": "+discovery.Error)
		}
	}
	// Bound chart output across all DCRs as well as within each chart. Tables remain
	// available when this budget is exhausted; raw evidence is never embedded.
	plotBudget := 96
	resources := slices.Clone(report.Resources)
	slices.SortStableFunc(resources, func(a, b amwResource) int {
		if (a.Kind == "workspace") != (b.Kind == "workspace") {
			if a.Kind == "workspace" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	for _, resource := range resources {
		view := amwRenderResource{Name: resource.Name, Kind: resource.Kind, Open: len(page.Resources) == 0 && resource.Kind == "workspace"}
		metrics := map[string][]amwRenderSeries{}
		counts := map[string]int{}
		for _, metric := range resource.Metrics {
			counts[metric.Name]++
		}
		for _, metric := range resource.Metrics {
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
				title := "Active time series"
				if name == "EventsPerMinuteIngested" {
					title = "Events per minute"
				}
				chart := amwRenderChart{Title: title, Unit: unit, Capacity: true}
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
				view.Drops = append(view.Drops, amwRenderChart{Title: name, Unit: unit, Series: metrics[name]})
			}
		case "dcr":
			view.Charts = append(view.Charts, amwRenderChart{Title: "Requests per minute", Unit: "requests / minute (Total)", Series: metrics["MetricIngestionRequest_Count"]})
		default:
			view.Warnings = append(view.Warnings, "Unknown resource kind; see amw.json for evidence")
		}
		for i := range view.Charts {
			amwPrepareChart(&view.Charts[i], report.Start, &plotBudget)
		}
		for i := range view.Drops {
			amwPrepareChart(&view.Drops[i], report.Start, &plotBudget)
		}
		slices.Sort(view.Warnings)
		view.Warnings = slices.Compact(view.Warnings)
		page.Resources = append(page.Resources, view)
	}
	slices.Sort(page.Warnings)
	page.Warnings = slices.Compact(page.Warnings)
	for i, warning := range page.Warnings {
		for _, resource := range resources {
			if resource.ID != "" {
				warning = strings.ReplaceAll(warning, resource.ID, resource.Name)
			}
		}
		page.Warnings[i] = warning
	}
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
	var required []string
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
		if len(dimensions) == 0 {
			series.Label = "Workspace (unsplit)"
		}
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
	// Retain unknown series in the table, but spend visual budgets only on
	// series with numeric observations.
	chart.Series = slices.DeleteFunc(chart.Series, func(series amwRenderSeries) bool {
		return !slices.ContainsFunc(series.Values, func(value *float64) bool { return value != nil })
	})
	count := min(len(chart.Series), amwPlotSeriesLimit, *budget)
	if count < len(chart.Series) {
		chart.Warnings = append(chart.Warnings, fmt.Sprintf("Showing %d of %d visual series (12 per plot, 96 per report maximum); omitted series remain in amw.json", count, len(chart.Series)))
	}
	chart.Series = chart.Series[:count]
	*budget -= count
	if len(chart.Series) == 0 {
		return
	}
	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithTooltipOpts(opts.Tooltip{Trigger: "axis", TriggerOn: "mousemove|click", AxisPointer: &opts.AxisPointer{Type: "line"}}),
		charts.WithLegendOpts(opts.Legend{Show: ptr.To(true), Type: "scroll", Bottom: "0"}),
		charts.WithXAxisOpts(opts.XAxis{Type: "time", Min: start.UnixMilli(), Max: start.Add(time.Duration(len(chart.Series[0].Values)) * time.Minute).UnixMilli()}),
		charts.WithYAxisOpts(opts.YAxis{Type: "value", Min: 0}),
		charts.WithGridOpts(opts.Grid{Left: "65", Right: "20", Top: "20", Bottom: "65"}),
	)
	for _, series := range chart.Series {
		name := series.Label
		if chart.Capacity {
			name = "Usage"
			if series.Limit {
				name = "Limit"
			}
			if series.Label != "Workspace (unsplit)" {
				name += " " + series.Label
			}
		}
		data := make([]opts.LineData, 0, len(series.Values))
		for minute, value := range series.Values {
			data = append(data, opts.LineData{Value: []any{start.Add(time.Duration(minute) * time.Minute).UnixMilli(), value}})
		}
		style := "solid"
		if series.Limit {
			style = "dashed"
		}
		line.AddSeries(name, data, charts.WithLineChartOpts(opts.LineChart{ShowSymbol: ptr.To(false), ConnectNulls: ptr.To(false)}), charts.WithLineStyleOpts(opts.LineStyle{Type: style}))
	}
	line.Validate()
	options := line.JSON()
	options["useUTC"], options["animation"], options["backgroundColor"] = true, false, "transparent"
	// Marshal with HTML escaping before embedding in a script data block. No
	// labels are interpolated into executable JavaScript or tooltip HTML.
	encoded, _ := json.Marshal(options)
	chart.Options = template.JS(encoded) //nolint:gosec // encoding/json escapes script terminators
}
