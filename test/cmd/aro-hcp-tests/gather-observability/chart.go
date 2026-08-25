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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"

	"k8s.io/apimachinery/pkg/util/sets"
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

func seriesPeakValue(series []parsedSeries) float64 {
	var peak float64
	for _, s := range series {
		for _, d := range s.data {
			if arr, ok := d.Value.([]any); ok && len(arr) == 2 {
				if v, ok := arr[1].(float64); ok && v > peak {
					peak = v
				}
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
	Title            string
	Description      string
	Query            string
	QueryLang        string // "PromQL" or "Azure Monitor", shown in the query footer
	HasData          bool
	Error            string
	Warning          string        // non-fatal notice shown alongside a rendered chart (e.g. partial failures)
	ChartHTML        template.HTML // raw HTML from go-echarts, not escaped
	MinPeakThreshold float64
	ChartType        string
}

// queryFooter returns the human-readable label and body shown in the collapsed
// query footer of a chart, adapting to the query source. For Azure Monitor
// charts the body is a copy-pasteable `az monitor metrics list` command that
// reproduces the plotted data.
func queryFooter(q QuerySpec, resourceID string, tw timing.TimeWindow) (lang, body string) {
	if q.Source == sourceAzureMonitor {
		return "Azure Monitor", azMetricsCommand(q, resourceID, tw)
	}
	return "PromQL", q.Query
}

// azMetricsCommand renders a copy-pasteable `az monitor metrics list` invocation
// that reproduces the data plotted for an azureMonitor chart. A reader can paste
// it into a shell (after `az login`) to pull the same timeseries straight from
// Azure Monitor. One command is emitted per metric so each carries its own
// dimension filter.
func azMetricsCommand(q QuerySpec, resourceID string, tw timing.TimeWindow) string {
	namespace := metricNamespaceForResource[q.Resource]
	start := tw.Start.UTC().Format(time.RFC3339)
	end := tw.End.UTC().Format(time.RFC3339)
	interval := stepToISO8601(q.Step)

	var cmds []string
	for _, m := range q.Metrics {
		var b strings.Builder
		b.WriteString("az monitor metrics list \\\n")
		fmt.Fprintf(&b, "  --resource %s \\\n", shellQuote(resourceID))
		if namespace != "" {
			fmt.Fprintf(&b, "  --namespace %s \\\n", shellQuote(namespace))
		}
		fmt.Fprintf(&b, "  --metrics %s \\\n", shellQuote(m.Name))
		fmt.Fprintf(&b, "  --aggregation %s \\\n", q.Aggregation)
		fmt.Fprintf(&b, "  --interval %s \\\n", interval)
		if filter := buildMetricFilter(m); filter != "" {
			fmt.Fprintf(&b, "  --filter %s \\\n", shellQuote(filter))
		}
		fmt.Fprintf(&b, "  --start-time %s \\\n", start)
		fmt.Fprintf(&b, "  --end-time %s", end)
		cmds = append(cmds, b.String())
	}
	return strings.Join(cmds, "\n\n")
}

// shellQuote wraps a value in double quotes when it contains characters a shell
// would otherwise interpret. Azure $filter expressions embed single quotes
// (e.g. CollectionName eq '*'), so double quoting is used and any embedded
// double quote is escaped.
func shellQuote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t'\"*|&;<>()$`\\") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// renderPanelHTML assembles multiple charts into a single self-contained HTML
// page and returns its bytes.
func renderPanelHTML(data panelPageData) ([]byte, error) {
	tmplContent := mustReadArtifact("metricspanel.html.tmpl")
	tmpl, err := template.New("panel").Parse(string(tmplContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse panel template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute panel template: %w", err)
	}
	return buf.Bytes(), nil
}

// estimateLegendHeight approximates the pixel height needed for the ECharts
// horizontal legend by simulating how entries wrap across rows.
func estimateLegendHeight(labels []string, chartWidth int) int {
	if len(labels) == 0 {
		return minLegendHeight
	}
	currentRowWidth := 0
	rows := 1
	for _, label := range labels {
		entryWidth := len(label)*legendCharWidth + legendEntryPadding
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
// labels. warning carries a non-fatal notice (e.g. partial-failure details)
// that is displayed alongside the chart when it still has data.
func buildChartData(q QuerySpec, resourceID, queryErr, warning string, results []PrometheusResult, tw timing.TimeWindow) chartData {
	lang, body := queryFooter(q, resourceID, tw)
	series := parseResultsToSeries(results)
	if len(series) == 0 {
		return chartData{Title: q.Title, Description: q.Description, Query: body, QueryLang: lang, Error: queryErr, Warning: warning, MinPeakThreshold: q.MinPeakThreshold}
	}
	switch q.ChartType {
	case chartTypeFacetedStackedArea:
		cd := buildFacetedStackedAreaChartData(q, resourceID, series, tw)
		cd.Warning = warning
		return cd
	case chartTypeLine:
		cd := buildLineChartData(q, resourceID, series, tw)
		cd.Warning = warning
		return cd
	default:
		return chartData{Title: q.Title, Description: q.Description, Query: body, QueryLang: lang, Error: fmt.Sprintf("unknown chartType: %q", q.ChartType)}
	}
}

func buildLineChartData(q QuerySpec, resourceID string, series []parsedSeries, tw timing.TimeWindow) chartData {
	for i := range series {
		series[i].data = insertGapMarkers(series[i].data)
	}
	// Sort by label for consistent color assignment across charts
	slices.SortFunc(series, func(a, b parsedSeries) int {
		return cmp.Compare(a.label, b.label)
	})
	subtitle := fmt.Sprintf("Window: %s — %s", tw.Start.UTC().Format(time.RFC3339), tw.End.UTC().Format(time.RFC3339))

	// Build labels: strip label keys that are the same across all series
	commonLabels := findCommonLabels(series)
	for i := range series {
		series[i].label = compactMetricLabel(series[i].metric, commonLabels)
	}

	// Adjust chart height for legend when many series
	seriesLabels := make([]string, len(series))
	for i := range series {
		seriesLabels[i] = series[i].label
	}
	legendHeight := estimateLegendHeight(seriesLabels, defaultChartWidth)
	chartHeight := baseChartHeight + legendHeight

	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle:       q.Title,
			Renderer:        "svg",
			Height:          fmt.Sprintf("%dpx", chartHeight),
			Width:           fmt.Sprintf("%dpx", defaultChartWidth),
			Theme:           "dark",
			BackgroundColor: "#000",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      q.Title,
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
		charts.WithYAxisOpts(func() opts.YAxis {
			axis := opts.YAxis{
				Type:         "value",
				Name:         q.Unit,
				NameLocation: "middle",
				NameGap:      50,
			}
			if q.Unit == "percent" {
				axis.Min = 0
				maxVal := seriesPeakValue(series)
				if maxVal <= 100 {
					axis.Max = 100
				}
			}
			return axis
		}()),
		charts.WithGridOpts(opts.Grid{
			Left:   "80",
			Right:  "40",
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

	lang, body := queryFooter(q, resourceID, tw)
	return chartData{
		Title:            q.Title,
		Description:      q.Description,
		Query:            body,
		QueryLang:        lang,
		HasData:          true,
		ChartHTML:        template.HTML(html), //nolint:gosec // trusted go-echarts output
		MinPeakThreshold: q.MinPeakThreshold,
		ChartType:        q.ChartType,
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

func buildFacetedStackedAreaChartData(q QuerySpec, resourceID string, series []parsedSeries, tw timing.TimeWindow) chartData {
	facets := groupSeriesByFacet(series, q.FacetBy, q.StackBy)
	facetNames := make([]string, 0, len(facets))
	for name := range facets {
		facetNames = append(facetNames, name)
	}
	slices.Sort(facetNames)

	allPhases := collectUniqueStackValues(series, q.StackBy)
	slices.Sort(allPhases)

	legendHeight := estimateLegendHeight(allPhases, defaultChartWidth)

	// ECharts multi-grid layouts require explicit pixel positions and total height — grids don't auto-stack.
	numFacets := len(facetNames)
	titleAreaHeight := 80
	facetSpacing := 60
	facetHeight := 250
	totalHeight := titleAreaHeight + numFacets*(facetHeight+facetSpacing) + legendHeight + legendBottomPadding

	subtitle := fmt.Sprintf("Window: %s — %s", tw.Start.UTC().Format(time.RFC3339), tw.End.UTC().Format(time.RFC3339))

	var grids []opts.Grid
	for i := range facetNames {
		top := titleAreaHeight + i*(facetHeight+facetSpacing)
		grids = append(grids, opts.Grid{
			Top:          fmt.Sprintf("%dpx", top),
			Height:       fmt.Sprintf("%dpx", facetHeight),
			Left:         "80",
			Right:        "40",
			ContainLabel: ptr.To(false),
		})
	}

	line := charts.NewLine()
	line.SetGlobalOptions(
		charts.WithInitializationOpts(opts.Initialization{
			PageTitle:       q.Title,
			Renderer:        "svg",
			Height:          fmt.Sprintf("%dpx", totalHeight),
			Width:           fmt.Sprintf("%dpx", defaultChartWidth),
			Theme:           "dark",
			BackgroundColor: "#000",
		}),
		charts.WithTitleOpts(opts.Title{
			Title:      q.Title,
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
			Type:      "time",
			Min:       tw.Start.UnixMilli(),
			Max:       tw.End.UnixMilli(),
			GridIndex: 0,
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Type:         "value",
			Name:         facetNames[0],
			NameLocation: "middle",
			NameGap:      50,
			GridIndex:    0,
		}),
		charts.WithGridOpts(grids...),
	)

	for i := 1; i < numFacets; i++ {
		line.ExtendXAxis(opts.XAxis{
			Type:      "time",
			Min:       tw.Start.UnixMilli(),
			Max:       tw.End.UnixMilli(),
			GridIndex: i,
		})
		line.ExtendYAxis(opts.YAxis{
			Type:         "value",
			Name:         facetNames[i],
			NameLocation: "middle",
			NameGap:      50,
			GridIndex:    i,
		})
	}

	for facetIdx, facetName := range facetNames {
		facetSeries := alignSeriesToCommonTimestamps(facets[facetName])
		for _, s := range facetSeries {
			color := q.Colors[s.label]
			seriesOpts := []charts.SeriesOpts{
				charts.WithLineChartOpts(opts.LineChart{
					ShowSymbol:   ptr.To(false),
					ConnectNulls: ptr.To(false),
					Stack:        fmt.Sprintf("facet-%d", facetIdx),
					XAxisIndex:   facetIdx,
					YAxisIndex:   facetIdx,
				}),
				charts.WithAreaStyleOpts(opts.AreaStyle{
					Opacity: opts.Float(0.7),
					Color:   color,
				}),
				charts.WithItemStyleOpts(opts.ItemStyle{Color: color}),
				charts.WithLineStyleOpts(opts.LineStyle{Color: color}),
			}
			line.AddSeries(s.label, s.data, seriesOpts...)
		}
	}

	rendered := line.RenderContent()
	html := extractChartBody(rendered)

	lang, body := queryFooter(q, resourceID, tw)
	return chartData{
		Title:       q.Title,
		Description: q.Description,
		Query:       body,
		QueryLang:   lang,
		HasData:     true,
		ChartHTML:   template.HTML(html), //nolint:gosec // trusted go-echarts output
		ChartType:   q.ChartType,
	}
}

func parseResultsToSeries(results []PrometheusResult) []parsedSeries {
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
				Value: []any{ts * 1000, val},
			})
		}
		if len(data) == 0 {
			continue
		}
		series = append(series, parsedSeries{
			metric: result.Metric,
			data:   data,
		})
	}
	return series
}

func groupSeriesByFacet(series []parsedSeries, facetBy, stackBy string) map[string][]parsedSeries {
	facets := make(map[string][]parsedSeries)
	for _, s := range series {
		facetValue := s.metric[facetBy]
		s.label = buildFacetSeriesLabel(s.metric, stackBy)
		facets[facetValue] = append(facets[facetValue], s)
	}
	return facets
}

func buildFacetSeriesLabel(metric map[string]string, stackBy string) string {
	if v := metric[stackBy]; len(v) > 0 {
		return v
	}
	return "value"
}

func collectUniqueStackValues(series []parsedSeries, stackBy string) []string {
	result := sets.New[string]()
	for _, s := range series {
		if v := s.metric[stackBy]; len(v) > 0 {
			result.Insert(v)
		}
	}
	return result.UnsortedList()
}

// alignSeriesToCommonTimestamps ensures all series share the same set of
// timestamps by filling 0 where a series has no data point. This prevents
// stacked area charts from collapsing when some series are absent at certain
// timestamps.
func alignSeriesToCommonTimestamps(series []parsedSeries) []parsedSeries {
	if len(series) <= 1 {
		return series
	}

	tsSet := make(map[int64]struct{})
	for _, s := range series {
		for _, d := range s.data {
			ts := dataPointTimestamp(d)
			if ts != 0 {
				tsSet[ts] = struct{}{}
			}
		}
	}

	timestamps := make([]int64, 0, len(tsSet))
	for ts := range tsSet {
		timestamps = append(timestamps, ts)
	}
	slices.Sort(timestamps)

	for i, s := range series {
		existing := make(map[int64]opts.LineData, len(s.data))
		for _, d := range s.data {
			ts := dataPointTimestamp(d)
			if ts == 0 {
				continue
			}
			if arr, ok := d.Value.([]any); ok && len(arr) >= 2 && arr[1] == nil {
				continue
			}
			existing[ts] = d
		}

		aligned := make([]opts.LineData, 0, len(timestamps))
		for _, ts := range timestamps {
			if d, ok := existing[ts]; ok {
				aligned = append(aligned, d)
			} else {
				aligned = append(aligned, opts.LineData{Value: []any{ts, 0}})
			}
		}
		series[i].data = aligned
	}

	return series
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
