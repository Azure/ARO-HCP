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

package priorityrefresh

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	et "github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	otesets "github.com/openshift-eng/openshift-tests-extension/pkg/util/sets"

	"github.com/Azure/ARO-HCP/test/pkg/citests"
	"github.com/Azure/ARO-HCP/test/util/labels"
)

const standaloneSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("suite", func() {
	// keeps this decorator order
	It("should replace an existing priority",
		labels.RequireNothing,
		labels.MIContainers(1),
		labels.StartPriority(40),
		func(ctx context.Context) {
			// body comment stays
			_ = ctx
		})

	It("should add a missing priority",
		labels.MIContainers(0),
		func(ctx context.Context) {
			_ = ctx
		})

	It("should remove a priority",
		labels.MIContainers(2),
		labels.StartPriority(50),
		func(ctx context.Context) {
			_ = ctx
		})

	It("should keep an unchanged priority",
		labels.MIContainers(3),
		labels.StartPriority(30),
		func(ctx context.Context) {
			_ = ctx
		})
})
`

const tableSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("table", func() {
	DescribeTable("should create versions",
		labels.MIContainers(1),
		func(ctx context.Context, version string) {
			_ = ctx
			_ = version
		},
		Entry("version 4.20", labels.StartPriority(50), "4.20"),
		Entry("version 4.21", "4.21"),
		Entry("version 4.22", labels.StartPriority(50), "4.22"),
	)
})
`

const sharedTableSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("shared", func() {
	DescribeTable("should share a priority",
		labels.StartPriority(40),
		func(ctx context.Context, version string) {
			_ = ctx
			_ = version
		},
		Entry("version 4.20", "4.20"),
		Entry("version 4.21", "4.21"),
	)
})
`

// testRepo writes a temporary checkout with the given files.
func testRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	for name, content := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

// lineOf returns the 1-based line of the first line containing needle.
func lineOf(t *testing.T, repo, relPath, needle string) int {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("%s has no line containing %q", relPath, needle)
	return 0
}

// catalogSpec builds a catalog entry with an optional declared priority.
func catalogSpec(name string, priority int, locations ...string) *et.ExtensionTestSpec {
	declared := otesets.New[string]("Critical", "MIContainers:1")
	if priority > 0 {
		declared.Insert(fmt.Sprintf("%s%d", labels.StartPriorityLabelPrefix, priority))
	}
	return &et.ExtensionTestSpec{Name: name, Labels: declared, CodeLocations: locations}
}

// scoredRow builds a report row carrying a proposal.
func scoredRow(name, id string, priority int, score float64) citests.TestReport {
	builds := 3
	mean := score
	return citests.TestReport{
		TestID: id, Name: name, Status: citests.StatusScored,
		ScoreMinutes: &score, ProposedPriority: &priority,
		Environments: []citests.TestEnvironmentStats{{
			Environment: citests.EnvironmentDev, EligibleBuildCount: builds, EligibleSampleCount: builds,
			MeanMinutes: mean, MedianMinutes: 1, Sufficient: true,
		}},
	}
}

// insufficientRow builds a report row without a proposal.
func insufficientRow(name, id string) citests.TestReport {
	return citests.TestReport{TestID: id, Name: name, Status: citests.StatusInsufficientData}
}

// testReport builds a valid report around the given rows.
func testReport(rows ...citests.TestReport) citests.PriorityReport {
	return citests.PriorityReport{SchemaVersion: citests.ReportSchemaVersion, Algorithm: citests.AlgorithmEnvironmentNormalizedMean, Tests: rows}
}

// effectivePriority reads one declaration's priority from a source file.
func effectivePriority(t *testing.T, repo, relPath, name string) int {
	t.Helper()
	file, err := loadFile(repo, filepath.Join(repo, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	leaf, ok := file.locateByName(name)
	if !ok {
		t.Fatalf("%s no longer declares %q", relPath, name)
	}
	call, err := file.resolve(leaf)
	if err != nil {
		t.Fatal(err)
	}
	return call.value
}

// mustContain fails when the file lost a fragment the edit had to preserve.
func mustContain(t *testing.T, repo, relPath string, fragments ...string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("%s lost %q:\n%s", relPath, fragment, content)
		}
	}
}

func TestPlanEditsStandaloneIt(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	location := "test/e2e/standalone.go:"
	before := et.ExtensionTestSpecs{
		catalogSpec("suite should replace an existing priority", 40, location+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))),
		catalogSpec("suite should add a missing priority", 0, location+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should add a missing priority"))),
		catalogSpec("suite should remove a priority", 50, location+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should remove a priority"))),
		catalogSpec("suite should keep an unchanged priority", 30, location+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should keep an unchanged priority"))),
	}
	report := testReport(
		scoredRow("suite should replace an existing priority", "id-replace", 90, 60),
		scoredRow("suite should add a missing priority", "id-add", 20, 15),
		scoredRow("suite should remove a priority", "id-remove", 0, 5),
		scoredRow("suite should keep an unchanged priority", "id-keep", 30, 30),
		insufficientRow("suite should be retired", "id-retired"),
	)
	changes, err := Plan(repo, before, report)
	if err != nil {
		t.Fatal(err)
	}
	if changes.Len() != 3 {
		t.Fatalf("planned %d changes: %+v", changes.Len(), changes.Summary(false))
	}
	summary := changes.Summary(false)
	if len(summary.Insufficient) != 1 || summary.Insufficient[0] != "suite should be retired" {
		t.Fatalf("insufficient rows = %+v", summary.Insufficient)
	}
	if len(summary.Unchanged) != 1 || summary.Unchanged[0] != "suite should keep an unchanged priority" {
		t.Fatalf("unchanged rows = %+v", summary.Unchanged)
	}
	if err := Apply(changes); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{
		"suite should replace an existing priority": 90,
		"suite should add a missing priority":       20,
		"suite should remove a priority":            0,
		"suite should keep an unchanged priority":   30,
	} {
		if got := effectivePriority(t, repo, "test/e2e/standalone.go", name); got != want {
			t.Errorf("%q priority = %d, want %d", name, got, want)
		}
	}
	mustContain(t, repo, "test/e2e/standalone.go",
		"// keeps this decorator order", "// body comment stays", "labels.RequireNothing", "labels.MIContainers(1)",
		"labels.MIContainers(0)", "labels.MIContainers(2)", "labels.MIContainers(3)")
	if count := strings.Count(mustRead(t, repo, "test/e2e/standalone.go"), "StartPriority("); count != 3 {
		t.Fatalf("file has %d StartPriority calls, want 3", count)
	}
}

func TestPlanEditsDescribeTableEntries(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/table.go": tableSource})
	path := "test/e2e/table.go"
	before := et.ExtensionTestSpecs{
		catalogSpec("table should create versions version 4.20", 50, path+":"+itoa(lineOf(t, repo, path, `Entry("version 4.20"`))),
		catalogSpec("table should create versions version 4.21", 0, path+":"+itoa(lineOf(t, repo, path, `Entry("version 4.21"`))),
		catalogSpec("table should create versions version 4.22", 50, path+":"+itoa(lineOf(t, repo, path, `Entry("version 4.22"`))),
	}
	report := testReport(
		scoredRow("table should create versions version 4.20", "id-420", 90, 60),
		scoredRow("table should create versions version 4.21", "id-421", 40, 30),
		scoredRow("table should create versions version 4.22", "id-422", 0, 5),
	)
	changes, err := Plan(repo, before, report)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(changes); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{
		"table should create versions version 4.20": 90,
		"table should create versions version 4.21": 40,
		"table should create versions version 4.22": 0,
	} {
		if got := effectivePriority(t, repo, path, name); got != want {
			t.Errorf("%q priority = %d, want %d", name, got, want)
		}
	}
	mustContain(t, repo, path, `"4.20"`, `"4.21"`, `"4.22"`, "func(ctx context.Context, version string)")
	if count := strings.Count(mustRead(t, repo, path), "StartPriority("); count != 2 {
		t.Fatalf("file has %d StartPriority calls, want 2", count)
	}
}

func TestPlanSharedTablePriority(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/shared.go": sharedTableSource})
	path := "test/e2e/shared.go"
	tableLine := lineOf(t, repo, path, `DescribeTable("should share a priority"`)
	first := "shared should share a priority version 4.20"
	second := "shared should share a priority version 4.21"
	before := et.ExtensionTestSpecs{
		catalogSpec(first, 40, path+":"+itoa(lineOf(t, repo, path, `Entry("version 4.20"`)), path+":"+itoa(tableLine)),
		catalogSpec(second, 40, path+":"+itoa(lineOf(t, repo, path, `Entry("version 4.21"`)), path+":"+itoa(tableLine)),
	}
	if _, err := Plan(repo, before, testReport(scoredRow(first, "id-1", 90, 60))); err == nil ||
		!strings.Contains(err.Error(), "shared-priority-conflict") {
		t.Fatalf("accepted a table priority one descendant does not propose: %v", err)
	}
	if _, err := Plan(repo, before, testReport(scoredRow(first, "id-1", 90, 60), scoredRow(second, "id-2", 80, 45))); err == nil ||
		!strings.Contains(err.Error(), "shared-priority-conflict") {
		t.Fatalf("accepted diverging table proposals: %v", err)
	}
	changes, err := Plan(repo, before, testReport(scoredRow(first, "id-1", 90, 60), scoredRow(second, "id-2", 90, 60)))
	if err != nil {
		t.Fatal(err)
	}
	if changes.Len() != 2 {
		t.Fatalf("planned changes = %+v", changes.Summary(false))
	}
	if err := Apply(changes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{first, second} {
		if got := effectivePriority(t, repo, path, name); got != 90 {
			t.Errorf("%q priority = %d, want 90", name, got)
		}
	}
	if count := strings.Count(mustRead(t, repo, path), "StartPriority("); count != 1 {
		t.Fatalf("file has %d StartPriority calls, want the single shared one", count)
	}
}

func TestPlanRejectsStaleAndUnmappableProposals(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	path := "test/e2e/standalone.go"
	name := "suite should replace an existing priority"
	line := lineOf(t, repo, path, "should replace an existing priority")
	for _, tc := range []struct {
		name      string
		catalog   et.ExtensionTestSpecs
		report    citests.PriorityReport
		substring string
	}{
		{
			name:      "stale catalog priority",
			catalog:   et.ExtensionTestSpecs{catalogSpec(name, 50, path+":"+itoa(line))},
			report:    testReport(scoredRow(name, "id", 90, 60)),
			substring: "stale mapping",
		},
		{
			name:      "no declaration at the location",
			catalog:   et.ExtensionTestSpecs{catalogSpec(name, 40, path+":"+itoa(line+40))},
			report:    testReport(scoredRow(name, "id", 90, 60)),
			substring: "no It or Entry declaration",
		},
		{
			name:      "duplicate catalog priorities",
			catalog:   et.ExtensionTestSpecs{{Name: name, Labels: otesets.New[string]("StartPriority:10", "StartPriority:20"), CodeLocations: []string{path + ":" + itoa(line)}}},
			report:    testReport(scoredRow(name, "id", 90, 60)),
			substring: "multiple start priorities",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := mustRead(t, repo, path)
			_, err := Plan(repo, tc.catalog, tc.report)
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
			if got := mustRead(t, repo, path); got != original {
				t.Fatal("planning wrote to the source")
			}
		})
	}
}

func TestPlanRejectsSymlinkGeneratedAndEscapingFiles(t *testing.T) {
	repo := testRepo(t, map[string]string{
		"test/e2e/standalone.go": standaloneSource,
		"test/e2e/generated.go": `// Code generated by tools. DO NOT EDIT.
package e2e
`,
		"elsewhere/secret.go": standaloneSource,
	})
	name := "suite should replace an existing priority"
	line := itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))
	if err := os.Symlink(filepath.Join(repo, "test/e2e/standalone.go"), filepath.Join(repo, "test/e2e/link.go")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		location  string
		substring string
	}{
		{name: "symlink", location: "test/e2e/link.go:" + line, substring: "symlink"},
		{name: "generated", location: "test/e2e/generated.go:1", substring: "generated"},
		{name: "traversal", location: "../elsewhere/secret.go:" + line, substring: "no It or Entry declaration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, tc.location)}
			_, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
		})
	}
}

func TestPlanAcceptsAbsoluteLocations(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	name := "suite should replace an existing priority"
	line := lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority")
	location := fmt.Sprintf("/build/src/github.com/Azure/ARO-HCP/test/e2e/standalone.go:%d", line)
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, location)}
	changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
	if err != nil {
		t.Fatal(err)
	}
	if changes.Len() != 1 || changes.Summary(false).Changes[0].Path != "test/e2e/standalone.go" {
		t.Fatalf("absolute location did not map: %+v", changes.Summary(false))
	}
}

func TestPlanOneBadChangeWritesNothing(t *testing.T) {
	repo := testRepo(t, map[string]string{
		"test/e2e/standalone.go": standaloneSource,
		"test/e2e/table.go":      tableSource,
	})
	good := "suite should replace an existing priority"
	bad := "table should create versions version 4.20"
	catalog := et.ExtensionTestSpecs{
		catalogSpec(good, 40, "test/e2e/standalone.go:"+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))),
		catalogSpec(bad, 50, "test/e2e/table.go:"+itoa(lineOf(t, repo, "test/e2e/table.go", `Entry("version 4.20"`))),
	}
	// Drift the second declaration from its catalog value so the batch fails
	// after the first proposal has already been mapped.
	corrupted := strings.Replace(mustRead(t, repo, "test/e2e/table.go"), "labels.StartPriority(50)", "labels.StartPriority(60)", 1)
	if err := os.WriteFile(filepath.Join(repo, "test/e2e/table.go"), []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}
	original := map[string]string{
		"test/e2e/standalone.go": mustRead(t, repo, "test/e2e/standalone.go"),
		"test/e2e/table.go":      mustRead(t, repo, "test/e2e/table.go"),
	}
	report := testReport(scoredRow(good, "id-good", 90, 60), scoredRow(bad, "id-bad", 40, 1))
	if _, err := Plan(repo, catalog, report); err == nil || !strings.Contains(err.Error(), "stale mapping") {
		t.Fatalf("error = %v, want a stale mapping rejection", err)
	}
	for path, want := range original {
		if got := mustRead(t, repo, path); got != want {
			t.Fatalf("%s was modified by a failed plan", path)
		}
	}
}

func TestApplyRejectsConcurrentModification(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	name := "suite should replace an existing priority"
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/standalone.go:"+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority")))}
	changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
	if err != nil {
		t.Fatal(err)
	}
	modified := mustRead(t, repo, "test/e2e/standalone.go") + "\n// concurrent edit\n"
	if err := os.WriteFile(filepath.Join(repo, "test/e2e/standalone.go"), []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Apply(changes); err == nil || !strings.Contains(err.Error(), "changed since planning") {
		t.Fatalf("error = %v", err)
	}
	if got := mustRead(t, repo, "test/e2e/standalone.go"); got != modified {
		t.Fatal("a rejected apply rewrote the file")
	}
}

func TestVerifyDetectsUnplannedCatalogChanges(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	name := "suite should replace an existing priority"
	other := "suite should keep an unchanged priority"
	line := itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))
	before := et.ExtensionTestSpecs{
		catalogSpec(name, 40, "test/e2e/standalone.go:"+line),
		catalogSpec(other, 30, "test/e2e/standalone.go:"+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should keep an unchanged priority"))),
	}
	changes, err := Plan(repo, before, testReport(scoredRow(name, "id", 90, 60)))
	if err != nil {
		t.Fatal(err)
	}
	rebuild := func(mutate func(et.ExtensionTestSpecs) et.ExtensionTestSpecs) et.ExtensionTestSpecs {
		after := make(et.ExtensionTestSpecs, 0, len(before))
		for _, spec := range before {
			copied := &et.ExtensionTestSpec{Name: spec.Name, Labels: spec.Labels.Clone(), CodeLocations: spec.CodeLocations}
			after = append(after, copied)
		}
		after[0].Labels = otesets.New[string]("Critical", "MIContainers:1", "StartPriority:90")
		return mutate(after)
	}
	if err := Verify(before, rebuild(func(after et.ExtensionTestSpecs) et.ExtensionTestSpecs { return after }), changes); err != nil {
		t.Fatalf("rejected a correct rebuild: %v", err)
	}
	for _, tc := range []struct {
		name      string
		mutate    func(et.ExtensionTestSpecs) et.ExtensionTestSpecs
		substring string
	}{
		{"unplanned label", func(after et.ExtensionTestSpecs) et.ExtensionTestSpecs {
			after[1].Labels.Insert("Speed:Slow")
			return after
		}, "changed labels other than StartPriority"},
		{"wrong priority", func(after et.ExtensionTestSpecs) et.ExtensionTestSpecs {
			after[1].Labels = otesets.New[string]("Critical", "MIContainers:1", "StartPriority:20")
			return after
		}, "want 30"},
		{"dropped spec", func(after et.ExtensionTestSpecs) et.ExtensionTestSpecs { return after[:1] }, "missing 1 specs"},
		{"added spec", func(after et.ExtensionTestSpecs) et.ExtensionTestSpecs {
			return append(after, catalogSpec("suite should be new", 10, "test/e2e/standalone.go:1"))
		}, "gained unexpected spec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(before, rebuild(tc.mutate), changes); err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
		})
	}
}

func TestValidateReport(t *testing.T) {
	priority, score := 90, 60.0
	invalid := 1001
	negative := -1.0
	for _, tc := range []struct {
		name      string
		report    citests.PriorityReport
		substring string
	}{
		{"schema version", citests.PriorityReport{SchemaVersion: 2, Algorithm: citests.AlgorithmEnvironmentNormalizedMean}, "schema_version"},
		{"algorithm", citests.PriorityReport{SchemaVersion: 1, Algorithm: "other"}, "algorithm"},
		{"duplicate name", testReport(scoredRow("a", "1", priority, score), scoredRow("a", "2", priority, score)), "duplicate test name"},
		{"duplicate id", testReport(scoredRow("a", "1", priority, score), scoredRow("b", "1", priority, score)), "duplicate test_id"},
		{"priority range", testReport(scoredRow("a", "1", invalid, score)), "outside 0..1000"},
		{"negative score", testReport(scoredRow("a", "1", priority, negative)), "invalid score"},
		{"insufficient with proposal", testReport(citests.TestReport{TestID: "1", Name: "a", Status: citests.StatusInsufficientData, ProposedPriority: &priority}), "carries a proposal"},
		{"unknown status", testReport(citests.TestReport{TestID: "1", Name: "a", Status: "scored-ish"}), "unknown status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateReport(tc.report); err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
		})
	}
}

func TestCanonicalLocation(t *testing.T) {
	for _, tc := range []struct {
		location string
		path     string
		line     int
		ok       bool
	}{
		{"test/e2e/cluster_create.go:42", "test/e2e/cluster_create.go", 42, true},
		{"./test/e2e/cluster_create.go:42", "test/e2e/cluster_create.go", 42, true},
		{"/build/src/github.com/Azure/ARO-HCP/test/e2e/cluster_create.go:42", "test/e2e/cluster_create.go", 42, true},
		{"test/e2e/../e2e/cluster_create.go:42", "test/e2e/cluster_create.go", 42, true},
		{"set up framework | framework.go:200", "", 0, false},
		{"test/e2e/cluster_create.go:0", "", 0, false},
		{"test/e2e/cluster_create.go", "", 0, false},
		{"test/e2e/../internal/api.go:10", "", 0, false},
		{"internal/api.go:10", "", 0, false},
		{"test/e2e/README.md:10", "", 0, false},
	} {
		path, line, ok := canonicalLocation(tc.location)
		if ok != tc.ok || path != tc.path || (ok && line != tc.line) {
			t.Errorf("canonicalLocation(%q) = %q, %d, %t; want %q, %d, %t", tc.location, path, line, ok, tc.path, tc.line, tc.ok)
		}
	}
}

func TestPlanReportsUnmatchedRetiredSpecs(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	name := "suite should replace an existing priority"
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/standalone.go:"+itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority")))}
	report := testReport(scoredRow("suite should no longer exist", "id-retired", 90, 60), citests.TestReport{TestID: "id-unnamed", Status: citests.StatusInsufficientData})
	changes, err := Plan(repo, catalog, report)
	if err != nil {
		t.Fatal(err)
	}
	summary := changes.Summary(false)
	if len(summary.Changes) != 0 || len(summary.Unmatched) != 2 {
		t.Fatalf("retired specs were not reported: %+v", summary)
	}
}

func mustRead(t *testing.T, repo, relPath string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}

const bodyCallSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("body", func() {
	It("should ignore a body call",
		func(ctx context.Context) {
			_ = ctx
			_ = labels.StartPriority(0)
		})
})
`

const repeatedPrioritySource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("repeated", func() {
	It("should reject repeated priorities",
		labels.StartPriority(40),
		labels.StartPriority(40),
		func(ctx context.Context) {
			_ = ctx
		})
})
`

const spreadSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var decorators = []any{labels.MIContainers(1)}

var _ = Describe("spread", func() {
	It("should reject spread arguments", append(decorators,
		func(ctx context.Context) {
			_ = ctx
		})...)
})
`

const commentedRemovalSource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("commented", func() {
	It("should keep its comment",
		labels.MIContainers(2), // rationale for the isolation
		labels.StartPriority(50),
		func(ctx context.Context) {
			_ = ctx
		})
})
`

const computedTableSource = `package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("computed", func() {
	DescribeTable("should share across entries",
		labels.StartPriority(40),
		func(ctx context.Context, version string) {
			_ = ctx
			_ = version
		},
		Entry("version 4.20", "4.20"),
		Entry(fmt.Sprintf("version %s", "4.21"), "4.21"),
	)
})
`

func TestPlanIgnoresCallsInsideTestBodies(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/body.go": bodyCallSource})
	name := "body should ignore a body call"
	line := itoa(lineOf(t, repo, "test/e2e/body.go", "should ignore a body call"))
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 0, "test/e2e/body.go:"+line)}
	changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 20, 18)))
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(changes); err != nil {
		t.Fatal(err)
	}
	if got := effectivePriority(t, repo, "test/e2e/body.go", name); got != 20 {
		t.Fatalf("effective priority = %d, want 20", got)
	}
	mustContain(t, repo, "test/e2e/body.go", "_ = labels.StartPriority(0)", "labels.StartPriority(20)")
}

func TestPlanRejectsUnmappableDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name      string
		source    string
		needle    string
		spec      string
		catalog   int
		proposal  int
		substring string
	}{
		{name: "repeated", source: repeatedPrioritySource, needle: "should reject repeated priorities",
			spec: "repeated should reject repeated priorities", catalog: 40, proposal: 90, substring: "more than once"},
		{name: "spread", source: spreadSource, needle: "should reject spread arguments",
			spec: "spread should reject spread arguments", catalog: 0, proposal: 20, substring: "spreads its arguments"},
		{name: "comment", source: commentedRemovalSource, needle: "should keep its comment",
			spec: "commented should keep its comment", catalog: 50, proposal: 0, substring: "would delete a comment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relPath := "test/e2e/" + tc.name + ".go"
			repo := testRepo(t, map[string]string{relPath: tc.source})
			line := itoa(lineOf(t, repo, relPath, tc.needle))
			before := mustRead(t, repo, relPath)
			catalog := et.ExtensionTestSpecs{catalogSpec(tc.spec, tc.catalog, relPath+":"+line)}
			changes, err := Plan(repo, catalog, testReport(scoredRow(tc.spec, "id", tc.proposal, 20)))
			if err == nil || !strings.Contains(err.Error(), tc.substring) {
				t.Fatalf("error = %v, want %q", err, tc.substring)
			}
			if changes.Len() != 0 {
				t.Fatalf("plan returned %d changes", changes.Len())
			}
			if after := mustRead(t, repo, relPath); after != before {
				t.Fatalf("%s was modified by a rejected plan", relPath)
			}
		})
	}
}

func TestPlanRejectsDirectorySymlink(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	outside := t.TempDir()
	external := filepath.Join(outside, "spec.go")
	if err := os.WriteFile(external, []byte(standaloneSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "test/e2e/linked")); err != nil {
		t.Fatal(err)
	}
	name := "suite should replace an existing priority"
	line := itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/linked/spec.go:"+line)}
	_, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want a symlink rejection", err)
	}
}

func TestPlanTreatsUnresolvableTableDescendantAsShared(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/computed.go": computedTableSource})
	tableLine := itoa(lineOf(t, repo, "test/e2e/computed.go", "should share across entries"))
	entryLine := itoa(lineOf(t, repo, "test/e2e/computed.go", `Entry("version 4.20"`))
	literal := "computed should share across entries version 4.20"
	computed := "computed should share across entries version 4.21"
	// The extension records the DescribeTable declaration for an entry it cannot
	// locate, which is exactly the descendant the shared check must not miss.
	catalog := et.ExtensionTestSpecs{
		catalogSpec(literal, 40, "test/e2e/computed.go:"+entryLine),
		catalogSpec(computed, 40, "test/e2e/computed.go:"+tableLine),
	}
	_, err := Plan(repo, catalog, testReport(scoredRow(literal, "id", 90, 60)))
	if err == nil || !strings.Contains(err.Error(), "shared-priority-conflict") {
		t.Fatalf("error = %v, want shared-priority-conflict", err)
	}
}

func TestPlanRejectsRenamedContainer(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/standalone.go": standaloneSource})
	name := "suite should replace an existing priority"
	line := itoa(lineOf(t, repo, "test/e2e/standalone.go", "should replace an existing priority"))
	renamed := strings.Replace(standaloneSource, `Describe("suite"`, `Describe("renamed"`, 1)
	if renamed == standaloneSource {
		t.Fatal("fixture has no Describe container to rename")
	}
	if err := os.WriteFile(filepath.Join(repo, "test/e2e/standalone.go"), []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/standalone.go:"+line)}
	_, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
	if err == nil || !strings.Contains(err.Error(), "no It or Entry declaration matches") {
		t.Fatalf("error = %v, want a stale identity rejection", err)
	}
}

const inheritedPrioritySource = `package e2e

import (
	"context"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("inherited",
	labels.StartPriority(40),
	func() {
		It("should reject inherited priorities",
			labels.StartPriority(40),
			func(ctx context.Context) {
				_ = ctx
			})
	})
`

func TestPlanRejectsInheritedPriority(t *testing.T) {
	for _, proposal := range []int{0, 90} {
		t.Run(itoa(proposal), func(t *testing.T) {
			repo := testRepo(t, map[string]string{"test/e2e/inherited.go": inheritedPrioritySource})
			name := "inherited should reject inherited priorities"
			line := itoa(lineOf(t, repo, "test/e2e/inherited.go", "should reject inherited priorities"))
			before := mustRead(t, repo, "test/e2e/inherited.go")
			catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/inherited.go:"+line)}
			changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", proposal, 20)))
			if err == nil || !strings.Contains(err.Error(), "already inherits one") {
				t.Fatalf("error = %v, want an inherited-priority rejection", err)
			}
			if changes.Len() != 0 {
				t.Fatalf("plan returned %d changes", changes.Len())
			}
			if after := mustRead(t, repo, "test/e2e/inherited.go"); after != before {
				t.Fatal("a rejected inherited-priority plan modified the source")
			}
		})
	}
}

func TestRestoreKeepsConcurrentEdits(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/concurrent.go": standaloneSource})
	path := filepath.Join(repo, "test/e2e/concurrent.go")
	published := []byte("published content")
	if err := os.WriteFile(path, published, 0o644); err != nil {
		t.Fatal(err)
	}
	err := restore([]writtenFile{{
		path:      path,
		original:  []byte("original content"),
		published: sha256.Sum256([]byte("different content")),
		mode:      0o644,
	}}, errors.New("later file changed since planning"))
	if err == nil || !strings.Contains(err.Error(), "needs recovery") {
		t.Fatalf("error = %v, want an incomplete rollback report", err)
	}
	if got := mustRead(t, repo, "test/e2e/concurrent.go"); got != string(published) {
		t.Fatalf("rollback overwrote a concurrent edit: %q", got)
	}
}

func TestVerifyRejectsUnplannedZeroPriorityLabel(t *testing.T) {
	before := et.ExtensionTestSpecs{catalogSpec("suite leaf", 0)}
	after := et.ExtensionTestSpecs{catalogSpec("suite leaf", 1)}
	// catalogSpec skips the label for priority 0, so set the explicit zero label.
	after[0].Labels.Insert(labels.StartPriorityLabelPrefix + "0")
	after[0].Labels.Delete(labels.StartPriorityLabelPrefix + "1")
	err := Verify(before, after, ChangeSet{})
	if err == nil || !strings.Contains(err.Error(), "declares start priorities") {
		t.Fatalf("error = %v, want an unplanned label rejection", err)
	}
}

const dynamicContainerSource = `package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	"github.com/Azure/ARO-HCP/test/util/labels"
)

var _ = Describe("dynamic", func() {
	DescribeTable(fmt.Sprintf("should share %s", "across entries"),
		labels.StartPriority(40),
		func(ctx context.Context, version string) {
			_ = ctx
			_ = version
		},
		Entry("version 4.20", "4.20"),
	)
})
`

func TestPlanMatchesDynamicContainerByLiteralFragments(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/dynamic.go": dynamicContainerSource})
	line := itoa(lineOf(t, repo, "test/e2e/dynamic.go", `Entry("version 4.20"`))
	name := "dynamic should share across entries version 4.20"
	catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/dynamic.go:"+line)}
	changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
	if err != nil {
		t.Fatal(err)
	}
	if changes.Len() != 1 {
		t.Fatalf("planned %d changes, want 1", changes.Len())
	}
}

func TestPlanRejectsStaleOrPartialIdentity(t *testing.T) {
	repo := testRepo(t, map[string]string{"test/e2e/dynamic.go": dynamicContainerSource})
	line := itoa(lineOf(t, repo, "test/e2e/dynamic.go", `Entry("version 4.20"`))
	before := mustRead(t, repo, "test/e2e/dynamic.go")
	for _, name := range []string{
		// The literal container anchor is part of the identity.
		"renamed should share across entries version 4.20",
		// A leaf suffix alone is not an identity.
		"version 4.20",
	} {
		catalog := et.ExtensionTestSpecs{catalogSpec(name, 40, "test/e2e/dynamic.go:"+line)}
		changes, err := Plan(repo, catalog, testReport(scoredRow(name, "id", 90, 60)))
		if err == nil || !strings.Contains(err.Error(), "no It or Entry declaration matches") {
			t.Fatalf("%q resolved to a stale identity: %v", name, err)
		}
		if changes.Len() != 0 {
			t.Fatalf("%q produced %d changes", name, changes.Len())
		}
	}
	if after := mustRead(t, repo, "test/e2e/dynamic.go"); after != before {
		t.Fatal("a rejected identity plan modified the source")
	}
}
