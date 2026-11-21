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

package prioritiessummary

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
)

// writeFile writes one fixture file and returns its path.
func writeFile(t *testing.T, dir, name string, content any) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// reportFixture builds a report that passes command validation.
func reportFixture() citests.PriorityReport {
	median := 29.3
	latest := time.Date(2026, 11, 18, 7, 37, 28, 0, time.UTC)
	return citests.PriorityReport{
		SchemaVersion:       citests.ReportSchemaVersion,
		Algorithm:           citests.AlgorithmEnvironmentNormalizedMean,
		Selection:           citests.Selection{Origin: "https://kusto/ServiceLogs", From: time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), To: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC), JobFilter: "e2e-parallel"},
		MinSamples:          3,
		GlobalMedianMinutes: &median,
		Environments:        []citests.EnvironmentSummary{{Environment: "dev", EligibleBuildCount: 6, EligibleSampleCount: 60, MedianMinutes: 28, LatestEligibleCompletion: &latest}},
	}
}

// render runs the command phases for one raw invocation.
func render(out io.Writer, raw *RawOptions) error {
	validated, err := raw.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete(out)
	if err != nil {
		return err
	}
	return completed.Run()
}

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts RawOptions
		want string
	}{
		{name: "missing output", opts: RawOptions{Note: "n"}, want: "--output is required"},
		{name: "report without changes", opts: RawOptions{OutputPath: "o", ReportPath: "r"}, want: "must be given together"},
		{name: "notice without note", opts: RawOptions{OutputPath: "o"}, want: "requires --note"},
		{name: "notice only", opts: RawOptions{OutputPath: "o", Note: "n"}},
		{name: "full", opts: RawOptions{OutputPath: "o", ReportPath: "r", ChangesPath: "c"}},
		{name: "body without reports", opts: RawOptions{OutputPath: "o", Note: "n", PublicationBody: "b", JobName: "j", BaseCommit: "c"}, want: "--publication-body requires --report and --changes"},
		{name: "body without job", opts: RawOptions{OutputPath: "o", ReportPath: "r", ChangesPath: "c", PublicationBody: "b", BaseCommit: "c"}, want: "--publication-body requires --job-name"},
		{name: "body without base", opts: RawOptions{OutputPath: "o", ReportPath: "r", ChangesPath: "c", PublicationBody: "b", JobName: "j"}, want: "--publication-body requires --base-commit"},
		{name: "body complete", opts: RawOptions{OutputPath: "o", ReportPath: "r", ChangesPath: "c", PublicationBody: "b", JobName: "j", BaseCommit: "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.opts.Validate()
			if tc.want == "" {
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

func TestRunWritesSummaryFromReports(t *testing.T) {
	dir := t.TempDir()
	report := writeFile(t, dir, "report.json", reportFixture())
	changes := writeFile(t, dir, "changes.json", map[string]any{
		"changes": []map[string]any{{
			"name": "spec one", "path": "test/e2e/one.go", "line": 12,
			"old_priority": 50, "new_priority": 40, "score_minutes": 29.2,
			"eligible_build_count": 477, "environments": []string{"dev"},
		}},
		"unchanged": []string{}, "insufficient": []string{}, "unmatched": []string{"retired spec"},
		"applied": false,
	})
	output := filepath.Join(dir, "summary.html")

	if err := render(io.Discard, &RawOptions{ReportPath: report, ChangesPath: changes, Note: "read-only", OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"read-only", "<h2>Coverage</h2>", "<h2>Proposed changes (1)</h2>", "one.go:12", "retired spec"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("summary is missing %q", want)
		}
	}
}

func TestRunNoticeWithoutReports(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "summary.html")
	if err := render(io.Discard, &RawOptions{Note: "Publication skipped: an open generated pull request exists", OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Publication skipped") {
		t.Fatalf("notice missing from summary:\n%s", page)
	}
}

func TestRunRejectsForeignReport(t *testing.T) {
	dir := t.TempDir()
	report := reportFixture()
	report.Algorithm = "per-run-median-v1"
	reportPath := writeFile(t, dir, "report.json", report)
	changes := writeFile(t, dir, "changes.json", map[string]any{"changes": []any{}, "applied": false})
	err := render(io.Discard, &RawOptions{ReportPath: reportPath, ChangesPath: changes, OutputPath: filepath.Join(dir, "s.html")})
	if err == nil || !strings.Contains(err.Error(), "algorithm") {
		t.Fatalf("err = %v, want algorithm rejection", err)
	}
}

func TestRunRendersPublicationLink(t *testing.T) {
	dir := t.TempDir()
	report := writeFile(t, dir, "report.json", reportFixture())
	changes := writeFile(t, dir, "changes.json", map[string]any{"changes": []any{}, "applied": true})
	output := filepath.Join(dir, "summary.html")

	raw := &RawOptions{
		ReportPath: report, ChangesPath: changes, Note: "Published one pull request.",
		PublicationURL: "https://github.com/Azure/ARO-HCP/pull/9000", OutputPath: output,
	}
	if err := render(io.Discard, raw); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `Published pull request: <a href="https://github.com/Azure/ARO-HCP/pull/9000">`) {
		t.Fatalf("publication link missing from the page:\n%s", page)
	}
}

func TestRunRejectsMalformedReports(t *testing.T) {
	dir := t.TempDir()
	changes := writeFile(t, dir, "changes.json", map[string]any{"changes": []any{}, "applied": false})
	output := filepath.Join(dir, "summary.html")

	foreign := reportFixture()
	foreign.SchemaVersion = 99
	for _, tc := range []struct {
		name    string
		content any
		raw     string
		want    string
	}{
		{name: "schema version", content: foreign, want: "schema version"},
		{name: "unknown field", raw: `{"schema_version":1,"algorithm":"environment-normalized-mean-v1","unexpected":true}`, want: "unknown field"},
		{name: "trailing data", raw: `{"schema_version":1,"algorithm":"environment-normalized-mean-v1"} {"extra":true}`, want: "exactly one JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.raw != "" {
				path = filepath.Join(dir, tc.name+".json")
				if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				path = writeFile(t, dir, tc.name+".json", tc.content)
			}
			err := render(io.Discard, &RawOptions{ReportPath: path, ChangesPath: changes, OutputPath: output})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(output); statErr == nil {
				t.Fatalf("a rejected report still produced a summary")
			}
		})
	}
}

func TestRunWritesPublicationBody(t *testing.T) {
	dir := t.TempDir()
	report := writeFile(t, dir, "report.json", reportFixture())
	changes := writeFile(t, dir, "changes.json", map[string]any{
		"changes": []map[string]any{{
			"name": "spec one", "path": "test/e2e/one.go", "line": 12,
			"old_priority": 50, "new_priority": 40, "score_minutes": 29.2,
			"eligible_build_count": 477, "environments": []string{"dev"},
		}},
		"applied": false,
	})
	body := filepath.Join(dir, "body.md")
	raw := &RawOptions{
		ReportPath: report, ChangesPath: changes, OutputPath: filepath.Join(dir, "summary.html"),
		PublicationBody: body, JobName: "periodic-ci-Azure-ARO-HCP-main-e2e-test-priorities-refresh",
		BuildID: "42", BaseCommit: "abc123",
	}
	if err := render(io.Discard, raw); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Changed specs", "| spec one | 50 | 40 | 29.2 | 477 | dev |", "(build 42)"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("publication body is missing %q", want)
		}
	}
}

