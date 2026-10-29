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
	"html"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func contributionFixture() *artifactData {
	a := testArtifact()
	a.Workspaces[0].Metrics[0].NewSeries = "n"
	a.Workspaces[0].Metrics[0].Samples = "s"
	a.Records["n"] = testRecord(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"A"},"value":[775,"1"]}]}}`)
	a.Records["s"] = testRecord(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"a"},"value":[775,"100"]},{"metric":{"job":"b"},"value":[775,"300"]}]}}`)
	return a
}

func baselineFixture() *artifactData {
	a := contributionFixture()
	a.Run.Start, a.Run.End = 3600, 4375
	for _, ref := range []string{"i", "n", "s"} {
		r := a.Records[ref]
		r.Body = strings.ReplaceAll(r.Body, "775", "4375")
		a.Records[ref] = r
	}
	a.Context = &artifactContext{SchemaVersion: 1}
	a.Context.Run.Start, a.Context.Run.End = "1970-01-01T01:00:00Z", "1970-01-01T01:12:55Z"
	a.Context.Baseline.Start, a.Context.Baseline.End, a.Context.Baseline.Reason = "1970-01-01T00:10:00Z", "1970-01-01T00:20:00Z", "Explicit verified quiet interval; shared load remains qualified"
	a.Context.Clusters = []artifactCluster{
		{ID: "sameprefix-one", Name: "duplicate-name", Namespaces: []string{"ocm-a", "ocm-a-cp"}},
		{ID: "sameprefix-two", Name: "duplicate-name", Namespaces: []string{"ocm-b", "ocm-b-cp"}},
	}
	m := &a.Workspaces[0].Metrics[0]
	m.BaselineSeries, m.BaselineSamples = "bs", "bp"
	a.Records["bs"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[1200,"2"]},{"metric":{"job":"baseline-only"},"value":[1200,"4"]}]}}`)
	a.Records["bp"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[1200,"100"]},{"metric":{"job":"baseline-only"},"value":[1200,"600"]}]}}`)
	return a
}

func TestExplicitBaselineUnionAndRates(t *testing.T) {
	a := baselineFixture()
	c := analyzeContributions(a, analyzeArtifact(a))
	if !c.BaselineValid || !c.ContextValid || c.BaselineDuration != 600 || c.NewLabel != "New vs explicit quiet baseline" || len(c.Rows) != 3 {
		t.Fatalf("unexpected baseline context: %+v", c)
	}
	rows := map[string]contributionRow{}
	for _, r := range c.Rows {
		rows[r.Labels["job"]] = r
	}
	old := rows["baseline-only"]
	requireNumber(t, old.BaselineSeries, 4)
	requireNumber(t, old.RunSeries, 0)
	requireNumber(t, old.NewSeries, 0)
	requireNumber(t, old.Samples, 0)
	requireNumber(t, old.BaselineRate, 60)
	requireNumber(t, old.RateDelta, -60)
	if old.Cohort != "Group present only in baseline" {
		t.Fatal("baseline-only group lost")
	}
	b := rows["b"]
	requireNumber(t, b.BaselineSeries, 0)
	requireNumber(t, b.BaselineSamples, 0)
	if b.Cohort != "Group present only in run" {
		t.Fatal("run-only group lost")
	}
	r := rows["a"]
	if r.Cohort != "Group present in both periods" {
		t.Fatal("persistent group lost")
	}
	requireNumber(t, r.RunRate, 100/(775.0/60))
	requireNumber(t, r.BaselineRate, 10)
	requireNumber(t, r.RateDelta, *r.RunRate-*r.BaselineRate) // Equal sample totals, unequal rates.
	requireNumber(t, c.Scopes[0].RunRate, 400/(775.0/60))
	requireNumber(t, c.Scopes[0].BaselineRate, 70)
	if c.Scopes[0].DeltaKnown != 3 {
		t.Fatal("paired coverage not retained")
	}
}

