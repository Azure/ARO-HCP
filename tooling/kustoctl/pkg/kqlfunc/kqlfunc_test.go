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

package kqlfunc

import (
	"os"
	"path/filepath"
	"testing"
)

func writeKQLFile(t *testing.T, dir, name, depends, declName string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "// @owner: sre-team\n" +
		"// @depends: " + depends + "\n" +
		".create-or-alter function\n" +
		"with (\n" +
		"  docstring = 'stub'\n" +
		")\n" +
		declName + "(p_X: string)\n" +
		"{\n" +
		"    print 1\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
	return path
}

// writeKQLFileNoDepends writes a function file with no "@depends:" header at
// all, exercising the common no-dependency case where the line is omitted
// rather than present-but-empty.
func writeKQLFileNoDepends(t *testing.T, dir, name, declName string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "// @owner: sre-team\n" +
		".create-or-alter function\n" +
		"with (\n" +
		"  docstring = 'stub'\n" +
		")\n" +
		declName + "(p_X: string)\n" +
		"{\n" +
		"    print 1\n" +
		"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}
	return path
}

func TestSortByDependency_ReordersRealWorldCase(t *testing.T) {
	dir := t.TempDir()
	// Episodes sorts before History alphabetically but invokes it.
	episodes := writeKQLFile(t, dir, "ServiceLogs_EnrichWithConditionEpisodes.kql",
		"EnrichWithConditionHistory", "EnrichWithConditionEpisodes")
	history := writeKQLFileNoDepends(t, dir, "ServiceLogs_EnrichWithConditionHistory.kql",
		"EnrichWithConditionHistory")

	files, err := ParseFiles([]string{episodes, history})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	grouped, err := SortByDependency(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := grouped["ServiceLogs"]
	if len(got) != 2 || got[0].Path != history || got[1].Path != episodes {
		t.Fatalf("expected [history, episodes], got %v", got)
	}
}

func TestSortByDependency_NoDependenciesStaysAlphabetical(t *testing.T) {
	dir := t.TempDir()
	zebra := writeKQLFileNoDepends(t, dir, "ServiceLogs_Zebra.kql", "Zebra")
	apple := writeKQLFileNoDepends(t, dir, "ServiceLogs_Apple.kql", "Apple")

	files, err := ParseFiles([]string{zebra, apple})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	grouped, err := SortByDependency(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := grouped["ServiceLogs"]
	if len(got) != 2 || got[0].Path != apple || got[1].Path != zebra {
		t.Fatalf("expected alphabetical [apple, zebra], got %v", got)
	}
}

func TestSortByDependency_BlankDependsLineIsNoDependency(t *testing.T) {
	dir := t.TempDir()
	// A present-but-empty "@depends:" line means the same as omitting it.
	f := writeKQLFile(t, dir, "ServiceLogs_GetClusterServiceIdHistory.kql", "", "GetClusterServiceIdHistory")

	files, err := ParseFiles([]string{f})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files[0].Depends) != 0 {
		t.Fatalf("expected no dependencies, got %v", files[0].Depends)
	}
	grouped, err := SortByDependency(files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(grouped["ServiceLogs"]) != 1 {
		t.Fatalf("expected [f], got %v", grouped["ServiceLogs"])
	}
}

func TestSortByDependency_DetectsCycle(t *testing.T) {
	dir := t.TempDir()
	a := writeKQLFile(t, dir, "ServiceLogs_A.kql", "B", "A")
	b := writeKQLFile(t, dir, "ServiceLogs_B.kql", "A", "B")

	files, err := ParseFiles([]string{a, b})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := SortByDependency(files); err == nil {
		t.Fatalf("expected a circular dependency error, got nil")
	}
}

func TestSortByDependency_UnresolvedDependencyErrors(t *testing.T) {
	dir := t.TempDir()
	// "@depends:" naming something that isn't a real function in this
	// database (e.g. a stale reference, a typo, or a table name left over
	// from the old convention) must fail loudly, not be silently ignored.
	f := writeKQLFile(t, dir, "ServiceLogs_A.kql", "NotARealFunction", "A")

	files, err := ParseFiles([]string{f})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := SortByDependency(files); err == nil {
		t.Fatalf("expected an error for an unresolved @depends entry, got nil")
	}
}

func TestSortByDependency_TableNameInDependsErrors(t *testing.T) {
	dir := t.TempDir()
	// Table names never belong in @depends (table-before-function order is
	// already structural), so one left over from the old convention must be
	// rejected, not silently tolerated.
	f := writeKQLFile(t, dir, "ServiceLogs_A.kql", "cosmosResourceSnapshots", "A")

	files, err := ParseFiles([]string{f})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := SortByDependency(files); err == nil {
		t.Fatalf("expected an error for a table name in @depends, got nil")
	}
}

func TestParseFiles_MissingDeclarationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ServiceLogs_Broken.kql")
	if err := os.WriteFile(path, []byte("// just a comment, no function here\n"), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}

	if _, err := ParseFiles([]string{path}); err == nil {
		t.Fatalf("expected an error for a file with no function declaration, got nil")
	}
}

func TestParseFiles_MissingDatabasePrefixErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "NoPrefix.kql")
	content := "// @owner: sre-team\n.create-or-alter function\nwith (\n  docstring = 'stub'\n)\nNoPrefix(p_X: string)\n{\n    print 1\n}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %q: %v", path, err)
	}

	if _, err := ParseFiles([]string{path}); err == nil {
		t.Fatalf("expected an error for a filename without a \"{Database}_\" prefix, got nil")
	}
}

