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

func TestWriteEV2RetryMetadata(t *testing.T) {
	t.Run("empty artifact dir is skipped without error", func(t *testing.T) {
		if err := writeEV2RetryMetadata("", []string{"spec A"}, []string{"spec A"}); err != nil {
			t.Fatalf("expected no error when ARTIFACT_DIR is unset, got %v", err)
		}
	})

	t.Run("writes a fresh metadata.json", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeEV2RetryMetadata(dir, []string{"spec A", "spec B"}, []string{"spec A"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 2 {
			t.Fatalf("expected 2 failed test names recorded, got %v", got[ev2FailedTestsKey])
		}
		allowRetry, ok := got[ev2AllowRetryTestsKey].([]interface{})
		if !ok || len(allowRetry) != 1 {
			t.Fatalf("expected 1 allow-retry test name recorded, got %v", got[ev2AllowRetryTestsKey])
		}
	})

	t.Run("writes empty lists when nothing failed, so the keys are always present", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeEV2RetryMetadata(dir, nil, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 0 {
			t.Fatalf("expected an empty failed-tests list, got %v", got[ev2FailedTestsKey])
		}
		allowRetry, ok := got[ev2AllowRetryTestsKey].([]interface{})
		if !ok || len(allowRetry) != 0 {
			t.Fatalf("expected an empty allow-retry-tests list, got %v", got[ev2AllowRetryTestsKey])
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

		if err := writeEV2RetryMetadata(dir, []string{"spec A"}, []string{"spec A"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := readMetadataFile(t, dir)
		if got["some-other-step"] != "wrote-this" {
			t.Fatalf("expected pre-existing key to survive the merge, got %v", got)
		}
		failed, ok := got[ev2FailedTestsKey].([]interface{})
		if !ok || len(failed) != 1 {
			t.Fatalf("expected 1 failed test name recorded, got %v", got[ev2FailedTestsKey])
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