func TestContributionOwnershipExactAndConflicting(t *testing.T) {
	a := baselineFixture()
	c := contributionAnalysis{}
	owners := contributionContext(a, &c)
	for _, tt := range []struct{ namespace, hcp, category, id string }{
		{"ocm-a", "", ownershipRun, "sameprefix-one"},
		{"shared", "ocm-a-cp", ownershipRun, "sameprefix-one"},
		{"", "ocm-b", ownershipRun, "sameprefix-two"},
		{"OCM-A", "ocm-a-cp", ownershipRun, "sameprefix-one"},
		{"ocm-a", "ocm-b", ownershipAmbiguous, ""},
		{"ocm-a-cp-extra", "", ownershipOther, ""},
		{"", "ocm-unmapped", ownershipOther, ""},
		{"duplicate-name", "", ownershipShared, ""},
		{"", "", ownershipShared, ""},
		{"kube-system", "", ownershipShared, ""},
	} {
		r := contributionRow{Labels: map[string]string{"namespace": tt.namespace, "hostedcontrolplane": tt.hcp}}
		ok := assignContributionOwner(&r, owners)
		if r.Ownership != tt.category || r.CustomerID != tt.id || ok != (tt.category != ownershipAmbiguous) {
			t.Fatalf("%+v: %+v", tt, r)
		}
	}
	r1, r2 := contributionRow{Labels: map[string]string{"namespace": "ocm-a"}}, contributionRow{Labels: map[string]string{"namespace": "ocm-b"}}
	assignContributionOwner(&r1, owners)
	assignContributionOwner(&r2, owners)
	if r1.Customer == r2.Customer || r1.CustomerID == r2.CustomerID {
		t.Fatal("duplicate display names or ID prefixes merged customer clusters")
	}
	a.Context.Clusters[1].Namespaces = append(a.Context.Clusters[1].Namespaces, "ocm-a")
	owners = contributionContext(a, &c)
	if assignContributionOwner(&r1, owners) || r1.Ownership != ownershipAmbiguous {
		t.Fatal("colliding literal namespace auto-assigned")
	}
	a.Records["i"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"namespace":"ocm-a","hostedcontrolplane":"ocm-b"},"value":[4375,"1"]}]}}`)
	c = analyzeContributions(a, analyzeArtifact(a))
	if !strings.Contains(strings.Join(c.Warnings, "\n"), "Ambiguous ownership") {
		t.Fatal("ownership conflict lacks warning")
	}
}

func TestBaselineFailuresAndTimestamps(t *testing.T) {
	for _, tt := range []struct {
		name   string
		record artifactRecord
		zero   bool
	}{
		{"empty", testRecord(`{"status":"success","data":{"result":[]}}`), true},
		{"failed", artifactRecord{}, false},
		{"partial", testRecord(`{"status":"success","warnings":["partial"],"data":{"result":[]}}`), false},
		{"wrong clock", testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[4375,"10"]}]}}`), false},
		{"invalid", testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[1200,"NaN"]}]}}`), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := baselineFixture()
			a.Records["bs"], a.Records["bp"] = tt.record, tt.record
			c := analyzeContributions(a, analyzeArtifact(a))
			for _, r := range c.Rows {
				if tt.zero {
					requireNumber(t, r.BaselineSeries, 0)
					requireNumber(t, r.BaselineSamples, 0)
				} else if r.BaselineSeries != nil || r.BaselineSamples != nil || r.RateDelta != nil {
					t.Fatalf("unknown baseline fabricated: %+v", r)
				}
			}
		})
	}
	a := baselineFixture()
	a.Records["bp"] = testRecord(`{"status":"success","warnings":["partial"],"data":{"result":[{"metric":{"job":"a"},"value":[1200,"100"]}]}}`)
	c := analyzeContributions(a, analyzeArtifact(a))
	requireNumber(t, c.Rows[0].BaselineSamples, 100)
	if c.Rows[0].RateDelta != nil || c.Rows[1].BaselineSamples != nil {
		t.Fatal("partial baseline manufactured paired delta or absent zero")
	}
	delete(a.Records, "bp")
	a.Workspaces[0].Metrics[0].BaselineSeries = ""
	c = analyzeContributions(a, analyzeArtifact(a))
	for _, r := range c.Rows {
		if r.BaselineSeries != nil || r.BaselineSamples != nil {
			t.Fatal("missing baseline reference fabricated")
		}
	}
}

func TestBaselineInvalidContextAndLegacyLabels(t *testing.T) {
	for _, mutate := range []func(*artifactData){
		func(a *artifactData) { a.Context.SchemaVersion = 2 },
		func(a *artifactData) {
			a.Run.Prow = "https://example.org/run-a"
			a.Context.Run.Prow = "https://example.org/run-b"
		},
		func(a *artifactData) { a.Context.Run.Start = "1970-01-01T00:59:59Z" },
		func(a *artifactData) { a.Context.Baseline.End = "1970-01-01T01:01:00Z" },
		func(a *artifactData) { a.Context.Baseline.Start = a.Context.Baseline.End },
		func(a *artifactData) { a.Context.Baseline.End = "invalid" },
	} {
		a := baselineFixture()
		mutate(a)
		c := analyzeContributions(a, analyzeArtifact(a))
		if c.BaselineValid || len(c.Warnings) == 0 || c.NewLabel == "Absent in preceding12h" {
			t.Fatal("invalid explicit context fell back to legacy semantics")
		}
		for _, r := range c.Rows {
			if r.BaselineSeries != nil || r.NewSeries != nil {
				t.Fatal("invalid context yielded comparison")
			}
		}
	}
	a := contributionFixture()
	c := analyzeContributions(a, analyzeArtifact(a))
	if c.NewLabel != "Absent in preceding12h" || c.BaselineValid {
		t.Fatal("legacy difference relabeled as quiet baseline")
	}
	var report bytes.Buffer
	if err := Render(&report, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "Absent in preceding12h") || strings.Contains(report.String(), "<strong>Explicit quiet baseline:</strong>") {
		t.Fatal("legacy HTML claims quiet baseline")
	}
	a.Workspaces[0].Metrics[0].NewSeries = ""
	c = analyzeContributions(a, analyzeArtifact(a))
	if c.DefaultWeight != "runSeries" {
		t.Fatal("no-context uncollected difference should remain unknown")
	}
}

func TestOwnershipTotalsIndependentOfNewness(t *testing.T) {
	a := baselineFixture()
	for _, ref := range []string{"i", "s", "bs", "bp"} {
		r := a.Records[ref]
		r.Body = strings.ReplaceAll(r.Body, `"job":"a"`, `"job":"a","namespace":"ocm-a"`)
		a.Records[ref] = r
	}
	a.Records["n"] = testRecord(`{"status":"success","data":{"result":[]}}`)
	c := analyzeContributions(a, analyzeArtifact(a))
	owned := c.Scopes[1]
	requireNumber(t, owned.RunSeries, 2)
	requireNumber(t, owned.NewSeries, 0)
	requireNumber(t, owned.BaselineSeries, 2)
	if owned.Name != ownershipRun || owned.Rows != 1 {
		t.Fatalf("bad run-owned scope: %+v", owned)
	}
}

func TestBaselineRateDeltaRequiresPairedEvidence(t *testing.T) {
	rows := []contributionRow{
		{RunRate: finiteSum(10)},
		{BaselineRate: finiteSum(20)},
	}
	s := summarizeContributionScope("unpaired", rows)
	requireNumber(t, s.RunRate, 10)
	requireNumber(t, s.BaselineRate, 20)
	if s.RateDelta != nil || s.DeltaKnown != 0 {
		t.Fatal("different measured subsets subtracted as a paired delta")
	}
	rows = append(rows, contributionRow{RunRate: finiteSum(5), BaselineRate: finiteSum(8), RateDelta: finiteSum(-3)})
	s = summarizeContributionScope("partly paired", rows)
	requireNumber(t, s.RateDelta, -3)
	if s.DeltaKnown != 1 || s.Rows != 3 {
		t.Fatal("paired subset coverage lost")
	}
}

func TestBaselineContextEscapingAndPreservation(t *testing.T) {
	a := baselineFixture()
	evil := `</script><img src=x onerror=alert(1)>`
	a.Context.Clusters[0].Name = evil
	a.Context.Baseline.Reason = evil
	a.Context.Limitations = []string{evil}
	a.Context.Baseline.Sources = []string{"javascript:alert(1)", "https://example.com/evidence"}
	a.Records["i"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"namespace":"ocm-a"},"value":[4375,"1"]}]}}`)
	var report bytes.Buffer
	if err := Render(&report, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), evil) || strings.Contains(report.String(), `href="javascript:`) {
		t.Fatal("untrusted context emitted active content")
	}
	match := regexp.MustCompile(`(?s)<pre id="evidence">(.*?)</pre>`).FindStringSubmatch(report.String())
	var recovered artifactData
	if len(match) != 2 {
		t.Fatal("missing raw evidence")
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &recovered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Context, recovered.Context) {
		t.Fatal("context provenance changed in archive round-trip")
	}
}

