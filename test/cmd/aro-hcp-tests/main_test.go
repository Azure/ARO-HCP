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

// TestUpgradeInPlaceParallelismMatchesSpecCount enforces that upgradeInPlaceParallelism
// stays in sync with the actual number of It blocks in the upgrade/in-place suite.
// If you add or remove an UpgradeInPlace spec, update the constant and this test will pass again.
func TestUpgradeInPlaceParallelismMatchesSpecCount(t *testing.T) {
	root := setupCli()
	root.SetArgs([]string{"list", "tests", "--suite", "upgrade/in-place", "--output", "names"})

	tmpFile, err := os.CreateTemp("", "upgrade-spec-count-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	originalStdout := os.Stdout
	os.Stdout = tmpFile
	execErr := root.Execute()
	os.Stdout = originalStdout
	tmpFile.Close()

	if execErr != nil {
		t.Fatalf("list command failed: %v", execErr)
	}

	raw, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to read temp file: %v", err)
	}

	var specCount int
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			specCount++
		}
	}

	if specCount != upgradeInPlaceParallelism {
		t.Errorf("upgradeInPlaceParallelism=%d but the upgrade/in-place suite has %d spec(s); "+
			"update upgradeInPlaceParallelism in main.go to match the actual spec count",
			upgradeInPlaceParallelism, specCount)
	}
}

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
