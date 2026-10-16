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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		file           string
		wantViolations int
		wantSubstring  string
	}{
		{
			name:           "valid labels produce no violations",
			file:           "testdata/valid.go",
			wantViolations: 0,
		},
		{
			name:           "missing label is flagged",
			file:           "testdata/missing_label.go",
			wantViolations: 1,
			wantSubstring:  "missing labels.MIContainers(N) decorator",
		},
		{
			name:           "negative MIContainers value is flagged",
			file:           "testdata/negative_label.go",
			wantViolations: 1,
			wantSubstring:  "N must be >= 0",
		},
		{
			name:           "mismatched count is flagged",
			file:           "testdata/mismatch_count.go",
			wantViolations: 1,
			wantSubstring:  "MIContainers(2) but calls AssignIdentityContainers with count=1",
		},
		{
			name:           "zero with AssignIdentityContainers call is flagged",
			file:           "testdata/zero_but_calls.go",
			wantViolations: 1,
			wantSubstring:  "MIContainers(0) but calls AssignIdentityContainers",
		},
		{
			name:           "nonzero without AssignIdentityContainers call is flagged",
			file:           "testdata/nonzero_no_call.go",
			wantViolations: 1,
			wantSubstring:  "MIContainers(2) but does not call AssignIdentityContainers",
		},
		{
			name:           "duplicate MIContainers labels are flagged",
			file:           "testdata/duplicate_label.go",
			wantViolations: 1,
			wantSubstring:  "MIContainers labels; exactly one is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			violations, err := checkFile(tc.file)
			if err != nil {
				t.Fatalf("checkFile(%s) returned error: %v", tc.file, err)
			}
			if len(violations) != tc.wantViolations {
				t.Errorf("checkFile(%s) returned %d violations, want %d:\n%s",
					tc.file, len(violations), tc.wantViolations, strings.Join(violations, "\n"))
			}
			if tc.wantSubstring != "" && len(violations) > 0 {
				found := false
				for _, v := range violations {
					if strings.Contains(v, tc.wantSubstring) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("checkFile(%s) violations do not contain %q:\n%s",
						tc.file, tc.wantSubstring, strings.Join(violations, "\n"))
				}
			}
		})
	}
}

