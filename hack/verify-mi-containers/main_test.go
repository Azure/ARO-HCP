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
	// testdata has 5 files with violations (missing, negative, mismatch, zero_but_calls, nonzero_no_call)
	// and 1 valid file
	if len(violations) != 5 {
		t.Errorf("checkDir(testdata) returned %d violations, want 5:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
