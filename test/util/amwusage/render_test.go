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
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func testRecord(body string) artifactRecord { return artifactRecord{OK: true, Body: body} }

func testArtifact() *artifactData {
	return &artifactData{
		SchemaVersion: 1,
		Run:           &artifactRun{Start: 0, End: 775, PlatformStart: 0, PlatformEnd: 180, Finished: map[string]any{"result": "FAILURE"}},
		Workspaces:    []artifactWorkspace{{Name: "test-workspace", Names: []string{"metric", "unselected"}, Metrics: []artifactMetric{{Name: "metric", Instant: "i", Range: "r"}}, Platform: map[string]string{}}},
		Manifests:     []artifactRecord{},
		Records: map[string]artifactRecord{
			"i": testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[775,"2"]},{"metric":{"job":"b"},"value":[775,"3"]}]}}`),
			"r": testRecord(`{"status":"success","data":{"result":[{"values":[[300,"2"],[600,"3"]]},{"values":[[300,"4"],[600,"NaN"]]}]}}`),
		},
	}
}

func testJSON(t *testing.T, a *artifactData) []byte {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func requireNumber(t *testing.T, actual *float64, want float64) {
	t.Helper()
	if actual == nil || *actual != want {
		t.Fatalf("numeric result = %v, want %v", actual, want)
	}
}

func TestRenderMetricMissingness(t *testing.T) {
	a := testArtifact()
	m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	requireNumber(t, m.Series, 5)
	requireNumber(t, m.Values[0].Value, 6)
	if m.Values[1].Value != nil {
		t.Fatal("invalid group must poison entire evaluation")
	}
	requireNumber(t, m.ObservedSamples, 30)
	requireNumber(t, m.PeakSamplesPerMinute, 6)
	if m.Expected != 2 || m.Valid != 1 || m.InvalidPoints != 1 || m.InvalidEvaluations != 1 || m.TrailingSeconds != 175 {
		t.Fatalf("unexpected coverage: %+v", m)
	}
	a.Records["i"] = testRecord(`{"status":"success","data":{"result":[]}}`)
	a.Records["r"] = testRecord(`{"status":"success","data":{"result":[{"values":[[300,"0"],[600,"3"]]},{"values":[[300,"4"]]}]}}`)
	m = analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	requireNumber(t, m.Values[0].Value, 4)
	requireNumber(t, m.Values[1].Value, 3)
	requireNumber(t, m.ObservedSamples, 35)
	if m.Series != nil || m.InvalidPoints != 0 || m.InvalidEvaluations != 0 {
		t.Fatal("absent series must not be confused with explicitly invalid groups or numeric zero")
	}
	a.Records["r"] = testRecord(`{"status":"success","data":{"result":[{"values":[[300,"0"],[600,"0"]]}]}}`)
	m = analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	requireNumber(t, m.ObservedSamples, 0)
	requireNumber(t, m.PeakSamplesPerMinute, 0)
	if m.Valid != 2 {
		t.Fatal("zero-valued evaluations must be counted")
	}
}

func TestRenderInvalidGroups(t *testing.T) {
	for _, value := range []any{"NaN", "+Inf", "-Inf", "Infinity", nil, "", "not-a-number", true, "1e999"} {
		for _, invalidFirst := range []bool{true, false} {
			a := testArtifact()
			results := []any{
				map[string]any{"metric": map[string]string{"job": "valid"}, "value": []any{775, "3"}, "values": []any{[]any{300, "3"}, []any{600, "0"}}},
				map[string]any{"metric": map[string]string{"job": "invalid"}, "value": []any{775, value}, "values": []any{[]any{300, value}, []any{600, value}}},
			}
			if invalidFirst {
				results[0], results[1] = results[1], results[0]
			}
			body, err := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"result": results}})
			if err != nil {
				t.Fatal(err)
			}
			a.Records["i"], a.Records["r"] = testRecord(string(body)), testRecord(string(body))
			m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
			if m.Series != nil || m.InvalidCardinalityGroups != 1 || m.Values[0].Value != nil || m.Values[1].Value != nil || m.InvalidPoints != 2 || m.InvalidEvaluations != 2 || m.Valid != 0 || m.ObservedSamples != nil || m.PeakSamplesPerMinute != nil {
				t.Fatalf("invalid value %#v, invalid-first %v not withheld: %+v", value, invalidFirst, m)
			}
			requireNumber(t, m.Groups[0].Count, 3)
		}
	}
}

func TestRenderMalformedAndFailedResponses(t *testing.T) {
	for _, record := range []artifactRecord{{}, {OK: false, Error: json.RawMessage(`"quota exceeded"`)}, testRecord(`{`), testRecord(`null`), testRecord(`{}`), testRecord(`{"status":"success","data":{}}`), testRecord(`{"status":"error","data":{"result":[{"value":[775,"3"],"values":[[300,"3"]]}]}}`)} {
		a := testArtifact()
		a.Records["i"], a.Records["r"] = record, record
		m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
		if m.InstantOK || m.RangeOK || m.Series != nil || m.ObservedSamples != nil || m.Valid != 0 || m.InstantState == "" || m.RangeState == "" {
			t.Fatalf("failed response produced numeric evidence: %+v", m)
		}
		var b bytes.Buffer
		if err := Render(&b, testJSON(t, a)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "Unavailable, not zero") {
			t.Fatal("failed query is not explained")
		}
	}
	a := testArtifact()
	a.Records["r"] = artifactRecord{}
	m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	requireNumber(t, m.Series, 5)
	if !m.InstantOK || m.RangeOK {
		t.Fatal("independent query success was not preserved")
	}
}

func TestRenderDuplicateAndOffGridPoints(t *testing.T) {
	a := testArtifact()
	a.Records["r"] = testRecord(`{"status":"success","warnings":["partial data"],"data":{"result":[{"values":[[0,"20"],[300,"2"],[300,"3"],[301,"4"],[600,"0"],[900,"5"],[],["NaN","6"]]}]}}`)
	m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	if m.DuplicatePoints != 1 || m.OutOfRangePoints != 5 || m.InvalidEvaluations != 1 || m.Values[0].Value != nil || !reflect.DeepEqual(m.Warnings, []string{"partial data"}) {
		t.Fatalf("unexpected point diagnostics: %+v", m)
	}
	requireNumber(t, m.Values[1].Value, 0)
	// Finite groups can still overflow a sum. That must not generate Inf SVG coordinates.
	a.Records["r"] = testRecord(`{"status":"success","data":{"result":[{"values":[[300,"1e308"]]},{"values":[[300,"1e308"]]}]}}`)
	m = analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	if m.Values[0].Value != nil || m.InvalidEvaluations != 1 {
		t.Fatal("overflowed evaluation was not withheld")
	}
}

func testPlatformRecord(value string, reason bool) artifactRecord {
	metadata := `[]`
	if reason {
		metadata = `[{"name":{"value":"Reason"},"value":"LimitThrottling"}]`
	}
	return testRecord(`{"interval":"PT1M","value":[{"timeseries":[{"metadatavalues":` + metadata + `,"data":[{"timeStamp":"1970-01-01T00:00:00Z","maximum":` + value + `},{"timeStamp":"1970-01-01T00:01:00Z","maximum":` + value + `},{"timeStamp":"1970-01-01T00:02:00Z","maximum":` + value + `}]}]}]}`)
}

func testCompletePlatform(a *artifactData) {
	for _, name := range []string{"ActiveTimeSeries", "EventsPerMinuteIngested", "ActiveTimeSeriesLimit", "EventsPerMinuteIngestedLimit", "EventsDropped", "TimeSeriesSamplesDropped"} {
		a.Workspaces[0].Platform[name] = name
		value := "0"
		if strings.HasSuffix(name, "Limit") {
			value = "10"
		}
		a.Records[name] = testPlatformRecord(value, strings.HasSuffix(name, "Dropped"))
	}
}

func TestRenderPlatformCoverage(t *testing.T) {
	a := testArtifact()
	a.Records["p"] = testRecord(`{"interval":"PT1M","value":[{"timeseries":[{"data":[{"timeStamp":"1970-01-01T00:00:00Z","maximum":0},{"timeStamp":"1970-01-01T00:01:00Z"}]}]}]}`)
	p := analyzePlatform("p", a.Records, a.Run)
	s := p.Series[0]
	if s.Expected != 3 || s.Valid != 1 || s.Missing != 2 || s.Values[1].Value != nil || s.Values[2].Value != nil {
		t.Fatalf("platform missingness lost: %+v", s)
	}
	requireNumber(t, s.Max, 0)
	a.Records["p"] = testRecord(`{"interval":"PT5M","value":[{"timeseries":[{"data":[{"timeStamp":"1970-01-01T00:00:00Z","maximum":0},{"timeStamp":"1970-01-01T00:00:00Z","maximum":7},{"timeStamp":"bad","maximum":5}]}]}]}`)
	s = analyzePlatform("p", a.Records, a.Run).Series[0]
	if s.DuplicateTimestamps != 1 || s.OutOfRangePoints != 1 || s.Values[0].Value != nil || completePlatform(s) {
		t.Fatal("ambiguous platform minute must not produce complete evidence")
	}
}

func TestRenderQuotaVerdicts(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*artifactData)
		want         string
		noThrottling bool
	}{
		{"complete zero", func(a *artifactData) {}, "No quota crossing observed with complete", true},
		{"crossing", func(a *artifactData) { a.Records["ActiveTimeSeries"] = testPlatformRecord("10", false) }, "Quota crossing observed", false},
		{"crossing and missing", func(a *artifactData) {
			a.Records["ActiveTimeSeries"] = testPlatformRecord("11", false)
			delete(a.Records, "EventsPerMinuteIngestedLimit")
		}, "Quota crossing observed", false},
		{"missing limit", func(a *artifactData) { delete(a.Records, "ActiveTimeSeriesLimit") }, "Quota coverage is incomplete", false},
		{"zero limit", func(a *artifactData) { a.Records["ActiveTimeSeriesLimit"] = testPlatformRecord("0", false) }, "Quota coverage is incomplete", false},
		{"missing reason", func(a *artifactData) { delete(a.Records, "EventsDropped") }, "Drop-reason coverage is incomplete", false},
		{"drops", func(a *artifactData) { a.Records["EventsDropped"] = testPlatformRecord("1", true) }, "Limit-throttling drops observed", false},
		{"multiple physical series", func(a *artifactData) {
			r := a.Records["ActiveTimeSeries"]
			r.Body = strings.Replace(r.Body, `"timeseries":[`, `"timeseries":[{"metadatavalues":[{"name":{"value":"stamp"},"value":"another"}],"data":[]},`, 1)
			a.Records["ActiveTimeSeries"] = r
		}, "Quota coverage is incomplete", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testArtifact()
			testCompletePlatform(a)
			tt.mutate(a)
			w := analyzeArtifact(a).Workspaces[0]
			if !strings.Contains(w.Verdict, tt.want) || w.NoLimitThrottling != tt.noThrottling {
				t.Fatalf("verdict: %q, no-throttling %v", w.Verdict, w.NoLimitThrottling)
			}
			incomplete, crossing := false, false
			for _, q := range w.Quota {
				incomplete = incomplete || !q.Complete
				crossing = crossing || q.Crossings > 0
			}
			if (incomplete || crossing) && strings.Contains(w.Verdict, "No quota crossing") {
				t.Fatalf("false no-crossings claim: %s", w.Verdict)
			}
			if tt.name == "multiple physical series" && len(w.Quota[0].Chart.Lines) != 3 {
				t.Fatal("physical series omitted from chart")
			}
		})
	}
}

func TestRenderMalformedSchema(t *testing.T) {
	for _, data := range []string{``, `{`, `null`, `[]`, `{}`, `{"schemaVersion":2}`, `{"schemaVersion":1}`, `{"schemaVersion":1,"run":{},"workspaces":[],"manifests":[],"records":{}}`} {
		var b bytes.Buffer
		if err := Render(&b, []byte(data)); err == nil || b.Len() != 0 {
			t.Fatalf("invalid schema accepted or emitted partial HTML: %q", data)
		}
	}
	for _, mutate := range []func(*artifactData){
		func(a *artifactData) { a.Run.End = a.Run.Start },
		func(a *artifactData) { a.Run.Start = -1 },
		func(a *artifactData) { a.Run.PlatformEnd = 181 },
		func(a *artifactData) { a.Run.End = 9223372036854775807 },
		func(a *artifactData) { a.Records = nil },
	} {
		a := testArtifact()
		mutate(a)
		if err := Render(io.Discard, testJSON(t, a)); err == nil {
			t.Fatal("invalid artifact accepted")
		}
	}
	// A short run has no complete sample windows, not a fabricated zero total.
	a := testArtifact()
	a.Run.End = 50
	m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	if m.Expected != 0 || m.ObservedSamples != nil {
		t.Fatal("short run fabricated complete windows")
	}
	if err := Render(io.Discard, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
}

func TestRenderTotals(t *testing.T) {
	a := testArtifact()
	for i, kind := range []string{"public", "promql", "promql", "platform"} {
		m := artifactRecord{OK: i%2 == 0, ElapsedMS: 10, ResponseBytes: finiteSum(100), APICost: finiteSum(2), StartedAt: "2026-01-01", FinishedAt: "2026-01-02"}
		m.Request.Kind = kind
		a.Manifests = append(a.Manifests, m)
	}
	s := analyzeArtifact(a)
	if s.Requests != 4 || s.Errors != 2 || s.PromQL != 2 || s.PromQLErrors != 1 || s.PlatformRequests != 1 || s.PlatformErrors != 1 || s.ElapsedMS != 40 || s.ResponseBytes != 400 || s.APICost != 8 || s.ByteReports != 4 || s.CostReports != 4 || s.FirstRequest != "2026-01-01" || s.LastRequest != "2026-01-02" {
		t.Fatalf("incorrect ledger totals: %+v", s)
	}
}

func TestRenderFractionalWindowAndCheckpoint(t *testing.T) {
	a := testArtifact()
	a.Run.Start, a.Run.End = 0.125, 775.25
	a.Records["r"] = testRecord(`{"status":"success","data":{"result":[{"values":[[300.125,"2"],[600.125,"3"]]}]}}`)
	complete := false
	a.Complete = &complete
	a.Errors = []string{"selection failed before query planning"}
	a.Workspaces[0].Discovery = "discovery-request"
	a.Records["discovery-request"] = artifactRecord{Error: json.RawMessage(`"discovery timed out"`)}
	a.Workspaces[0].Names = nil
	m := analyzeMetric(a.Workspaces[0].Metrics[0], a.Records, a.Run)
	if m.Valid != 2 || m.TrailingSeconds != 175.125 {
		t.Fatalf("fractional run coverage changed: %+v", m)
	}
	requireNumber(t, m.ObservedSamples, 25)
	var report bytes.Buffer
	if err := Render(&report, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Incomplete checkpoint", "selection failed before query planning", "Discovery coverage unavailable", "discovery timed out", "00:00:00.125 UTC"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("partial checkpoint report missing %q", want)
		}
	}
}

func TestRenderChartGapsAndExtremeValues(t *testing.T) {
	run := &artifactRun{Start: 0, End: 180}
	chart := makeChart("gaps", []chartInput{{"values", "#006f79", false, []renderPoint{{0, finiteSum(0)}, {60, nil}, {120, finiteSum(2)}}}}, 0, 180, run)
	if strings.Count(chart.Lines[0].Path, "M") != 2 || strings.Contains(chart.Lines[0].Path, "L") || len(chart.Lines[0].Dots) != 2 {
		t.Fatal("chart bridged unavailable data or omitted zero")
	}
	chart = makeChart("extremes", []chartInput{{"values", "#006f79", false, []renderPoint{{0, finiteSum(-math.MaxFloat64)}, {0, finiteSum(math.MaxFloat64)}}}}, 0, 0, run)
	if strings.Contains(chart.Lines[0].Path, "Inf") || strings.Contains(chart.Lines[0].Path, "NaN") {
		t.Fatalf("invalid SVG geometry: %s", chart.Lines[0].Path)
	}
}

func TestRenderOfflineAndEscaping(t *testing.T) {
	a := testArtifact()
	testCompletePlatform(a)
	evil := "</script><img src=x onerror=alert(1)><svg onload=alert(2)>&\u2028\u2029"
	a.Workspaces[0].Name = evil
	a.Workspaces[0].Metrics[0].RangeQuery = evil
	a.Run.Prow = "javascript:alert(3)"
	a.Records["i"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"</td><script>alert(4)</script>"},"value":[775,"0"]}]}}`)
	var archive map[string]json.RawMessage
	if err := json.Unmarshal(testJSON(t, a), &archive); err != nil {
		t.Fatal(err)
	}
	archive["unknownProvenance"] = json.RawMessage(`{"keep": "all fields"}`)
	data, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Render(&b, data); err != nil {
		t.Fatal(err)
	}
	report := b.String()
	for _, forbidden := range []string{"<iframe", "<img", "<use", "<link", "href=\"javascript:", "<svg onload", "</td><script>", "fetch(", "import(", "XMLHttpRequest", "file://", "#ZgotmplZ"} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("report contains forbidden active content %q", forbidden)
		}
	}
	scripts := regexp.MustCompile(`<script[^>]*src="([^"]+)"`).FindAllStringSubmatch(report, -1)
	if len(scripts) != 1 || scripts[0][1] != "https://cdn.jsdelivr.net/npm/echarts@5.6.0/dist/echarts.min.js" {
		t.Fatalf("unexpected script sources: %v", scripts)
	}
	inline := regexp.MustCompile(`(?s)<script id="contribution-data" type="application/json">(.*?)</script>`).FindStringSubmatch(report)
	if len(inline) != 2 || strings.ContainsAny(inline[1], "<>&\u2028\u2029") {
		t.Fatal("contribution JSON must escape HTML-sensitive and script-separating characters")
	}
	var contributions contributionAnalysis
	if err := json.Unmarshal([]byte(inline[1]), &contributions); err != nil || len(contributions.Rows) != 1 || contributions.Rows[0].WorkspaceName != evil {
		t.Fatalf("contribution JSON is not safe and reversible: %v", err)
	}
	for _, want := range []string{"default-src 'none'", "<svg viewBox", "<table>", "Recorded result: FAILURE", "<script>", "data-filter", `id="download"`} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q", want)
		}
	}
	match := regexp.MustCompile(`(?s)<pre id="evidence">(.*?)</pre>`).FindStringSubmatch(report)
	if len(match) != 2 {
		t.Fatal("full evidence not retained")
	}
	var recovered map[string]json.RawMessage
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &recovered); err != nil {
		t.Fatalf("embedded JSON is not safe and reversible: %v", err)
	}
	var original, roundtrip any
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &roundtrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, roundtrip) {
		t.Fatal("full evidence changed during rendering")
	}
	charts := regexp.MustCompile(`(?s)<figure>.*?</figure>`)
	if len(charts.FindAllString(report, -1)) != 4 {
		t.Fatal("expected an immediate active-series chart, two quota charts and one metric chart")
	}
}