func TestCheckDir(t *testing.T) {
	t.Parallel()

	violations, err := checkDir("testdata")
	if err != nil {
		t.Fatalf("checkDir(testdata) returned error: %v", err)
	}
	// testdata has 6 files with violations (missing, negative, mismatch, zero_but_calls, nonzero_no_call, duplicate_label)
	// and 1 valid file
	if len(violations) != 6 {
		t.Errorf("checkDir(testdata) returned %d violations, want 6:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestResourceScopedE2EInvariant(t *testing.T) {
	t.Parallel()

	specs, err := findResourceScopedSpecs(filepath.Join("..", "..", "test", "e2e"))
	if err != nil {
		t.Fatalf("findResourceScopedSpecs() returned error: %v", err)
	}
	if violations := validateResourceScopedSpecs(specs); len(violations) > 0 {
		t.Fatalf("resource-scoped E2E invariant failed:\n%s", strings.Join(violations, "\n"))
	}
}

func TestValidateResourceScopedSpecs(t *testing.T) {
	t.Parallel()

	valid := resourceScopedSpec{
		file: filepath.Join("test", "e2e", resourceScopedTestFile),
		line: 37,
		kind: "It",
		name: resourceScopedTestName,
		uses: 1,
	}
	tests := []struct {
		name          string
		specs         []resourceScopedSpec
		wantViolation string
	}{
		{
			name:  "canonical test is accepted",
			specs: []resourceScopedSpec{valid},
		},
		{
			name:          "missing resource-scoped test is rejected",
			wantViolation: "found 0",
		},
		{
			name: "multiple resource-scoped tests are rejected",
			specs: []resourceScopedSpec{
				valid,
				{file: "test/e2e/unrelated.go", line: 10, kind: "It", name: "unrelated", uses: 1},
			},
			wantViolation: "found 2",
		},
		{
			name: "wrong file is rejected",
			specs: []resourceScopedSpec{{
				file: "test/e2e/unrelated.go",
				line: 10,
				kind: "It",
				name: resourceScopedTestName,
				uses: 1,
			}},
			wantViolation: "expected cluster_delete_cx_rg.go",
		},
		{
			name: "wrong spec is rejected",
			specs: []resourceScopedSpec{{
				file: filepath.Join("test", "e2e", resourceScopedTestFile),
				line: 10,
				kind: "It",
				name: "unrelated",
				uses: 1,
			}},
			wantViolation: resourceScopedTestName,
		},
		{
			name: "describe table is rejected",
			specs: []resourceScopedSpec{{
				file: filepath.Join("test", "e2e", resourceScopedTestFile),
				line: 10,
				kind: "DescribeTable",
				name: resourceScopedTestName,
				uses: 1,
			}},
			wantViolation: "DescribeTable",
		},
		{
			name: "multiple uses in canonical spec are rejected",
			specs: []resourceScopedSpec{{
				file: filepath.Join("test", "e2e", resourceScopedTestFile),
				line: 10,
				kind: "It",
				name: resourceScopedTestName,
				uses: 2,
			}},
			wantViolation: "2 times; expected exactly once",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			violations := validateResourceScopedSpecs(tc.specs)
			if tc.wantViolation == "" {
				if len(violations) != 0 {
					t.Fatalf("validateResourceScopedSpecs() returned violations:\n%s", strings.Join(violations, "\n"))
				}
				return
			}
			if len(violations) != 1 || !strings.Contains(violations[0], tc.wantViolation) {
				t.Fatalf("validateResourceScopedSpecs() = %v, want one violation containing %q", violations, tc.wantViolation)
			}
		})
	}
}

func TestCanonicalSpecWithMultipleResourceScopedUsesIsRejected(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), resourceScopedTestFile)
	source := `package e2e

func register() {
	It("` + resourceScopedTestName + `",
		func() {
			use(framework.RBACScopeResource)
			use(framework.RBACScopeResource)
		},
	)
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("failed to write test source: %v", err)
	}

	specs, err := findResourceScopedSpecsInFile(path)
	if err != nil {
		t.Fatalf("findResourceScopedSpecsInFile() returned error: %v", err)
	}
	violations := validateResourceScopedSpecs(specs)
	if len(violations) != 1 || !strings.Contains(violations[0], "2 times; expected exactly once") {
		t.Fatalf("validateResourceScopedSpecs() = %v, want multiple-use violation", violations)
	}
}

func TestSameNamedResourceScopedDescribeTableIsRejected(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), resourceScopedTestFile)
	source := `package e2e

func register() {
	DescribeTable("` + resourceScopedTestName + `",
		func() {
			use(framework.RBACScopeResource)
		},
	)
}
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("failed to write test source: %v", err)
	}

	specs, err := findResourceScopedSpecsInFile(path)
	if err != nil {
		t.Fatalf("findResourceScopedSpecsInFile() returned error: %v", err)
	}
	violations := validateResourceScopedSpecs(specs)
	if len(violations) != 1 || !strings.Contains(violations[0], "DescribeTable") {
		t.Fatalf("validateResourceScopedSpecs() = %v, want DescribeTable violation", violations)
	}
}

func TestFormatViolations(t *testing.T) {
	t.Parallel()

	violations := []string{
		`test/e2e/missing.go:10: It("missing label") is missing labels.MIContainers(N) decorator`,
		`test/e2e/unrelated.go:20: It("unrelated") uses framework.RBACScopeResource; expected cluster_delete_cx_rg.go: It("canonical")`,
	}
	want := "ERROR: 2 E2E test invariant violation(s):\n" +
		"  - " + violations[0] + "\n" +
		"  - " + violations[1] + "\n"

	if got := formatViolations(violations); got != want {
		t.Fatalf("formatViolations() = %q, want %q", got, want)
	}
}
