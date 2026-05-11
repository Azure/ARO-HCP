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
	"cmp"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"

	"k8s.io/utils/ptr"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

const (
	// minLegendHeight is the minimum height for the chart legend area.
	minLegendHeight = 40
	// legendRowHeight is the height of one row of legend entries.
	legendRowHeight = 22
	// baseChartHeight is the base height for timeseries charts before legend.
	baseChartHeight = 400
	// legendBottomPadding is extra space below the legend area.
	legendBottomPadding = 20
	// legendCharWidth is the approximate pixel width per character in legend labels.
	legendCharWidth = 7
	// legendEntryPadding is the approximate pixel width for the legend icon and spacing.
	legendEntryPadding = 40
	// defaultChartWidth is the default chart width in pixels.
	defaultChartWidth = 1200
)

// parsedSeries is a timeseries with parsed data points ready for charting.
type parsedSeries struct {
	label  string
	metric map[string]string
	data   []opts.LineData
}

func (s parsedSeries) peakValue() float64 {
	var peak float64
	for _, d := range s.data {
		if arr, ok := d.Value.([]any); ok && len(arr) == 2 {
			if v, ok := arr[1].(float64); ok && v > peak {
				peak = v
			}
		}
	}
	return peak
}

// panelPageData is the data passed to the metricspanel.html.tmpl template.
type panelPageData struct {
	Title      string
	Charts     []chartData
	TimeWindow struct {
		Start string
		End   string
	}
}

// chartData holds the rendered chart HTML and metadata for a single query
// within a panel.
type chartData struct {
	Title       string
	Description string
	Query       string
	HasData     bool
	Error       string
	ChartHTML   template.HTML // raw HTML from go-echarts, not escaped
}

// renderPanel assembles multiple charts into a single HTML page.
func renderPanel(outputPath string, data panelPageData) error {
	tmplContent := mustReadArtifact("metricspanel.html.tmpl")
	tmpl, err := template.New("panel").Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse panel template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute panel template: %w", err)
	}
	if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	return nil
}

// estimateLegendHeight approximates the pixel height needed for the ECharts
// horizontal legend by simulating how entries wrap across rows.
func estimateLegendHeight(series []parsedSeries, chartWidth int) int {
	if len(series) == 0 {
		return minLegendHeight
	}
	currentRowWidth := 0
	rows := 1
	for _, s := range series {
		entryWidth := len(s.label)*legendCharWidth + legendEntryPadding
		if currentRowWidth+entryWidth > chartWidth && currentRowWidth > 0 {
			rows++
			currentRowWidth = entryWidth
		} else {
			currentRowWidth += entryWidth
		}
	}
	return max(minLegendHeight, rows*legendRowHeight)
}

// buildChartData builds the chart HTML for a single PromQL query result.
// Each PrometheusResult becomes a separate series, labeled by its metric
// labels.
func buildChartData(title, description, query, unit, queryErr string, results []PrometheusResult, tw timing.TimeWindow) chartData {
	var series []parsedSeries
	for _, result := range results {
		if len(result.Values) == 0 {
			continue
		}

		var data []opts.LineData
		for _, v := range result.Values {
			if len(v) < 2 {
				continue
			}
			ts, val, ok := parsePrometheusValue(v)
			if !ok || ts == 0 {
				continue
			}
			data = append(data, opts.LineData{
				Value: []any{ts * 1000, val}, // ECharts time axis expects milliseconds
			})
		}

		if len(data) == 0 {
			continue
		}

		data = insertGapMarkers(data)

		series = append(series, parsedSeries{
			metric: result.Metric,
			data:   data,
		})
	}

	if len(series) == 0 {
		return chartData{Title: title, Description: description, Query: query, Error: queryErr}
	}

	// Sort by peak value descending for consistent legend ordering
	slices.SortFunc(series, func(a, b parsedSeries) int {
		return cmp.Compare(b.peakValue(), a.peakValue())
	})
	subtitle := fmt.Sprintf("Window: %s — %s", tw.Start.UTC().Format(time.RFC3339), tw.End.UTC().Format(time.RFC3339))

	// Build labels: strip label keys that are the same across all series
	commonLabels := findCommonLabels(series)
	for i := range series {
		series[i].label = compactMetricLabel(series[i].metric, commonLabels)
	}

	// Adjust chart height for legend when many series
	legendHeight := estimateLegendHeight(series, defaultChartWidth)
	chartHeight := baseChartHeight + legendHeight

	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle:       title,
			Renderer:        "svg",
			Height:          fmt.Sprintf("%dpx", chartHeight),
			Width:           fmt.Sprintf("%dpx", defaultChartWidth),
			Theme:           "dark",
			BackgroundColor: "#000",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      title,
			Subtitle:   subtitle,
			TitleStyle: &opts.TextStyle{Align: "left", Color: "#4E9AF1", FontSize: 18},
			TextAlign:  "left",
			Left:       "center",
		}),
		charts.WithTooltipOpts(opts.Tooltip{
			Trigger: "axis",
		}),
		charts.WithLegendOpts(opts.Legend{
			Show:   ptr.To(true),
			Bottom: "0",
		}),
		charts.WithXAxisOpts(opts.XAxis{
			Type: "time",
			Min:  tw.Start.UnixMilli(),
			Max:  tw.End.UnixMilli(),
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Type:         "value",
			Name:         unit,
			NameLocation: "middle",
			NameGap:      50,
		}),
		charts.WithGridOpts(opts.Grid{
			Bottom: fmt.Sprintf("%d", legendHeight+legendBottomPadding),
		}),
	)

	for _, s := range series {
		line.AddSeries(s.label, s.data,
			charts.WithLineChartOpts(opts.LineChart{
				ShowSymbol:   ptr.To(false),
				ConnectNulls: ptr.To(false),
			}),
		)
	}

	// Extract just the chart div+script from go-echarts, stripping the outer HTML shell
	rendered := line.RenderContent()
	html := extractChartBody(rendered)

	return chartData{
		Title:       title,
		Description: description,
		Query:       query,
		HasData:     true,
		ChartHTML:   template.HTML(html), //nolint:gosec // trusted go-echarts output
	}
}