// Optional real context regression. No measurements are synthesized from the
// inventory; the numeric run clock only checks the independent ownership join.
func TestOwnershipContextArchive(t *testing.T) {
	path := os.Getenv("AMW_USAGE_CONTEXT")
	if path == "" {
		t.Skip("AMW_USAGE_CONTEXT not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ctx artifactContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatal(err)
	}
	a := testArtifact()
	a.Context = &ctx
	start, err := time.Parse(time.RFC3339Nano, ctx.Run.Start)
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.RFC3339Nano, ctx.Run.End)
	if err != nil {
		t.Fatal(err)
	}
	a.Run.Start, a.Run.End = float64(start.Unix()), float64(end.Unix())
	c := contributionAnalysis{}
	owners := contributionContext(a, &c)
	if !c.BaselineValid || len(ctx.Clusters) != 55 || len(owners) != 110 {
		t.Fatalf("context inventory changed: clusters=%d namespaces=%d warnings=%v", len(ctx.Clusters), len(owners), c.Warnings)
	}
	for _, cluster := range ctx.Clusters {
		for _, ns := range cluster.Namespaces {
			r := contributionRow{Labels: map[string]string{"hostedcontrolplane": ns}}
			if !assignContributionOwner(&r, owners) || r.CustomerID != cluster.ID {
				t.Fatalf("literal namespace %s did not map to ID %s", ns, cluster.ID)
			}
		}
	}
}

