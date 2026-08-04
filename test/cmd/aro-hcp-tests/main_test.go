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
	"strings"
	"testing"

	"github.com/Azure/ARO-HCP/test/util/testutil"
)

func TestMainListSuitesForEachSuite(t *testing.T) {
	type testCase struct {
		suite             string
		suffix            string
		setDevelopmentEnv bool
	}

	tests := []testCase{
		{suite: "integration/parallel", suffix: "integration-parallel"},
		{suite: "stage/parallel", suffix: "stage-parallel"},
		{suite: "prod/parallel", suffix: "prod-parallel"},
		{suite: "dev-cd-check/parallel", suffix: "dev-cd-check-parallel"},
		{suite: "rp-api-compat-all/parallel", suffix: "rp-api-compat-all-parallel"},
		{suite: "rp-api-compat-all/parallel", suffix: "rp-api-compat-all-parallel-development", setDevelopmentEnv: true},
		{suite: "upgrade/in-place", suffix: "upgrade-in-place"},
	}

	for _, test := range tests {
		t.Run(test.suite, func(t *testing.T) {
			if test.setDevelopmentEnv {
				os.Setenv("AROHCP_ENV", "development")
			}
			root := setupCli()
			root.SetArgs([]string{"list", "tests", "--suite", test.suite, "--output", "names"})

			mktempfile, err := os.CreateTemp("", "test-output-*.txt")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer os.Remove(mktempfile.Name())

			// Capture stdout to verify the command executes successfully
			originalStdout := os.Stdout
			os.Stdout = mktempfile
			defer func() {
				os.Stdout = originalStdout
			}()

			err = root.Execute()
			if err != nil {
				t.Fatalf("failed to execute command for suite %s: %v", test.suite, err)
			}
			testutil.CompareFileWithFixture(t, mktempfile.Name(), testutil.WithSuffix(test.suffix))

		})
	}
}

func TestEV2RetryMarker(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failedNames []string
		nonRetried  int
		wantOK      bool
	}{
		{
			name:   "clean run emits nothing",
			wantOK: false,
		},
		{
			name:        "single labeled failure qualifies",
			failedNames: []string{"spec A"},
			wantOK:      true,
		},
		{
			name:        "failures at the cap still qualify",
			failedNames: []string{"spec A", "spec B"},
			wantOK:      true,
		},
		{
			name:        "one failure over the cap disqualifies",
			failedNames: []string{"spec A", "spec B", "spec C"},
			wantOK:      false,
		},
		{
			name:        "an unlabeled failure disqualifies the whole run",
			failedNames: []string{"spec A", "spec B"},
			nonRetried:  1,
			wantOK:      false,
		},
		{
			name:        "a lone unlabeled failure disqualifies",
			failedNames: []string{"spec A"},
			nonRetried:  1,
			wantOK:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker, ok := ev2RetryMarker(tc.failedNames, tc.nonRetried)
			if ok != tc.wantOK {
				t.Fatalf("ev2RetryMarker(%v, %d) ok = %v, want %v", tc.failedNames, tc.nonRetried, ok, tc.wantOK)
			}
			if !ok {
				if marker != "" {
					t.Fatalf("expected no marker when not qualifying, got %q", marker)
				}
				return
			}
			if !strings.HasPrefix(marker, "EV2_RETRY_ALLOWED:") {
				t.Fatalf("marker must start with the grep token the EV2 step looks for, got %q", marker)
			}
			if strings.Contains(marker, "\n") {
				t.Fatalf("marker must be a single line so the EV2 step can grep it, got %q", marker)
			}
			for _, name := range tc.failedNames {
				if !strings.Contains(marker, name) {
					t.Fatalf("marker must name every failed spec so a human can triage it, %q missing from %q", name, marker)
				}
			}
		})
	}
}
