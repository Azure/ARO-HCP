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

package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TestSummary holds metadata about a single e2e test from a Prow job run.
// This includes both passing and failing tests so the analysis agent can
// compare errant logs against known-good exemplars from passing sibling tests.
type TestSummary struct {
	Name             string    `json:"name"`
	Result           string    `json:"result"`
	ResourceGroup    string    `json:"resource_group,omitempty"`
	StartTime        time.Time `json:"start_time,omitempty"`
	EndTime          time.Time `json:"end_time,omitempty"`
	CleanupStartTime time.Time `json:"cleanup_start_time,omitempty"`
}

// ConvertTestResults transforms the raw test results returned by
// FetchProwJobTestResults into the TestSummary format used by the analysis agent.
func ConvertTestResults(results []TestResult) []TestSummary {
	summaries := make([]TestSummary, 0, len(results))
	for _, r := range results {
		result := "passed"
		if r.Failed {
			result = "failed"
		}
		ts := TestSummary{
			Name:             r.Name,
			Result:           result,
			ResourceGroup:    r.ResourceGroup,
			StartTime:        r.StartTime,
			EndTime:          r.EndTime,
			CleanupStartTime: r.CleanupStartTime,
		}
		summaries = append(summaries, ts)
	}
	return summaries
}

// WriteTestLogs writes the test's error and output logs to the test_logs/
// subdirectory of the output directory. This provides the analysis agent with
// the raw test output for inclusion in proof items.
func WriteTestLogs(outputDir string, test *TestResult) error {
	testDir := filepath.Join(outputDir, "test_logs")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		return fmt.Errorf("failed to create test output dir: %w", err)
	}
	if test.Error != "" {
		if err := os.WriteFile(filepath.Join(testDir, "error.log"), []byte(test.Error), 0o644); err != nil {
			return fmt.Errorf("failed to write error log: %w", err)
		}
	}
	if test.Output != "" {
		if err := os.WriteFile(filepath.Join(testDir, "output.log"), []byte(test.Output), 0o644); err != nil {
			return fmt.Errorf("failed to write output log: %w", err)
		}
	}
	return nil
}

// WriteSiblingTests writes the sibling test summaries to sibling_tests.json
// in the output directory. This provides the analysis agent with visibility
// into all tests from the same Prow job run.
func WriteSiblingTests(outputDir string, summaries []TestSummary) error {
	if len(summaries) == 0 {
		return nil
	}
	data, err := json.Marshal(summaries)
	if err != nil {
		return fmt.Errorf("failed to marshal sibling test summaries: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "sibling_tests.json"), data, 0o644); err != nil {
		return fmt.Errorf("failed to write sibling_tests.json: %w", err)
	}
	return nil
}

// WriteNodeConsoleLogs writes VM serial console log files to the node_boot_logs/
// subdirectory of the output directory. These files contain boot diagnostic output
// from VMs that were part of the test, captured by the test framework when node
// pool creation fails.
func WriteNodeConsoleLogs(outputDir string, logs []NodeConsoleLogFile) error {
	if len(logs) == 0 {
		return nil
	}
	dir := filepath.Join(outputDir, "node_boot_logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create node_boot_logs dir: %w", err)
	}
	for _, log := range logs {
		// Sanitize the filename to prevent path traversal: use only the
		// base name component and reject any filename where Base() differs
		// from the original (which would indicate embedded separators or
		// directory components like "../" or "foo/bar").
		safeName := filepath.Base(log.FileName)
		if safeName != log.FileName || safeName == "." || safeName == ".." {
			return fmt.Errorf("console log filename %q contains path separators or is invalid", log.FileName)
		}
		if err := os.WriteFile(filepath.Join(dir, safeName), log.Content, 0o644); err != nil {
			return fmt.Errorf("failed to write console log %s: %w", safeName, err)
		}
	}
	return nil
}

// WriteAzureLog writes the client-side azure.log to azure_sdk_log/azure.log in
// the output directory. It is a no-op when no log was captured.
func WriteAzureLog(outputDir string, log *AzureLogFile) error {
	if log == nil || len(log.Content) == 0 {
		return nil
	}
	dir := filepath.Join(outputDir, "azure_sdk_log")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create azure_sdk_log dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "azure.log"), log.Content, 0o644); err != nil {
		return fmt.Errorf("failed to write azure.log: %w", err)
	}
	return nil
}

// RecoverTimeWindow reads sibling_tests.json from a previously gathered data
// directory and returns the start/end/cleanup-start times for the named test.
// This is used when reusing gathered data from a previous run where the time
// window was not persisted separately. Returns zero times if the file is
// missing or the test is not found.
func RecoverTimeWindow(dataDir, testName string) (time.Time, time.Time, time.Time) {
	siblingPath := filepath.Join(dataDir, "sibling_tests.json")
	data, err := os.ReadFile(siblingPath)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}
	}
	var summaries []TestSummary
	if err := json.Unmarshal(data, &summaries); err != nil {
		return time.Time{}, time.Time{}, time.Time{}
	}
	for _, s := range summaries {
		if s.Name == testName {
			return s.StartTime, s.EndTime, s.CleanupStartTime
		}
	}
	return time.Time{}, time.Time{}, time.Time{}
}
