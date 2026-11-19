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

package prioritiesrun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/internal/priorityrefresh"
	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// testRepo returns a checkout containing the directory validation expects.
func testRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "test", "e2e"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// validOptions returns options that pass Validate.
func validOptions(t *testing.T) *options {
	t.Helper()
	return &options{
		cluster: "hcp-dev-us-2", region: "eastus2", database: "ServiceLogs",
		since: 720 * time.Hour, jobFilter: "e2e-parallel", timeout: 10 * time.Minute,
		minSamples: 3, repo: testRepo(t), maxAge: 7 * 24 * time.Hour,
		now: func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) },
		out: &bytes.Buffer{},
	}
}

func TestValidateRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*options)
		want string
	}{
		{name: "cluster", edit: func(o *options) { o.cluster = "bad/name" }, want: "--kusto and --region"},
		{name: "database", edit: func(o *options) { o.database = "" }, want: "--database"},
		{name: "timeout", edit: func(o *options) { o.timeout = 0 }, want: "--timeout"},
		{name: "min samples", edit: func(o *options) { o.minSamples = 1 }, want: "--min-samples"},
		{name: "max age", edit: func(o *options) { o.maxAge = 0 }, want: "--max-age"},
		{name: "repo", edit: func(o *options) { o.repo = t.TempDir() }, want: "test/e2e"},
		{name: "window", edit: func(o *options) { o.from = "2026-11-19T00:00:00Z"; o.to = "2026-11-18T00:00:00Z" }, want: "--from < --to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := validOptions(t)
			tc.edit(opts)
			if err := opts.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := validOptions(t).Validate(); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
}

func TestCheckCoverageRefusesThinOrStaleEnvironments(t *testing.T) {
	windowEnd := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	fresh := windowEnd.Add(-24 * time.Hour)
	stale := windowEnd.Add(-30 * 24 * time.Hour)
	environments := func() []citests.EnvironmentSummary {
		return []citests.EnvironmentSummary{
			{Environment: citests.EnvironmentDev, EligibleBuildCount: 5, LatestEligibleCompletion: &fresh},
			{Environment: citests.EnvironmentInt, EligibleBuildCount: 5, LatestEligibleCompletion: &fresh},
			{Environment: citests.EnvironmentStg, EligibleBuildCount: 5, LatestEligibleCompletion: &fresh},
			{Environment: citests.EnvironmentProd, EligibleBuildCount: 5, LatestEligibleCompletion: &fresh},
		}
	}
	for _, tc := range []struct {
		name   string
		edit   func([]citests.EnvironmentSummary) []citests.EnvironmentSummary
		reject bool
		want   string
	}{
		{name: "complete coverage", edit: func(e []citests.EnvironmentSummary) []citests.EnvironmentSummary { return e }},
		{name: "missing environment", edit: func(e []citests.EnvironmentSummary) []citests.EnvironmentSummary { return e[:3] }, reject: true, want: "prod"},
		{name: "thin environment", edit: func(e []citests.EnvironmentSummary) []citests.EnvironmentSummary {
			e[1].EligibleBuildCount = 2
			return e
		}, reject: true, want: "int has 2"},
		{name: "stale environment", edit: func(e []citests.EnvironmentSummary) []citests.EnvironmentSummary {
			e[2].LatestEligibleCompletion = &stale
			return e
		}, reject: true, want: "stg last had"},
		{name: "no completion", edit: func(e []citests.EnvironmentSummary) []citests.EnvironmentSummary {
			e[0].LatestEligibleCompletion = nil
			return e
		}, reject: true, want: "no eligible completion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := validOptions(t)
			opts.report = citests.PriorityReport{
				Selection:    citests.Selection{To: windowEnd},
				Environments: tc.edit(environments()),
			}
			err := opts.checkCoverage()
			if !tc.reject {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPrintTableRendersTheDecision(t *testing.T) {
	opts := validOptions(t)
	median := 29.4
	latest := time.Date(2026, 11, 19, 6, 0, 0, 0, time.UTC)
	opts.report = citests.PriorityReport{
		Selection:           citests.Selection{From: latest.Add(-720 * time.Hour), To: latest},
		GlobalMedianMinutes: &median,
		Environments: []citests.EnvironmentSummary{
			{Environment: citests.EnvironmentDev, EligibleBuildCount: 6, EligibleSampleCount: 60, MedianMinutes: 28, LatestEligibleCompletion: &latest},
		},
	}
	opts.summary = priorityrefresh.Summary{Changes: []priorityrefresh.Change{{
		Name: "suite should be faster", Path: "test/e2e/cluster.go", Line: 42,
		OldPriority: 50, NewPriority: 40, ScoreMinutes: 29.2, EligibleBuildCount: 477,
		Environments: []string{"dev"},
	}}, Unchanged: []string{"a"}, Insufficient: []string{"b"}, Unmatched: []string{"c", "d"}}
	out := &bytes.Buffer{}
	opts.out = out
	if err := opts.printTable(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"median 29.4m", "Coverage  dev", "cluster.go:42", "50 to 40", "29.2m", "suite should be faster",
		"1 changed, 1 unchanged, 1 insufficient, 2 unmatched", "pass --write",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table is missing %q:\n%s", want, out.String())
		}
	}
}

func TestValidateKeepsClientEndpointFreeOfTheDatabase(t *testing.T) {
	opts := validOptions(t)
	if err := opts.Validate(); err != nil {
		t.Fatal(err)
	}
	if opts.endpoint == nil {
		t.Fatal("endpoint was not resolved")
	}
	if opts.endpoint.Path != "" {
		t.Errorf("client endpoint carries a path %q", opts.endpoint.Path)
	}
	if !strings.Contains(opts.selection.Origin, "/ServiceLogs") {
		t.Errorf("selection origin lost the database: %q", opts.selection.Origin)
	}
}

func TestCoverageFailureKeepsArtifactsAndTable(t *testing.T) {
	opts := validOptions(t)
	opts.artifactDir = t.TempDir()
	stale := filepath.Join(opts.artifactDir, "priority-changes.json")
	if err := os.WriteFile(stale, []byte(`{"changes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	windowEnd := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	fresh := windowEnd.Add(-time.Hour)
	opts.report = citests.PriorityReport{
		Selection: citests.Selection{From: windowEnd.Add(-720 * time.Hour), To: windowEnd},
		Environments: []citests.EnvironmentSummary{
			{Environment: citests.EnvironmentDev, EligibleBuildCount: 6, LatestEligibleCompletion: &fresh},
		},
	}
	out := &bytes.Buffer{}
	opts.out = out

	cause := opts.checkCoverage()
	if cause == nil {
		t.Fatal("coverage should have been refused")
	}
	if err := opts.reportCoverageFailure(cause); err == nil {
		t.Fatal("the gate error must still be returned")
	}
	for _, name := range []string{"priority-scores.json", "priorities-summary.html"} {
		if _, err := os.Stat(filepath.Join(opts.artifactDir, name)); err != nil {
			t.Errorf("missing artifact %s: %v", name, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a refused run left the previous change summary in place")
	}
	page, err := os.ReadFile(filepath.Join(opts.artifactDir, "priorities-summary.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Coverage insufficient") {
		t.Errorf("the page does not explain the refusal:\n%s", page)
	}
	if !strings.Contains(out.String(), "Coverage  dev") {
		t.Errorf("the coverage table was not printed:\n%s", out.String())
	}
}
