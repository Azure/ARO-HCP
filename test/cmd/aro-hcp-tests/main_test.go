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
	"encoding/json"
	"os"
	"path/filepath"
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

func TestEV2RetryAllowed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failedNames []string
		nonRetried  int
		want        bool
	}{
		{
			name: "clean run does not qualify",
			want: false,
		},
		{
			name:        "single labeled failure qualifies",
			failedNames: []string{"spec A"},
			want:        true,
		},
		{
			name:        "failures at the cap still qualify",
			failedNames: []string{"spec A", "spec B"},
			want:        true,
		},
		{
			name:        "one failure over the cap disqualifies",
			failedNames: []string{"spec A", "spec B", "spec C"},
			want:        false,
		},
		{
			name:        "an unlabeled failure disqualifies the whole run",
			failedNames: []string{"spec A", "spec B"},
			nonRetried:  1,
			want:        false,
		},
		{
			name:        "a lone unlabeled failure disqualifies",
			failedNames: []string{"spec A"},
			nonRetried:  1,
			want:        false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ev2RetryAllowed(tc.failedNames, tc.nonRetried); got != tc.want {
				t.Fatalf("ev2RetryAllowed(%v, %d) = %v, want %v", tc.failedNames, tc.nonRetried, got, tc.want)
			}
		})
	}
}

func TestWriteEV2RetryMetadata(t *testing.T) {
	t.Run("empty artifact dir is skipped without error", func(t *testing.T) {
		if err := writeEV2RetryMetadata("", []string{"spec A"}); err != nil {
			t.Fatalf("expected no error when ARTIFACT_DIR is unset, got %v", err)
		}
	})

	t.Run("writes a fresh metadata.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeEV2RetryMetadata(dir, []string{"spec A", "spec B"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		if got[ev2RetryMetadataKey] != true {
			t.Fatalf("expected %s=true, got %v", ev2RetryMetadataKey, got[ev2RetryMetadataKey])
		}
		names, ok := got[ev2RetryMetadataKey+"-tests"].([]interface{})
		if !ok || len(names) != 2 {
			t.Fatalf("expected 2 failed test names recorded, got %v", got[ev2RetryMetadataKey+"-tests"])
		}
	})

	t.Run("merges into an existing metadata.json without clobbering other keys", func(t *testing.T) {
		dir := t.TempDir()
		existing := map[string]interface{}{"some-other-step": "wrote-this"}
		data, err := json.Marshal(existing)
		if err != nil {
			t.Fatalf("failed to marshal fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ev2RetryMetadataFile), data, 0o644); err != nil {
			t.Fatalf("failed to write fixture: %v", err)
		}

		if err := writeEV2RetryMetadata(dir, []string{"spec A"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		if got["some-other-step"] != "wrote-this" {
			t.Fatalf("expected pre-existing key to survive the merge, got %v", got)
		}
		if got[ev2RetryMetadataKey] != true {
			t.Fatalf("expected %s=true, got %v", ev2RetryMetadataKey, got[ev2RetryMetadataKey])
		}
	})
}

func readMetadataFile(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ev2RetryMetadataFile))
	if err != nil {
		t.Fatalf("failed to read %s: %v", ev2RetryMetadataFile, err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("failed to parse %s: %v", ev2RetryMetadataFile, err)
	}
	return got
}
