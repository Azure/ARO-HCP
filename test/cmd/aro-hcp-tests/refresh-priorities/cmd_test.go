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

package refreshpriorities

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/internal/priorityrefresh"
)

// run executes the command with the given arguments and returns stdout and stderr.
func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	command := NewCommand()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

// writeFile writes one test input file and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// catalogDocument renders a minimal openshift-tests-extension catalog.
func catalogDocument(t *testing.T, priorities map[string]int) string {
	t.Helper()
	type spec struct {
		Name          string              `json:"name"`
		Labels        map[string]struct{} `json:"labels"`
		CodeLocations []string            `json:"codeLocations"`
	}
	specs := []spec{}
	for name, priority := range priorities {
		labels := map[string]struct{}{"Critical": {}, "MIContainers:1": {}}
		if priority > 0 {
			labels["StartPriority:"+itoa(priority)] = struct{}{}
		}
		specs = append(specs, spec{Name: name, Labels: labels, CodeLocations: []string{"test/e2e/cluster_test.go:10"}})
	}
	document, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

// reportDocument renders a priority report with one scored and one insufficient row.
func reportDocument(t *testing.T, priority int) string {
	t.Helper()
	score := 60.0
	report := map[string]any{
		"schema_version": 1,
		"algorithm":      "environment-normalized-mean-v1",
		"selection": map[string]any{
			"origin": "https://example.invalid/ServiceLogs",
			"from":   "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z", "job_filter": "e2e-parallel",
		},
		"min_samples":           3,
		"global_median_minutes": 30,
		"environments":          []any{},
		"quality":               map[string]any{"selected_build_count": 3, "processed_build_count": 3, "green_build_count": 3, "exclusions": map[string]int{}},
		"tests": []any{
			map[string]any{"test_id": "id-1", "name": "suite should be faster", "status": "scored",
				"score_minutes": score, "proposed_priority": priority,
				"environments": []any{map[string]any{"environment": "dev", "eligible_build_count": 3,
					"eligible_sample_count": 9, "mean_minutes": score, "median_minutes": 20, "sufficient": true}}},
			map[string]any{"test_id": "id-2", "name": "suite should be sparse", "status": "insufficient-data",
				"score_minutes": nil, "proposed_priority": nil, "environments": []any{}},
		},
	}
	document, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return string(document)
}

// itoa renders a priority for the catalog document.
func itoa(value int) string {
	return strconv.Itoa(value)
}

func TestValidateRejectsInvalidInvocations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		substring string
	}{
		{name: "missing report", args: []string{"--catalog", "c.json"}, substring: "--report is required"},
		{name: "missing catalog", args: []string{"--report", "r.json"}, substring: "--catalog is required"},
		{name: "write with verify", args: []string{"--report", "r.json", "--catalog", "c.json", "--verify-catalog", "a.json", "--write"}, substring: "mutually exclusive"},
		{name: "positional arguments", args: []string{"--report", "r.json", "--catalog", "c.json", "extra"}, substring: "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := run(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
		})
	}
}

func TestVerifyAcceptsTheExpectedCatalogAndReportsChanges(t *testing.T) {
	report := writeFile(t, "report.json", reportDocument(t, 90))
	before := writeFile(t, "before.json", catalogDocument(t, map[string]int{"suite should be faster": 40, "suite should be sparse": 30}))
	after := writeFile(t, "after.json", catalogDocument(t, map[string]int{"suite should be faster": 90, "suite should be sparse": 30}))

	stdout, stderr, err := run(t, "--report", report, "--catalog", before, "--verify-catalog", after)
	if err != nil {
		t.Fatalf("verification failed: %v (%s)", err, stderr)
	}
	var summary struct {
		Changes []struct {
			Name        string `json:"name"`
			OldPriority int    `json:"old_priority"`
			NewPriority int    `json:"new_priority"`
		} `json:"changes"`
		Insufficient []string `json:"insufficient"`
		Unmatched    []string `json:"unmatched"`
		Applied      bool     `json:"applied"`
	}
	if err := json.Unmarshal([]byte(stdout), &summary); err != nil {
		t.Fatalf("decoding summary: %v\n%s", err, stdout)
	}
	if len(summary.Changes) != 1 || summary.Changes[0].Name != "suite should be faster" ||
		summary.Changes[0].OldPriority != 40 || summary.Changes[0].NewPriority != 90 {
		t.Fatalf("changes = %+v", summary.Changes)
	}
	if len(summary.Insufficient) != 1 || summary.Applied {
		t.Fatalf("summary = %+v", summary)
	}
	if !strings.Contains(stderr, "Verified the rebuilt catalog") {
		t.Fatalf("verification did not report success: %s", stderr)
	}
}

func TestVerifyRejectsAnUnexpectedCatalog(t *testing.T) {
	report := writeFile(t, "report.json", reportDocument(t, 90))
	before := writeFile(t, "before.json", catalogDocument(t, map[string]int{"suite should be faster": 40, "suite should be sparse": 30}))
	for name, priorities := range map[string]map[string]int{
		"unchanged-label.json": {"suite should be faster": 40, "suite should be sparse": 30},
		"dropped-spec.json":    {"suite should be faster": 90},
		"other-priority.json":  {"suite should be faster": 80, "suite should be sparse": 30},
	} {
		after := writeFile(t, name, catalogDocument(t, priorities))
		stdout, _, err := run(t, "--report", report, "--catalog", before, "--verify-catalog", after)
		if err == nil {
			t.Fatalf("%s was accepted: %s", name, stdout)
		}
	}
}

func TestVerifyRejectsInvalidInputDocuments(t *testing.T) {
	report := writeFile(t, "report.json", reportDocument(t, 90))
	before := writeFile(t, "before.json", catalogDocument(t, map[string]int{"suite should be faster": 40}))
	trailing := writeFile(t, "trailing.json", catalogDocument(t, map[string]int{"suite should be faster": 90})+"{}\n")
	if _, _, err := run(t, "--report", report, "--catalog", before, "--verify-catalog", trailing); err == nil {
		t.Fatal("accepted trailing JSON in the after catalog")
	}
	unknown := writeFile(t, "unknown.json", strings.Replace(reportDocument(t, 90), `"schema_version"`, `"unexpected": 1, "schema_version"`, 1))
	if _, _, err := run(t, "--report", unknown, "--catalog", before, "--verify-catalog", before); err == nil {
		t.Fatal("accepted an unknown report field")
	}
	nullEntry := writeFile(t, "null.json", "[null]\n")
	if _, _, err := run(t, "--report", report, "--catalog", nullEntry, "--verify-catalog", before); err == nil ||
		!strings.Contains(err.Error(), "null") {
		t.Fatalf("null catalog entry error = %v", err)
	}
}

func TestTableCountsUnmatchedReportRows(t *testing.T) {
	var output bytes.Buffer
	if err := writeTable(&output, priorityrefresh.Summary{
		Unchanged:    []string{"suite unchanged"},
		Insufficient: []string{"suite sparse"},
		Unmatched:    []string{"suite retired"},
	}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(output.String()), " "), "Reported rows 3") {
		t.Fatalf("reported rows exclude unmatched entries:\n%s", output.String())
	}
}