func TestCommandWiresEveryFlag(t *testing.T) {
	dir := t.TempDir()
	report := writeFile(t, dir, "report.json", reportFixture())
	changes := writeFile(t, dir, "changes.json", map[string]any{
		"changes": []map[string]any{{
			"name": "spec one", "path": "test/e2e/one.go", "line": 12,
			"old_priority": 50, "new_priority": 40, "score_minutes": 29.2,
			"eligible_build_count": 477, "environments": []string{"dev"},
		}},
		"applied": false,
	})
	page := filepath.Join(dir, "summary.html")
	body := filepath.Join(dir, "body.md")
	command := NewCommand()
	command.SetArgs([]string{
		"--report", report, "--changes", changes,
		"--output", page, "--publication-body", body,
		"--note", "Published one pull request.",
		"--publication-url", "https://github.com/Azure/ARO-HCP/pull/9000",
		"--job-name", "periodic-ci-Azure-ARO-HCP-main-e2e-test-priorities-refresh",
		"--build-id", "42", "--job-url", "https://prow.example/build/42", "--base-commit", "abc123",
	})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(build 42)", "https://prow.example/build/42", "`abc123`", "| spec one | 50 | 40 |"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("publication body is missing %q", want)
		}
	}
	rendered, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "Published one pull request.") ||
		!strings.Contains(string(rendered), "https://github.com/Azure/ARO-HCP/pull/9000") {
		t.Error("the summary page did not receive the note and publication URL")
	}
}
