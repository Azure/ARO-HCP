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

package amwusage

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*
var renderTemplates embed.FS

// Render writes a standalone report from a schemaVersion 1 evidence archive.
// Analysis, fallback tables and SVG charts are generated offline in Go. Optional
// CDN-hosted ECharts adds a treemap; no evidence is fetched by the browser.
// Failed or missing query evidence is displayed as unavailable, not as zero.
func Render(w io.Writer, data []byte) error {
	archive, err := decodeArtifact(data)
	if err != nil {
		return err
	}
	// Marshal the original JSON, not the typed view: preserve unknown fields and
	// escape HTML-sensitive characters. Even this JSON is ordinary template text.
	evidence, err := json.MarshalIndent(json.RawMessage(data), "", "  ")
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	funcs := template.FuncMap{
		"number": renderNumber,
		"signed": func(v *float64) string {
			if v != nil && *v > 0 {
				return "+" + renderNumber(v)
			}
			return renderNumber(v)
		},
		"utc":  renderUTC,
		"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) },
		"join": strings.Join,
		"collectionState": func(complete *bool) string {
			if complete == nil {
				return "Not recorded in archive"
			}
			if !*complete {
				return "Incomplete checkpoint; collection may have stopped before all requests were attempted"
			}
			return "Complete; query failures, if any, remain evidence gaps"
		},
		"label": func(m map[string]string, k string) string {
			if v, ok := m[k]; ok && v != "" {
				return v
			}
			return "(absent)"
		},
		"percent": func(v *float64) string {
			if v == nil {
				return "Unavailable"
			}
			return fmt.Sprintf("%.2f%%", *v*100)
		},
		"safeURL": func(s string) string {
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				return ""
			}
			return s
		},
	}
	t, err := template.New("report.html").Funcs(funcs).ParseFS(renderTemplates, "templates/*")
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	summary := analyzeArtifact(archive)
	contributions := analyzeContributions(archive, summary)
	active := analyzeActive(archive, summary)
	activeJSON, err := json.Marshal(active)
	if err != nil {
		return fmt.Errorf("encode active inventories: %w", err)
	}
	contributionJSON, err := json.Marshal(contributions)
	if err != nil {
		return fmt.Errorf("encode contributions: %w", err)
	}
	view := struct {
		Data               *artifactData
		Summary            renderAnalysis
		Evidence           string
		Duration, Trailing float64
		Windows            int
		Contributions      contributionAnalysis
		ContributionJSON   template.JS
		Active             activeAnalysis
		ActiveJSON         template.JS
	}{archive, summary, string(evidence), archive.Run.End - archive.Run.Start,
		math.Mod(archive.Run.End-archive.Run.Start, 300), int((archive.Run.End - archive.Run.Start) / 300), contributions,
		// Only encoding/json output may enter this trusted script context. Its default
		// HTML escaping protects script terminators, ampersands and U+2028/U+2029.
		template.JS(contributionJSON), active, template.JS(activeJSON)}
	if err := t.ExecuteTemplate(w, "report.html", view); err != nil {
		return fmt.Errorf("render AMW report: %w", err)
	}
	return nil
}

func decodeArtifact(data []byte) (*artifactData, error) {
	var a artifactData
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("decode AMW evidence: %w", err)
	}
	if a.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported AMW schemaVersion %d (want 1)", a.SchemaVersion)
	}
	if a.Run == nil || a.Workspaces == nil || a.Manifests == nil || a.Records == nil {
		return nil, fmt.Errorf("AMW evidence requires run, workspaces, manifests and records")
	}
	var fields struct{ Run map[string]json.RawMessage }
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, key := range []string{"start", "end", "platformStart", "platformEnd"} {
		if len(fields.Run[key]) == 0 || string(fields.Run[key]) == "null" {
			return nil, fmt.Errorf("AMW run requires %s", key)
		}
	}
	r := a.Run
	// Bound timeline allocation and avoid integer overflow on corrupt archives.
	if r.Start < 0 || r.End > 253402300799 || r.PlatformStart < 0 || r.PlatformEnd > 253402300799 || r.End <= r.Start || r.PlatformEnd <= r.PlatformStart ||
		r.End-r.Start > 300*1000000 || r.PlatformEnd-r.PlatformStart > 60*1000000 ||
		(r.PlatformEnd-r.PlatformStart)%60 != 0 {
		return nil, fmt.Errorf("invalid AMW run windows (positive duration, whole platform minutes, at most 1000000 evaluations required)")
	}
	return &a, nil
}