func TestContributionsFullWindowAndZero(t *testing.T) {
	a := contributionFixture()
	c := analyzeContributions(a, analyzeArtifact(a))
	if c.DefaultWeight != "newSeries" || len(c.Rows) != 2 || c.Measured != 2 || len(c.Warnings) != 0 {
		t.Fatalf("unexpected contribution coverage: %+v", c)
	}
	requireNumber(t, c.Rows[0].RunSeries, 2)
	requireNumber(t, c.Rows[0].NewSeries, 1)
	requireNumber(t, c.Rows[1].NewSeries, 0)
	requireNumber(t, c.Rows[0].Samples, 100)
	requireNumber(t, c.Rows[1].Samples, 300)
	requireNumber(t, c.SamplesTotal, 400)
	requireNumber(t, c.SamplesPerMinute, 400/(775.0/60))
	requireNumber(t, c.Total, 1)
	requireNumber(t, c.Ranked[0].Share, 1)
	requireNumber(t, c.Ranked[1].Cumulative, 1)
	if c.Rows[0].Labels["job"] != "a" || c.Rows[0].Collector != "Unattributed" {
		t.Fatal("join changed display or inferred collector")
	}
	// Exact samples include all 775 seconds, not the legacy 600-second subtotal.
	if c.Duration != 775 || *c.Rows[0].Samples+*c.Rows[1].Samples != 400 {
		t.Fatal("full-window samples not preserved")
	}
	encoded, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(encoded), `"samples":300`) {
		t.Fatalf("bad browser model: %s %v", encoded, err)
	}
}

