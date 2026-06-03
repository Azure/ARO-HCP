// Copyright 2025 Microsoft Corporation
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

package customlinters

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestNoPrintLinter_PositiveCases(t *testing.T) {
	linter := &NoPrintLinter{}
	analyzers, err := linter.BuildAnalyzers()
	if err != nil {
		t.Fatalf("Failed to build analyzers: %v", err)
	}

	if len(analyzers) != 1 {
		t.Fatalf("Expected 1 analyzer, got %d", len(analyzers))
	}

	analyzer := analyzers[0]

	// Parse the positive test file
	fset := token.NewFileSet()
	content, err := os.ReadFile("testdata/positive_test.go")
	if err != nil {
		t.Fatalf("Failed to read positive_test.go: %v", err)
	}
	// Register with a path that contains /testdata/ to match the linter's filter
	file, err := parser.ParseFile(fset, "/fake/path/testdata/positive_test.go", content, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse positive_test.go: %v", err)
	}

	// Create a pass
	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{file},
		Report: func(d analysis.Diagnostic) {
			t.Logf("Found issue: %s at %s", d.Message, fset.Position(d.Pos))
		},
	}

	// Count diagnostics
	diagnosticCount := 0
	originalReport := pass.Report
	pass.Report = func(d analysis.Diagnostic) {
		diagnosticCount++
		originalReport(d)
	}

	// Run the analyzer
	_, err = analyzer.Run(pass)
	if err != nil {
		t.Fatalf("Analyzer run failed: %v", err)
	}

	// Positive test should find 6 issues
	expectedIssues := 6
	if diagnosticCount != expectedIssues {
		t.Errorf("Expected %d issues in positive_test.go, got %d", expectedIssues, diagnosticCount)
	}
}

func TestNoPrintLinter_NegativeCases(t *testing.T) {
	linter := &NoPrintLinter{}
	analyzers, err := linter.BuildAnalyzers()
	if err != nil {
		t.Fatalf("Failed to build analyzers: %v", err)
	}

	if len(analyzers) != 1 {
		t.Fatalf("Expected 1 analyzer, got %d", len(analyzers))
	}

	analyzer := analyzers[0]

	// Parse the negative test file
	fset := token.NewFileSet()
	content, err := os.ReadFile("testdata/negative_test.go")
	if err != nil {
		t.Fatalf("Failed to read negative_test.go: %v", err)
	}
	// Register with a path that contains /testdata/ to match the linter's filter
	file, err := parser.ParseFile(fset, "/fake/path/testdata/negative_test.go", content, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse negative_test.go: %v", err)
	}

	// Create a pass
	pass := &analysis.Pass{
		Fset:  fset,
		Files: []*ast.File{file},
		Report: func(d analysis.Diagnostic) {
			t.Errorf("Unexpected issue found: %s at %s", d.Message, fset.Position(d.Pos))
		},
	}

	// Count diagnostics
	diagnosticCount := 0
	pass.Report = func(d analysis.Diagnostic) {
		diagnosticCount++
		t.Errorf("Unexpected issue found: %s at %s", d.Message, fset.Position(d.Pos))
	}

	// Run the analyzer
	_, err = analyzer.Run(pass)
	if err != nil {
		t.Fatalf("Analyzer run failed: %v", err)
	}

	// Negative test should find 0 issues
	if diagnosticCount != 0 {
		t.Errorf("Expected 0 issues in negative_test.go, got %d", diagnosticCount)
	}
}
