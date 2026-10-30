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

package visualize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/go-logr/logr"

	"github.com/Azure/ARO-HCP/test/util/timing"
)

func TestLifecycleCoverageUsesOperationUnionNotPhases(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := func(name string, first, last int) StepTimingMetadata {
		return StepTimingMetadata{Name: name, StartedAt: start.Add(time.Duration(first) * time.Second), FinishedAt: start.Add(time.Duration(last) * time.Second)}
	}
	item := TestInfo{
		Identifier: []string{"test"}, StartedAt: start, FinishedAt: start.Add(20 * time.Second),
		CleanupStartedAt: start.Add(10 * time.Second), Outcome: "passed",
		Phases: []StepTimingMetadata{span("body", 0, 10), span("cleanup", 10, 20)},
		Steps:  []StepTimingMetadata{span("outer", -1, 6), span("nested", 2, 4), span("overlap", 5, 8), span("cross-boundary", 9, 12), span("beyond-root", 16, 30)},
	}
	rows := lifecycleRows(item)
	var gaps []StepTimingMetadata
	for _, row := range rows {
		if row.Kind == "gap" {
			gaps = append(gaps, row)
		}
		if row.StartedAt.Before(item.StartedAt) || row.FinishedAt.After(item.FinishedAt) {
			t.Fatalf("row was not clipped to root: %+v", row)
		}
	}
	if len(gaps) != 2 || gaps[0].StartedAt != start.Add(8*time.Second) || gaps[0].FinishedAt != start.Add(9*time.Second) || gaps[1].StartedAt != start.Add(12*time.Second) || gaps[1].FinishedAt != start.Add(16*time.Second) {
		t.Fatalf("expected body [8,9] and cleanup [12,16] gaps, got %+v", gaps)
	}
	if !strings.Contains(gaps[0].Name, "test work") || !strings.Contains(gaps[1].Name, "cleanup") {
		t.Fatalf("body and cleanup gaps must be distinguishable: %+v", gaps)
	}
	chart := testTimingChart([]TestInfo{item})
	if len(chart.MultiSeries) != 2 {
		t.Fatalf("phases and gaps must retain two series, got %d", len(chart.MultiSeries))
	}
	data := chart.MultiSeries[1].Data.([]opts.BarData)
	if data[0].Value != float64(20000) || len(data) != 1+len(rows) {
		t.Fatalf("root must cover complete lifecycle and all rows: %+v", data)
	}
	// Removing all operations exposes the whole body and cleanup, even though phase bars cover both.
	item.Steps = nil
	var uncovered time.Duration
	for _, row := range lifecycleRows(item) {
		if row.Kind == "gap" {
			uncovered += row.FinishedAt.Sub(row.StartedAt)
		}
	}
	if uncovered != 20*time.Second {
		t.Fatalf("phase bars incorrectly counted as operation coverage: %s", uncovered)
	}
}

func TestLifecycleSummarySelectsSuccessfulIndependentSamples(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := func(kind, name string, first, last int) StepTimingMetadata {
		return StepTimingMetadata{Name: name, Kind: kind, StartedAt: start.Add(time.Duration(first) * time.Second), FinishedAt: start.Add(time.Duration(last) * time.Second)}
	}
	passed := TestInfo{
		StartedAt: start, FinishedAt: start.Add(40 * time.Second), CleanupStartedAt: start.Add(20 * time.Second), Outcome: "passed",
		Steps: []StepTimingMetadata{
			// Separate resource operations remain separate samples even when fully contained.
			span(timing.KindCreateCluster, "cluster-a", 0, 10), span(timing.KindCreateCluster, "cluster-b", 2, 5),
			span(timing.KindCreateNodePool, "nodepool-a", 2, 6), span(timing.KindCreateNodePool, "nodepool-b", 4, 9),
			span(timing.KindDeleteCluster, "cluster-a", 22, 26), span(timing.KindResourceGroupCleanup, "group-a", 21, 38),
		},
	}
	incomplete := span(timing.KindCreateCluster, "cluster-c", 18, 20)
	incomplete.Incomplete = true
	passed.Steps = append(passed.Steps, incomplete)
	// A misleading label without an explicit kind must not create a deletion sample.
	passed.Steps = append(passed.Steps, StepTimingMetadata{Name: "delete-cluster", StartedAt: start, FinishedAt: start.Add(time.Second)})
	times := []TestInfo{passed}
	for _, outcome := range []string{"failed", "timedout", "", "interrupted"} {
		item := passed
		item.Outcome = outcome
		times = append(times, item)
	}
	summary := summarizeLifecycle(times)
	if summary.Passed != 1 || summary.Failed != 1 || summary.TimedOut != 1 || summary.Unknown != 1 || summary.Other != 1 {
		t.Fatalf("unexpected outcome selection: %+v", summary)
	}
	for i, want := range []struct {
		count int
		total time.Duration
	}{
		{2, 13 * time.Second}, {2, 9 * time.Second}, {1, 4 * time.Second}, {1, 17 * time.Second}, {1, 20 * time.Second},
	} {
		got := summary.Rows[i]
		if got.Count != want.count || got.Total != want.total {
			t.Errorf("category %q: got count %d total %s, want %d %s", got.Name, got.Count, got.Total, want.count, want.total)
		}
	}
	if summary.Rows[0].Mean() != "6.5s" || summary.Rows[1].Mean() != "4.5s" || summary.Rows[0].Incomplete != 1 {
		t.Fatalf("mean or incomplete sample selection is incorrect: %+v", summary.Rows[0])
	}
	if got := summarizeLifecycle([]TestInfo{{Outcome: "passed"}}).Rows[4]; got.Count != 0 || got.Mean() != "N/A" {
		t.Fatalf("missing cleanup boundary cannot become a zero-second sample: %+v", got)
	}
	passed.Phases = []StepTimingMetadata{{Name: "cleanup interrupted", StartedAt: start.Add(20 * time.Second), FinishedAt: start.Add(40 * time.Second), Incomplete: true}}
	if got := summarizeLifecycle([]TestInfo{passed}).Rows[4]; got.Count != 0 || got.Incomplete != 1 {
		t.Fatalf("interrupted cleanup cannot be a success sample: %+v", got)
	}
}