func finiteValue(raw json.RawMessage) *float64 {
	s := string(raw)
	if strings.HasPrefix(s, `"`) {
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func finiteSum(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func decodeResponse(record artifactRecord, target any) string {
	if !record.OK {
		if len(record.Error) > 0 && string(record.Error) != "null" {
			return "Query failed: " + string(record.Error)
		}
		return "Query failed or record missing"
	}
	if err := json.Unmarshal([]byte(record.Body), target); err != nil {
		return "Malformed response: " + err.Error()
	}
	return ""
}

func analyzeMetric(metric artifactMetric, records map[string]artifactRecord, run *artifactRun) metricAnalysis {
	m := metricAnalysis{artifactMetric: metric, Expected: int((run.End - run.Start) / 300), TrailingSeconds: math.Mod(run.End-run.Start, 300)}
	var instant, ranged promResponse
	m.InstantState = decodeResponse(records[metric.Instant], &instant)
	m.RangeState = decodeResponse(records[metric.Range], &ranged)
	m.InstantOK = m.InstantState == "" && instant.Status == "success" && instant.Data.Result != nil
	m.RangeOK = m.RangeState == "" && ranged.Status == "success" && ranged.Data.Result != nil
	if !m.InstantOK && m.InstantState == "" {
		m.InstantState = "Unsuccessful or malformed Prometheus response"
	}
	if !m.RangeOK && m.RangeState == "" {
		m.RangeState = "Unsuccessful or malformed Prometheus response"
	}
	m.Warnings = append(instant.Warnings, ranged.Warnings...)
	if m.InstantOK {
		total := 0.0
		for _, series := range instant.Data.Result {
			var count *float64
			if len(series.Value) == 2 {
				count = finiteValue(series.Value[1])
			}
			if count == nil {
				m.InvalidCardinalityGroups++
			} else {
				total += *count
			}
			m.Groups = append(m.Groups, renderGroup{series.Metric, count})
		}
		if len(m.Groups) > 0 && m.InvalidCardinalityGroups == 0 {
			m.Series = finiteSum(total)
		}
		sort.SliceStable(m.Groups, func(i, j int) bool {
			if m.Groups[i].Count == nil {
				return false
			}
			return m.Groups[j].Count == nil || *m.Groups[i].Count > *m.Groups[j].Count
		})
	}
	sums := make([]float64, m.Expected)
	groups := make([]int, m.Expected)
	invalid := make([]bool, m.Expected)
	if m.RangeOK {
		for _, series := range ranged.Data.Result {
			seen := map[int]bool{}
			for _, point := range series.Values {
				var timestamp *float64
				if len(point) > 0 {
					timestamp = finiteValue(point[0])
				}
				if timestamp == nil || *timestamp <= run.Start || *timestamp > run.Start+float64(m.Expected)*300 || math.Mod(*timestamp-run.Start, 300) != 0 {
					m.OutOfRangePoints++
					continue
				}
				i := int((*timestamp-float64(run.Start))/300) - 1
				if seen[i] {
					m.DuplicatePoints++
					invalid[i] = true
				}
				seen[i] = true
				var value *float64
				if len(point) == 2 {
					value = finiteValue(point[1])
				}
				// Explicitly invalid groups poison the total, independent of group order.
				// Absent groups do not impute a zero or invalidate returned groups.
				if value == nil {
					m.InvalidPoints++
					invalid[i] = true
					continue
				}
				sums[i] += *value
				groups[i]++
				if finiteSum(sums[i]) == nil {
					invalid[i] = true
				}
			}
		}
	}
	observed := 0.0
	for i := range m.Expected {
		p := renderPoint{Time: run.Start + float64(i+1)*300}
		if invalid[i] {
			m.InvalidEvaluations++
		}
		if groups[i] > 0 && !invalid[i] {
			p.Value = finiteSum(sums[i])
			m.Valid++
			observed += sums[i] * 5
			if m.PeakSamplesPerMinute == nil || sums[i] > *m.PeakSamplesPerMinute {
				m.PeakSamplesPerMinute = p.Value
			}
		}
		m.Values = append(m.Values, p)
	}
	if m.Valid > 0 {
		m.ObservedSamples = finiteSum(observed)
	}
	m.Chart = makeChart("Observed stored samples per minute / five-minute windows", []chartInput{{"Sum of returned groups", "#006f79", false, m.Values}}, run.Start, run.End, run)
	return m
}

func analyzePlatform(id string, records map[string]artifactRecord, run *artifactRun) platformAnalysis {
	p := platformAnalysis{ID: id}
	var body platformResponse
	p.State = decodeResponse(records[id], &body)
	if p.State != "" {
		return p
	}
	if len(body.Value) != 1 || (body.Value[0].ErrorCode != "" && body.Value[0].ErrorCode != "Success") {
		p.State = "Unsuccessful or ambiguous platform response"
		return p
	}
	for _, source := range body.Value[0].Timeseries {
		s := platformSeriesAnalysis{Labels: map[string]string{}, Expected: int((run.PlatformEnd - run.PlatformStart) / 60), Rows: len(source.Data), Interval: body.Interval}
		for _, label := range source.MetadataValues {
			s.Labels[label.Name.Value] = label.Value
		}
		points := map[int64]*float64{}
		for _, point := range source.Data {
			t, err := time.Parse(time.RFC3339Nano, point.Timestamp)
			if err != nil || t.Nanosecond() != 0 || t.Unix() < run.PlatformStart || t.Unix() >= run.PlatformEnd || (t.Unix()-run.PlatformStart)%60 != 0 {
				s.OutOfRangePoints++
				continue
			}
			if _, exists := points[t.Unix()]; exists {
				s.DuplicateTimestamps++
				points[t.Unix()] = nil
				continue
			}
			points[t.Unix()] = finiteValue(point.Maximum)
		}
		for i := range s.Expected {
			t := run.PlatformStart + int64(i)*60
			v := points[t]
			s.Values = append(s.Values, renderPoint{float64(t), v})
			if v == nil {
				continue
			}
			s.Valid++
			if s.First == nil {
				s.First = v
			}
			s.Last = v
			if s.Max == nil || *v > *s.Max {
				s.Max = v
			}
		}
		s.Missing = s.Expected - s.Valid
		p.Series = append(p.Series, s)
	}
	if len(p.Series) == 0 {
		p.State = "No series returned"
	}
	return p
}

func completePlatform(s platformSeriesAnalysis) bool {
	return s.Expected > 0 && s.Missing == 0 && s.DuplicateTimestamps == 0 && s.OutOfRangePoints == 0 && s.Interval == "PT1M"
}

func analyzeArtifact(a *artifactData) renderAnalysis {
	var summary renderAnalysis
	for _, source := range a.Workspaces {
		w := workspaceAnalysis{artifactWorkspace: source, Platform: map[string]platformAnalysis{}, SelectedPercent: "Unavailable"}
		if source.Discovery != "" {
			var discovery struct {
				Status string   `json:"status"`
				Data   []string `json:"data"`
			}
			w.DiscoveryState = decodeResponse(a.Records[source.Discovery], &discovery)
			if w.DiscoveryState == "" && (discovery.Status != "success" || discovery.Data == nil) {
				w.DiscoveryState = "Unsuccessful or malformed discovery response"
			}
		}
		if len(source.Names) > 0 {
			w.SelectedPercent = fmt.Sprintf("%.2f%%", 100*float64(len(source.Metrics))/float64(len(source.Names)))
		}
		for _, name := range []string{"ActiveTimeSeries", "ActiveTimeSeriesLimit", "EventsPerMinuteIngested", "EventsPerMinuteIngestedLimit", "EventsDropped", "TimeSeriesSamplesDropped"} {
			w.Platform[name] = analyzePlatform(source.Platform[name], a.Records, a.Run)
		}
		quotaComplete, crossing := true, false
		for _, pair := range [][2]string{{"ActiveTimeSeries", "ActiveTimeSeriesLimit"}, {"EventsPerMinuteIngested", "EventsPerMinuteIngestedLimit"}} {
			q := quotaAnalysis{Usage: pair[0], Limit: pair[1]}
			u, l := w.Platform[q.Usage].Series, w.Platform[q.Limit].Series
			if len(u) == 1 && len(l) == 1 {
				q.Complete = completePlatform(u[0]) && completePlatform(l[0])
				for i, p := range u[0].Values {
					lp := l[0].Values[i]
					if p.Value == nil || lp.Value == nil || *lp.Value <= 0 || *p.Value < 0 {
						q.Complete = false
						continue
					}
					ratio := *p.Value / *lp.Value
					if finiteSum(ratio) == nil {
						q.Complete = false
						continue
					}
					q.Paired++
					if q.MaxRatio == nil || ratio > *q.MaxRatio {
						q.MaxRatio = finiteSum(ratio)
					}
					if ratio >= 1 {
						q.Crossings++
					}
				}
			}
			var lines []chartInput
			for _, item := range []struct {
				name, color string
				dashed      bool
				series      []platformSeriesAnalysis
			}{{"Usage", "#006f79", false, u}, {"Limit", "#b45309", true, l}} {
				for i, s := range item.series {
					name := item.name + " (1-minute maximum)"
					if len(item.series) > 1 {
						labels, _ := json.Marshal(s.Labels)
						name += fmt.Sprintf(" series %d %s", i+1, labels)
					}
					lines = append(lines, chartInput{name, item.color, item.dashed, s.Values})
				}
			}
			q.Chart = makeChart(q.Usage, lines, float64(a.Run.PlatformStart), float64(a.Run.PlatformEnd-60), a.Run)
			w.Quota = append(w.Quota, q)
			quotaComplete = quotaComplete && q.Complete
			crossing = crossing || q.Crossings > 0
		}
		limitDrop, reasonsComplete := false, true
		for _, name := range []string{"EventsDropped", "TimeSeriesSamplesDropped"} {
			found := false
			for _, s := range w.Platform[name].Series {
				reason := strings.ToLower(s.Labels["Reason"])
				if !strings.Contains(reason, "limit") && !strings.Contains(reason, "throttl") {
					continue
				}
				found = true
				reasonsComplete = reasonsComplete && completePlatform(s)
				for _, p := range s.Values {
					if p.Value != nil && *p.Value > 0 {
						limitDrop = true
					}
					if p.Value != nil && *p.Value < 0 {
						reasonsComplete = false
					}
				}
			}
			reasonsComplete = reasonsComplete && found
		}
		w.NoLimitThrottling = !limitDrop && !crossing && quotaComplete && reasonsComplete
		switch {
		case crossing:
			w.Verdict = "Quota crossing observed in paired minute maxima."
		case !quotaComplete:
			w.Verdict = "Quota coverage is incomplete or not comparable; crossings cannot be ruled out."
		default:
			w.Verdict = "No quota crossing observed with complete paired quota coverage."
		}
		if crossing && !quotaComplete {
			w.Verdict += " Other quota coverage is incomplete or not comparable."
		}
		switch {
		case limitDrop:
			w.Verdict += " Limit-throttling drops observed."
		case w.NoLimitThrottling:
			w.Verdict += " No limit throttling observed with complete quota and drop-reason coverage."
		default:
			w.Verdict += " No-limit-throttling conclusion withheld."
		}
		if !reasonsComplete {
			w.Verdict += " Drop-reason coverage is incomplete."
		}
		for _, metric := range source.Metrics {
			m := analyzeMetric(metric, a.Records, a.Run)
			w.MetricResults = append(w.MetricResults, m)
			if m.InstantOK && m.RangeOK {
				w.Succeeded++
			}
		}
		summary.Workspaces = append(summary.Workspaces, w)
	}
	summary.Requests = len(a.Manifests)
	for _, m := range a.Manifests {
		if !m.OK {
			summary.Errors++
		}
		if m.Request.Kind == "promql" || m.Request.Kind == "inventory" {
			summary.PromQL++
			if !m.OK {
				summary.PromQLErrors++
			}
		}
		if m.Request.Kind == "platform" {
			summary.PlatformRequests++
			if !m.OK {
				summary.PlatformErrors++
			}
		}
		summary.ElapsedMS += m.ElapsedMS
		if m.ResponseBytes != nil {
			summary.ResponseBytes += *m.ResponseBytes
			summary.ByteReports++
		}
		if m.APICost != nil {
			summary.APICost += *m.APICost
			summary.CostReports++
		}
		if m.StartedAt != "" && (summary.FirstRequest == "" || m.StartedAt < summary.FirstRequest) {
			summary.FirstRequest = m.StartedAt
		}
		if m.FinishedAt > summary.LastRequest {
			summary.LastRequest = m.FinishedAt
		}
	}
	return summary
}

type chartInput struct {
	name, color string
	dashed      bool
	values      []renderPoint
}

func makeChart(title string, series []chartInput, start, end float64, run *artifactRun) renderChart {
	c := renderChart{Title: title}
	low, high := 0.0, 1.0
	for _, s := range series {
		for _, p := range s.values {
			if p.Value == nil {
				continue
			}
			c.HasData = true
			low = math.Min(low, *p.Value)
			high = math.Max(high, *p.Value)
		}
	}
	// Normalize before subtracting to avoid overflow on extreme finite inputs.
	scale := math.Max(math.Abs(low), high)
	lo, hi := low/scale, high/scale
	x := func(t float64) string {
		if end <= start {
			return "399.50"
		}
		return fmt.Sprintf("%.2f", 62+675*float64(t-start)/float64(end-start))
	}
	y := func(v float64) string { return fmt.Sprintf("%.2f", 182-156*(v/scale-lo)/(hi-lo)) }
	for i := 0; i <= 4; i++ {
		v := (lo + (hi-lo)*float64(i)/4) * scale
		c.Ticks = append(c.Ticks, chartTick{y(v), renderNumber(v), "end"})
	}
	for i, t := range []float64{start, start + (end-start)/2, end} {
		c.Times = append(c.Times, chartTick{x(t), time.Unix(int64(t), 0).UTC().Format("15:04:05"), []string{"start", "middle", "end"}[i]})
	}
	for _, t := range []float64{run.Start, run.End} {
		if t >= start && t <= end {
			c.RunMarkers = append(c.RunMarkers, x(t))
		}
	}
	for _, s := range series {
		line := chartLine{Name: s.name, Color: s.color, Dashed: s.dashed}
		var path strings.Builder
		connected := false
		for _, p := range s.values {
			if p.Value == nil {
				connected = false
				continue
			}
			command := "M"
			if connected {
				command = "L"
			}
			fmt.Fprintf(&path, "%s%s,%s ", command, x(p.Time), y(*p.Value))
			connected = true
			line.Dots = append(line.Dots, chartDot{x(p.Time), y(*p.Value), s.name + ": " + renderNumber(*p.Value) + " at " + renderUTC(p.Time)})
		}
		line.Path = path.String()
		c.Lines = append(c.Lines, line)
	}
	return c
}

func renderUTC(value any) string {
	var seconds float64
	switch v := value.(type) {
	case float64:
		seconds = v
	case int64:
		seconds = float64(v)
	default:
		return "Unavailable"
	}
	whole, fraction := math.Modf(seconds)
	return time.Unix(int64(whole), int64(math.Round(fraction*1e9))).UTC().Format("2006-01-02 15:04:05.999 UTC")
}

func renderNumber(value any) string {
	var v float64
	switch n := value.(type) {
	case *float64:
		if n == nil {
			return "Unavailable"
		}
		v = *n
	case float64:
		v = n
	case int:
		v = float64(n)
	case int64:
		v = float64(n)
	case *int:
		if n == nil {
			return "Unavailable"
		}
		v = float64(*n)
	default:
		return "Unavailable"
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "Unavailable"
	}
	if math.Abs(v) >= 1e15 {
		return strconv.FormatFloat(v, 'g', 5, 64)
	}
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	parts := strings.SplitN(s, ".", 2)
	whole := parts[0]
	start := 0
	if strings.HasPrefix(whole, "-") {
		start = 1
	}
	for i := len(whole) - 3; i > start; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if len(parts) == 2 {
		whole += "." + parts[1]
	}
	return whole
}
