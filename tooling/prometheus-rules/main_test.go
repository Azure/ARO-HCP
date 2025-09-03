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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupTestFiles(tmpDir string, defaultEvaluationInterval string) error {
	config := `
prometheusRules:
  defaultEvaluationInterval: ${defaultEvaluationInterval}
  rulesFolders:
  - ./alerts
  untestedRules: []
  outputBicep: zzz_generated_AlertingRules.bicep
`
	updatedConfig := strings.Replace(config, "${defaultEvaluationInterval}", defaultEvaluationInterval, 1)
	err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(updatedConfig), 0660)
	if err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(tmpDir, "alerts"), 0755)
}

func copyFile(fileToCopy, targetDir string) error {
	input, err := os.ReadFile(fileToCopy)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(targetDir, filepath.Base(fileToCopy)), input, 0644)
}

func TestPrometheusRules(t *testing.T) {

	testCases := []struct {
		name                      string
		defaultEvaluationInterval string
		generatedFile             string
	}{
		{name: "1m", defaultEvaluationInterval: "1m", generatedFile: "generated.bicep"},
		{name: "5m", defaultEvaluationInterval: "5m", generatedFile: "generated_5m.bicep"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			require.NoError(t, setupTestFiles(tmpDir, testCase.defaultEvaluationInterval))

			for _, testfile := range []string{
				"./testdata/alerts/testing-prometheusRule_test.yaml",
				"./testdata/alerts/testing-prometheusRule.yaml"} {
				require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
			}
			err := runGenerator(filepath.Join(tmpDir, "config.yaml"), false)
			require.NoError(t, err)

			generatedFile, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
			require.NoError(t, err)

			expectedContent, err := os.ReadFile(filepath.Join("testdata", testCase.generatedFile))
			require.NoError(t, err)

				require.Equal(t, string(expectedContent), string(generatedFile))
		})
	}

}

func TestPrometheusRulesMissingTest(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule.yaml"} {
		require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}
	err := runGenerator(filepath.Join(tmpDir, "config.yaml"), false)
	require.ErrorContains(t, err, "missing testfile")
}

func TestPrometheusRulesMixedRulesNotAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	// Create a rule file with mixed alert and recording rules in the same group
	mixedRulesContent := `apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mixed-rules
spec:
  groups:
  - name: MixedGroup
    rules:
    - alert: TestAlert
      expr: up == 0
      labels:
        severity: critical
    - record: test:metric:rate5m
      expr: rate(test_metric[5m])
`

	// Create a corresponding test file (required by the generator)
	testFileContent := `rule_files:
- mixed-prometheusRule.yaml
tests: []
`

	err := os.WriteFile(filepath.Join(tmpDir, "alerts", "mixed-prometheusRule.yaml"), []byte(mixedRulesContent), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "alerts", "mixed-prometheusRule_test.yaml"), []byte(testFileContent), 0644)
	require.NoError(t, err)

	// Run the generator - it should handle mixed rules based on file type
	// Since we're using AlertingRules filename, it should process only alerts
	err = runGenerator(filepath.Join(tmpDir, "config.yaml"), false)
	require.NoError(t, err)

	// Verify the generated file exists and contains only alert rules
	generatedFile, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
	require.NoError(t, err)

	// The generated content should contain alert-related configuration
	require.Contains(t, string(generatedFile), "alert: 'TestAlert'")
	// Recording rules should be ignored when generating AlertingRules file
	require.NotContains(t, string(generatedFile), "record: 'test:metric:rate5m'")
}