func TestLifecycleGapSplitsAtCleanupAndPhaseBoundaries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := TestInfo{StartedAt: start, FinishedAt: start.Add(10 * time.Second), CleanupStartedAt: start.Add(4 * time.Second), Phases: []StepTimingMetadata{
		{Name: "debug", StartedAt: start.Add(4 * time.Second), FinishedAt: start.Add(6 * time.Second)},
	}}
	var gaps []StepTimingMetadata
	for _, row := range lifecycleRows(item) {
		if row.Kind == "gap" {
			gaps = append(gaps, row)
		}
	}
	if len(gaps) != 3 || gaps[0].FinishedAt != item.CleanupStartedAt || !strings.Contains(gaps[1].Name, "debug") || gaps[2].StartedAt != start.Add(6*time.Second) {
		t.Fatalf("uncovered root time must be partitioned, not duplicated per phase: %+v", gaps)
	}
}

func TestVisualizeLoadsLifecycleAndLegacyArtifacts(t *testing.T) {
	input, output := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{
		"timing-metadata-new.yaml": `identifier: ['</script><img src=x onerror=alert(1)>__f__']
startedAt: '2026-01-01T00:00:00.000000001Z'
finishedAt: '2026-01-01T00:00:10.000000001Z'
cleanupStartedAt: '2026-01-01T00:00:06.000000001Z'
outcome: passed
phases:
- name: '<b>cleanup</b>'
  kind: resource-cleanup
  startedAt: '2026-01-01T00:00:06.000000001Z'
  finishedAt: '2026-01-01T00:00:10.000000001Z'
steps:
- name: create
  kind: create-cluster
  startedAt: '2026-01-01T00:00:00.000000001Z'
  finishedAt: '2026-01-01T00:00:02.000000001Z'
- name: interrupted
  kind: delete-cluster
  incomplete: true
  startedAt: '2026-01-01T00:00:08.000000001Z'
  finishedAt: '2026-01-01T00:00:10.000000001Z'
`,
		"timing-metadata-old.yaml": `identifier: [legacy]
startedAt: '2026-01-01T00:00:00Z'
finishedAt: '2026-01-01T00:00:03Z'
steps:
- name: create-cluster
  startedAt: '2026-01-01T00:00:00Z'
  finishedAt: '2026-01-01T00:00:03Z'
`,
	} {
		if err := os.WriteFile(filepath.Join(input, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	validated, err := (&RawOptions{TimingInputDir: input, OutputDir: output}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	options, err := validated.Complete(logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Times) != 2 {
		t.Fatalf("expected both artifact generations, got %d", len(options.Times))
	}
	var current TestInfo
	for _, item := range options.Times {
		if item.Outcome == "passed" {
			current = item
		}
	}
	if current.CleanupStartedAt.Nanosecond() != 1 || len(current.Phases) != 1 || current.Phases[0].Kind != "resource-cleanup" || len(current.Steps) != 2 || current.Steps[0].Kind != timing.KindCreateCluster || !current.Steps[1].Incomplete {
		t.Fatalf("lifecycle fields did not survive loading: %+v", current)
	}
	if err := options.Visualize(logr.NewContext(context.Background(), logr.Discard())); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(output, "e2e-timelines_spyglass_timing-metadata.html"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(html)
	if !strings.Contains(text, "Successful-test operation averages") || !strings.Contains(text, "<td>create-cluster</td><td>2s</td><td>1</td>") || !strings.Contains(text, "1 unknown outcome") {
		t.Fatal("visible summary must show passed-only means, sample counts, and unknown legacy outcomes")
	}
	if strings.Contains(text, "<img src=x") || strings.Contains(text, "<b>cleanup</b>") || !strings.Contains(text, `\u003cb\u003ecleanup\u003c/b\u003e`) {
		t.Fatal("hostile phase and test labels must remain escaped data")
	}
	if !strings.Contains(testTimingChart(options.Times).Subtitle, "1/2 include full cleanup") {
		t.Fatal("mixed legacy metadata must not claim full-lifecycle elapsed")
	}
}