func TestContributionsEmptyFailedAndOld(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ref    string
		record artifactRecord
		zero   bool
	}{
		{"empty success", "n", testRecord(`{"status":"success","data":{"result":[]}}`), true},
		{"failed", "n", artifactRecord{}, false},
		{"missing reference", "", artifactRecord{}, false},
		{"malformed", "n", testRecord(`{"status":"success","data":{}}`), false},
		{"partial empty", "n", testRecord(`{"status":"success","warnings":["partial"],"data":{"result":[]}}`), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := contributionFixture()
			a.Workspaces[0].Metrics[0].NewSeries = tt.ref
			a.Records["n"] = tt.record
			c := analyzeContributions(a, analyzeArtifact(a))
			for _, r := range c.Rows {
				if tt.zero {
					requireNumber(t, r.NewSeries, 0)
				} else if r.NewSeries != nil {
					t.Fatal("unknown new series became measured")
				}
			}
			if tt.zero {
				requireNumber(t, c.Total, 0)
			} else if c.DefaultWeight != "runSeries" {
				t.Fatal("default did not fall back to series seen")
			}
		})
	}
	a := testArtifact()
	c := analyzeContributions(a, analyzeArtifact(a))
	for _, r := range c.Rows {
		if r.NewSeries != nil || r.Samples != nil {
			t.Fatal("old archive fabricated new measurements")
		}
	}
	a = contributionFixture()
	for _, ref := range []string{"i", "n", "s"} {
		a.Records[ref] = testRecord(`{"status":"success","data":{"result":[]}}`)
	}
	c = analyzeContributions(a, analyzeArtifact(a))
	if len(c.Rows) != 1 {
		t.Fatal("empty metric must retain coverage row")
	}
	requireNumber(t, c.Rows[0].NewSeries, 0)
}

func TestContributionsInvalidPoisonsMetric(t *testing.T) {
	for _, ref := range []string{"i", "n", "s"} {
		for _, value := range []string{"NaN", "+Inf", "-1", "1.5", "bad"} {
			a := contributionFixture()
			a.Records[ref] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[775,"1"]},{"metric":{"job":"b"},"value":[775,"` + value + `"]}]}}`)
			c := analyzeContributions(a, analyzeArtifact(a))
			for _, r := range c.Rows {
				v := map[string]*float64{"i": r.RunSeries, "n": r.NewSeries, "s": r.Samples}[ref]
				if v != nil {
					t.Fatalf("%s %s did not poison entire metric", ref, value)
				}
			}
			if len(c.Warnings) == 0 {
				t.Fatal("invalid metric lacks diagnostics")
			}
		}
	}
	for _, body := range []string{
		`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[775,"1"]},{"metric":{"JOB":"A"},"value":[775,"1"]}]}}`,
		`{"status":"success","data":{"result":[{"metric":{"job":"a","JOB":"a"},"value":[775,"1"]}]}}`,
		`{"status":"success","data":{"result":[{"metric":{"instance":"unexpected"},"value":[775,"1"]}]}}`,
		`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[600,"1"]}]}}`,
		`{"status":"success","data":{"result":[{"metric":{"job":"a"},"value":[775,"1e308"]},{"metric":{"job":"b"},"value":[775,"1e308"]}]}}`,
	} {
		v := readContribution("n", map[string]artifactRecord{"n": testRecord(body)}, 775)
		if v.complete || len(v.warnings) == 0 {
			t.Fatal("ambiguous vector accepted")
		}
		for _, g := range v.groups {
			if g.Count != nil {
				t.Fatal("ambiguous vector produced count")
			}
		}
	}
}

func TestContributionsSubsetDiscrepancy(t *testing.T) {
	a := contributionFixture()
	a.Records["n"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"A"},"value":[775,"9"]},{"metric":{"job":"missing"},"value":[775,"2"]}]}}`)
	c := analyzeContributions(a, analyzeArtifact(a))
	if len(c.Warnings) != 2 {
		t.Fatalf("expected both subset warnings: %v", c.Warnings)
	}
	requireNumber(t, c.Total, 11)
	requireNumber(t, c.Ranked[0].NewSeries, 9)
}

