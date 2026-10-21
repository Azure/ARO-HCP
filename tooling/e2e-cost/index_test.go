// Copyright 2026 Microsoft Corporation
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cost

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSummarizeRun(t *testing.T) {
	run := SummarizeRun(renderFixture(), "123.html")
	// renderFixture: infra 1.75+.5-.25-.5+0+.125 = 1.625, tests .375.
	if run.InfraUSD != 1.625 || run.TestsUSD != .375 || run.NetUSD != 2 || !run.Complete || run.PR != "42" || run.BuildID != "123" || run.Report != "123.html" {
		t.Fatalf("unexpected summary: %+v", run)
	}
	partial := renderFixture()
	partial.Groups[1].BillingStatus = "partial"
	if SummarizeRun(partial, "x.html").Complete {
		t.Fatal("a partially billed group must mark the run incomplete")
	}
	failed := renderFixture()
	failed.Diagnose("error", "billing", "billing", "failed")
	if SummarizeRun(failed, "x.html").Complete {
		t.Fatal("collection errors must mark the run incomplete")
	}
}

func TestWriteDayIndexOrderAndValidation(t *testing.T) {
	runs := []DayRun{
		{Report: "1.json.gz", BuildID: "1", PR: "<script>alert(1)</script>", NetUSD: 5, Complete: true},
		{Report: "2.json.gz", BuildID: "2", NetUSD: 50, Complete: false},
	}
	var out bytes.Buffer
	if err := WriteDayIndex(&out, "2026-10-19", runs); err != nil {
		t.Fatal(err)
	}
	var index DayIndex
	if err := json.Unmarshal(out.Bytes(), &index); err != nil {
		t.Fatal(err)
	}
	if index.Day != "2026-10-19" || len(index.Runs) != 2 || index.Runs[0].BuildID != "2" || index.Summary.NetUSD != 55 || index.Summary.Incomplete != 1 {
		t.Fatalf("day index must list runs most expensive first with totals: %+v", index)
	}
	if index.Runs[1].PR != "<script>alert(1)</script>" || strings.Contains(out.String(), "<script>") {
		t.Fatal("snapshot strings must round trip as escaped JSON")
	}
	if err := WriteDayIndex(&out, "yesterday", runs); err == nil {
		t.Fatal("invalid day must be rejected")
	}
	if err := WriteDayIndex(&out, "2026-10-19", []DayRun{{NetUSD: math.NaN()}}); err == nil {
		t.Fatal("non-finite cost must be rejected")
	}
}

func TestRenderViewerSharesReportAssets(t *testing.T) {
	var out bytes.Buffer
	if err := RenderViewer(&out); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"function initCostReport(data)", reportCSS[:200], `id="licenses" type="application/json"`, `fetchJSON("manifest.json")`} {
		if !strings.Contains(page, want) {
			t.Fatalf("viewer missing %q", want[:min(len(want), 60)])
		}
	}
	if strings.Count(page, echartsRuntime[:200]) != 1 {
		t.Fatal("viewer must embed the chart runtime exactly once")
	}
	match := regexp.MustCompile(`(?s)<script id="licenses" type="application/json">(.*?)</script>`).FindStringSubmatch(page)
	var licenses map[string]string
	if len(match) != 2 || json.Unmarshal([]byte(match[1]), &licenses) != nil || licenses["notice"] != echartsNotice {
		t.Fatal("license notices must be embedded as JSON")
	}
}

func TestManifestUpsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	manifest, err := ReadManifest(path)
	if err != nil || len(manifest.Days) != 0 {
		t.Fatalf("missing manifest must read as empty: %+v %v", manifest, err)
	}
	manifest.Upsert(DaySummary{Day: "2026-10-18", Runs: 1})
	manifest.Upsert(DaySummary{Day: "2026-10-19", Runs: 2})
	manifest.Upsert(DaySummary{Day: "2026-10-18", Runs: 3, NetUSD: 7}) // rerun refreshes
	if len(manifest.Days) != 2 || manifest.Days[0].Day != "2026-10-19" || manifest.Days[1].Runs != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest.Days)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"days":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err == nil {
		t.Fatal("unsupported manifest version must be rejected")
	}
}
