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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azure/ARO-HCP/tooling/prometheus-rules/pkg/prometheusrules"
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

func runGenerator(configFile, promtoolPath string, skipTests bool) error {
	opts := &prometheusrules.RawOptions{
		ConfigFile:                configFile,
		PromtoolPath:              promtoolPath,
		SkipTests:                 skipTests,
		PreserveAggregationLabels: "region",
	}
	validated, err := opts.Validate()
	if err != nil {
		return err
	}
	completed, err := validated.Complete()
	if err != nil {
		return err
	}
	return completed.Run()
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
				"./testdata/alerts/testing-prometheusRule.yaml",
			} {
				require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
			}
			err := runGenerator(filepath.Join(tmpDir, "config.yaml"), "promtool", false)
			require.NoError(t, err)

			generatedFile, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
			require.NoError(t, err)

			expectedContent, err := os.ReadFile(filepath.Join("testdata", testCase.generatedFile))
			require.NoError(t, err)

			if os.Getenv("UPDATE") != "" {
				require.NoError(t, os.WriteFile(filepath.Join("testdata", testCase.generatedFile), generatedFile, 0644))
			} else {
				require.Equal(t, string(expectedContent), string(generatedFile))
			}
		})
	}

}

func TestPrometheusRulesMissingTest(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule.yaml",
	} {
		require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}
	err := runGenerator(filepath.Join(tmpDir, "config.yaml"), "promtool", false)
	require.ErrorContains(t, err, "missing testfile")
}

func TestPrometheusRulesMixedRulesNotAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

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
        component: testing
    - record: test:metric:rate5m
      expr: rate(test_metric[5m])
`

	testFileContent := `rule_files:
- mixed-prometheusRule.yaml
tests: []
`

	err := os.WriteFile(filepath.Join(tmpDir, "alerts", "mixed-prometheusRule.yaml"), []byte(mixedRulesContent), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "alerts", "mixed-prometheusRule_test.yaml"), []byte(testFileContent), 0644)
	require.NoError(t, err)

	err = runGenerator(filepath.Join(tmpDir, "config.yaml"), "promtool", false)
	require.NoError(t, err)

	generatedFile, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
	require.NoError(t, err)

	require.Contains(t, string(generatedFile), "alert: 'TestAlert'")
	require.NotContains(t, string(generatedFile), "record: 'test:metric:rate5m'")
}

// executeCommand builds the real cobra command and runs it with the given
// arguments, returning anything the command wrote to its output stream. This
// exercises the flag binding and RunE dispatch that main() relies on, which
// calling prometheusrules.RawOptions directly would bypass.
func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd, err := newCommand()
	require.NoError(t, err)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	execErr := cmd.Execute()
	return out.String(), execErr
}

func TestCommandGenerates(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule_test.yaml",
		"./testdata/alerts/testing-prometheusRule.yaml",
	} {
		require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}

	_, err := executeCommand(t, "--config-file", filepath.Join(tmpDir, "config.yaml"), "--skip-tests")
	require.NoError(t, err)

	generated, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
	require.NoError(t, err)
	require.Contains(t, string(generated), "alert:")
}

func TestCommandCorrelationMap(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule_test.yaml",
		"./testdata/alerts/testing-prometheusRule.yaml",
	} {
		require.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}

	configPath := filepath.Join(tmpDir, "config.yaml")

	// The Makefile drives correlation-map mode with positional args rather than
	// --config-file, so both spellings need to keep working.
	t.Run("positional config", func(t *testing.T) {
		out, err := executeCommand(t, "--correlation-map", configPath)
		require.NoError(t, err)
		require.Contains(t, out, "alert: InstancesDownV1/")
		require.Contains(t, out, "correlationId:")
	})

	t.Run("config-file flag", func(t *testing.T) {
		out, err := executeCommand(t, "--correlation-map", "--config-file", configPath)
		require.NoError(t, err)
		require.Contains(t, out, "alert: InstancesDownV1/")
		require.Contains(t, out, "correlationId:")
	})

	t.Run("no config at all", func(t *testing.T) {
		_, err := executeCommand(t, "--correlation-map")
		require.ErrorContains(t, err, "at least one config file must be provided")
	})
}

func TestCommandRejectsUnexpectedArgs(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, setupTestFiles(tmpDir, ""))

	_, err := executeCommand(t, "--config-file", filepath.Join(tmpDir, "config.yaml"), "--skip-tests", "stray-arg")
	require.ErrorContains(t, err, "unexpected positional arguments")
}

func TestCommandRequiresConfigFile(t *testing.T) {
	_, err := executeCommand(t, "--skip-tests")
	require.ErrorContains(t, err, "--config-file is required")
}