func TestContributionsPhysicalOverlapAndReplicas(t *testing.T) {
	// Two old and two newly seen physical labelsets share the SAME grouped
	// dimensions. Group subtraction would miss the new members. The renderer
	// must consume the collector's full-labelset set-difference result directly.
	a := contributionFixture()
	a.Workspaces[0].Metrics[0].Name = "latency_bucket"
	a.Records["i"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"a","prometheus":"writer-1","hostedcontrolplane":"literal-hcp"},"value":[775,"4"]},{"metric":{"job":"a","prometheus":"writer-2"},"value":[775,"4"]}]}}`)
	a.Records["n"] = testRecord(`{"status":"success","data":{"result":[{"metric":{"job":"A","prometheus":"WRITER-1","hostedcontrolplane":"literal-hcp"},"value":[775,"2"]},{"metric":{"job":"a","prometheus":"writer-2"},"value":[775,"2"]}]}}`)
	a.Records["s"] = testRecord(`{"status":"success","data":{"result":[]}}`)
	c := analyzeContributions(a, analyzeArtifact(a))
	requireNumber(t, c.Total, 4)
	if len(c.Rows) != 2 {
		t.Fatal("replicas deduplicated")
	}
	for _, r := range c.Ranked {
		requireNumber(t, r.RunSeries, 4)
		requireNumber(t, r.NewSeries, 2)
		requireNumber(t, r.Share, 0.5)
		if r.Collector != "OSS Prometheus" || !strings.Contains(r.Suggestion, "histogram dimensions") || !strings.Contains(r.Suggestion, "target churn") {
			t.Fatalf("missing evidence-based lead: %+v", r)
		}
	}
}

func TestContributionsPartialDoesNotImpute(t *testing.T) {
	a := contributionFixture()
	a.Records["n"] = testRecord(`{"status":"success","warnings":["partial response"],"data":{"result":[{"metric":{"job":"a"},"value":[775,"1"]}]}}`)
	a.Workspaces[0].Metrics = append(a.Workspaces[0].Metrics, artifactMetric{Name: "failed", Instant: "missing", NewSeries: "missing", Samples: "missing"})
	c := analyzeContributions(a, analyzeArtifact(a))
	requireNumber(t, c.Total, 1)
	if c.Measured != 1 || len(c.Rows) != 3 || c.Rows[1].NewSeries != nil || c.Rows[2].Samples != nil {
		t.Fatalf("partial coverage fabricated: %+v", c)
	}
	if len(c.Rows[0].Partial) != 1 || c.Rows[0].Partial[0] != "newSeries" {
		t.Fatal("partial response not carried into browser measure")
	}
	requireNumber(t, c.Ranked[0].Share, 1)
	var report bytes.Buffer
	if err := Render(&report, testJSON(t, a)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1/3 contribution rows measured", "partial response", "Where did the active series come from?", "Inspect target churn"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("report missing %q", want)
		}
	}
}

func TestWorkspaceGrowthUsesRunBoundaries(t *testing.T) {
	run := &artifactRun{Start: 125, End: 305}
	s := platformSeriesAnalysis{Interval: "PT1M", Values: []renderPoint{{0, finiteSum(900)}, {60, finiteSum(800)}, {120, finiteSum(10)}, {180, finiteSum(20)}, {240, finiteSum(15)}, {300, finiteSum(12)}, {360, finiteSum(999)}}}
	p := platformAnalysis{Series: []platformSeriesAnalysis{s}}
	g := analyzeWorkspaceGrowth("w", p, run)
	requireNumber(t, g.Baseline, 10)
	requireNumber(t, g.End, 12)
	requireNumber(t, g.Peak, 20)
	requireNumber(t, g.Delta, 2)
	if !g.Complete || g.BaselineTime != 120 || g.EndTime != 300 {
		t.Fatalf("wrong boundary pairing: %+v", g)
	}
	p.Series[0].Values[2].Value = nil
	g = analyzeWorkspaceGrowth("w", p, run)
	if g.Baseline != nil || g.Delta != nil || g.Peak != nil {
		t.Fatal("missing boundary used older padded endpoint")
	}
	p.Series[0].Values[2].Value = finiteSum(10)
	p.Series[0].Values[3].Value = nil
	g = analyzeWorkspaceGrowth("w", p, run)
	if g.Peak != nil || g.Complete {
		t.Fatal("incomplete peak was declared known")
	}
	requireNumber(t, g.Delta, 2) // Paired boundaries remain independently measured.
	run.End = 420
	g = analyzeWorkspaceGrowth("w", p, run)
	if g.End != nil || g.Delta != nil {
		t.Fatal("stale boundary minute accepted")
	}
}