func TestSortByDependency_SameFunctionNameAcrossDatabasesAllowed(t *testing.T) {
	dir := t.TempDir()
	// The "{Database}_{FunctionName}.kql" convention intentionally allows the
	// same bare function name to recur once per database.
	hcp := writeKQLFileNoDepends(t, dir, "HostedControlPlaneLogs_GetARMResourceIDForClustersServiceID.kql",
		"GetARMResourceIDForClustersServiceID")
	svc := writeKQLFileNoDepends(t, dir, "ServiceLogs_GetARMResourceIDForClustersServiceID.kql",
		"GetARMResourceIDForClustersServiceID")

	files, err := ParseFiles([]string{hcp, svc})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	grouped, err := SortByDependency(files)
	if err != nil {
		t.Fatalf("unexpected error for same function name in different databases: %v", err)
	}
	if len(grouped["HostedControlPlaneLogs"]) != 1 || len(grouped["ServiceLogs"]) != 1 {
		t.Fatalf("expected one file per database, got %v", grouped)
	}
}

func TestSortByDependency_CrossDatabaseDependencyErrors(t *testing.T) {
	dir := t.TempDir()
	// A function cannot depend on a function in a different database
	// (Kusto has no cross-database invoke), even if a function with that
	// exact name exists elsewhere.
	writeKQLFileNoDepends(t, dir, "HostedControlPlaneLogs_Helper.kql", "Helper")
	svc := writeKQLFile(t, dir, "ServiceLogs_A.kql", "Helper", "A")

	files, err := ParseFiles([]string{filepath.Join(dir, "HostedControlPlaneLogs_Helper.kql"), svc})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := SortByDependency(files); err == nil {
		t.Fatalf("expected an error for a cross-database @depends reference, got nil")
	}
}

func TestSortByDependency_DuplicateFilenameStillErrors(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", sub, err)
	}
	a := writeKQLFileNoDepends(t, dir, "ServiceLogs_GetClusterIdFromInternalId.kql", "GetClusterIdFromInternalId")
	b := writeKQLFileNoDepends(t, sub, "ServiceLogs_GetClusterIdFromInternalId.kql", "GetClusterIdFromInternalIdAlt")

	files, err := ParseFiles([]string{a, b})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := SortByDependency(files); err == nil {
		t.Fatalf("expected a duplicate filename error, got nil")
	}
}