// extractChartBody strips the outer HTML/head/body tags from go-echarts output
// and returns just the inner content (chart div, script, style).
func extractChartBody(rendered []byte) []byte {
	// Extract content between <body> and </body>
	start := bytes.Index(rendered, []byte("<body>"))
	end := bytes.Index(rendered, []byte("</body>"))
	if start >= 0 && end > start {
		return rendered[start+len("<body>") : end]
	}
	return rendered
}

// findCommonLabels returns label keys whose values are identical across all series.
func findCommonLabels(series []parsedSeries) map[string]bool {
	if len(series) <= 1 {
		return nil
	}
	common := make(map[string]bool)
	for k, v := range series[0].metric {
		same := true
		for _, s := range series[1:] {
			if s.metric[k] != v {
				same = false
				break
			}
		}
		if same {
			common[k] = true
		}
	}
	return common
}

// compactMetricLabel builds a short label showing only the label keys that
// differ across series. If only one differentiating key exists, shows just
// the value.
func compactMetricLabel(metric map[string]string, common map[string]bool) string {
	var keys []string
	for k := range metric {
		if !common[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)

	if len(keys) == 0 {
		// all labels are common — fall back to full label
		return metricLabel(metric)
	}
	if len(keys) == 1 {
		return metric[keys[0]]
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, metric[k]))
	}
	return strings.Join(parts, ", ")
}

// parsePrometheusValue extracts a unix timestamp and float value from a
// Prometheus [timestamp, "value"] pair. Returns ok=false for NaN values
// which cannot be serialized to JSON. Inf values are capped to a large
// finite number so they can be displayed on charts.
func parsePrometheusValue(v []any) (ts int64, val float64, ok bool) {
	switch t := v[0].(type) {
	case float64:
		ts = int64(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			ts = n
		}
	}

	switch s := v[1].(type) {
	case string:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			val = f
		}
	case float64:
		val = s
	}

	if math.IsNaN(val) {
		return ts, 0, false
	}
	if math.IsInf(val, 1) {
		val = math.MaxFloat64
	} else if math.IsInf(val, -1) {
		val = -math.MaxFloat64
	}
	return ts, val, true
}

// insertGapMarkers inserts null data points where the time between consecutive
// points is much larger than the typical interval, causing ECharts to break
// the line instead of drawing a misleading straight line across the gap.
// The typical interval is inferred as the minimum gap between consecutive points.
func insertGapMarkers(data []opts.LineData) []opts.LineData {
	if len(data) < 3 {
		return data
	}
	var minGap int64 = math.MaxInt64
	for i := 1; i < len(data); i++ {
		gap := dataPointTimestamp(data[i]) - dataPointTimestamp(data[i-1])
		if gap > 0 && gap < minGap {
			minGap = gap
		}
	}
	if minGap == math.MaxInt64 {
		return data
	}
	threshold := 3 * minGap
	var result []opts.LineData
	result = append(result, data[0])
	for i := 1; i < len(data); i++ {
		gap := dataPointTimestamp(data[i]) - dataPointTimestamp(data[i-1])
		if gap > threshold {
			midpoint := (dataPointTimestamp(data[i-1]) + dataPointTimestamp(data[i])) / 2
			result = append(result, opts.LineData{Value: []any{midpoint, nil}})
		}
		result = append(result, data[i])
	}
	return result
}

func dataPointTimestamp(d opts.LineData) int64 {
	if arr, ok := d.Value.([]any); ok && len(arr) >= 1 {
		switch v := arr[0].(type) {
		case int64:
			return v
		case float64:
			return int64(v)
		}
	}
	return 0
}

// metricLabel builds a display label from Prometheus metric labels.
func metricLabel(metric map[string]string) string {
	if len(metric) == 0 {
		return "value"
	}
	keys := make([]string, 0, len(metric))
	for k := range metric {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, metric[k]))
	}
	return strings.Join(parts, ", ")
}