func TestRenderSafeLinks(t *testing.T) {
	for _, link := range []string{"javascript:alert(1)", "data:text/html,evil", "file:///tmp/evil", "//example.com", "https://user:pass@example.com", "https://example.com/report"} {
		a := testArtifact()
		a.Run.Prow = link
		var b bytes.Buffer
		if err := Render(&b, testJSON(t, a)); err != nil {
			t.Fatal(err)
		}
		linked := strings.Contains(b.String(), `rel="noreferrer">Prow `)
		if linked != (link == "https://example.com/report") {
			t.Fatalf("unexpected link policy for %q", link)
		}
	}
}

type renderErrorWriter struct{}

func (renderErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRenderWriterError(t *testing.T) {
	if err := Render(renderErrorWriter{}, testJSON(t, testArtifact())); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("writer error not propagated: %v", err)
	}
}

// Optional archive regression, deliberately not dependent on a machine-specific
// fixture or network. Run with AMW_USAGE_ARCHIVE pointing to the reviewed v1 data.
func TestRenderArchivedEvidence(t *testing.T) {
	path := os.Getenv("AMW_USAGE_ARCHIVE")
	if path == "" {
		t.Skip("AMW_USAGE_ARCHIVE not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := decodeArtifact(data)
	if err != nil {
		t.Fatal(err)
	}
	s := analyzeArtifact(a)
	if s.Requests != 38 || s.Errors != 6 || s.PromQL != 16 || s.PromQLErrors != 2 || s.PlatformRequests != 16 || s.PlatformErrors != 4 || len(s.Workspaces) != 2 || s.ElapsedMS != 32832 || s.ResponseBytes != 490507 || s.APICost != 1896 {
		t.Fatalf("reviewed archive request totals differ: %+v", s)
	}
	requireNumber(t, s.Workspaces[0].MetricResults[0].Series, 11904)
	requireNumber(t, s.Workspaces[1].MetricResults[3].Series, 26010)
	wantSamples := [][]float64{{322992, 0, 243473, 11174}, {615780, 194975, 1943530, 1721471}}
	wantValid := [][]int{{20, 0, 22, 21}, {24, 27, 27, 27}}
	for wi, w := range s.Workspaces {
		if w.NoLimitThrottling {
			t.Fatal("reviewed archive has incomplete drop-reason coverage")
		}
		for _, q := range w.Quota {
			if !q.Complete || q.Crossings != 0 {
				t.Fatalf("archive quota changed: %+v", q)
			}
		}
		for mi, m := range w.MetricResults {
			if m.Expected != 27 || m.TrailingSeconds != 175 || m.InvalidPoints != 0 || m.InvalidEvaluations != 0 || m.InvalidCardinalityGroups != 0 {
				t.Fatalf("archive metric coverage changed: %+v", m)
			}
			if m.Valid != wantValid[wi][mi] {
				t.Fatalf("%s numeric evaluations: %d", m.Name, m.Valid)
			}
			if m.RangeOK {
				if m.ObservedSamples == nil || math.Abs(*m.ObservedSamples-wantSamples[wi][mi]) > 1e-6 {
					t.Fatalf("%s sample subtotal: %v", m.Name, m.ObservedSamples)
				}
			} else if m.ObservedSamples != nil {
				t.Fatal("failed query produced a subtotal")
			}
		}
	}
	if err := Render(io.Discard, data); err != nil {
		t.Fatal(err)
	}
}
