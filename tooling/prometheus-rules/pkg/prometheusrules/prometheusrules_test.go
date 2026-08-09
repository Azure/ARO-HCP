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

package prometheusrules

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	assert.Equal(t, "promtool", opts.PromtoolPath)
	assert.Equal(t, "", opts.ConfigFile)
	assert.False(t, opts.SkipTests)
	assert.Equal(t, "region", opts.PreserveAggregationLabels)
}

func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		name     string
		csv      string
		expected []string
	}{
		{name: "empty", csv: "", expected: nil},
		{name: "single", csv: "region", expected: []string{"region"}},
		{name: "multiple with whitespace", csv: "region, cluster ,namespace", expected: []string{"region", "cluster", "namespace"}},
		{name: "drops empties", csv: "region,,  ,cluster", expected: []string{"region", "cluster"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, splitAndTrim(tc.csv))
		})
	}
}

func TestBindOptions(t *testing.T) {
	opts := DefaultOptions()
	cmd := &cobra.Command{}
	require.NoError(t, BindOptions(opts, cmd))

	// config-file flag should exist (validated at runtime, not cobra-level required,
	// because --correlation-map mode uses positional args instead)
	flag := cmd.Flags().Lookup("config-file")
	require.NotNil(t, flag)

	// promtool-path should have a default
	flag = cmd.Flags().Lookup("promtool-path")
	require.NotNil(t, flag)
	assert.Equal(t, "promtool", flag.DefValue)

	// skip-tests should exist
	flag = cmd.Flags().Lookup("skip-tests")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)

	// preserve-aggregation-labels should keep the "region" default
	flag = cmd.Flags().Lookup("preserve-aggregation-labels")
	require.NotNil(t, flag)
	assert.Equal(t, "region", flag.DefValue)
}

func TestValidate(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(""), 0644))

	promtoolFile := filepath.Join(t.TempDir(), "promtool")
	require.NoError(t, os.WriteFile(promtoolFile, []byte(""), 0755))

	tests := []struct {
		name        string
		opts        RawOptions
		expectError string
	}{
		{
			name: "valid with tests enabled and explicit promtool path",
			opts: RawOptions{
				ConfigFile:   configFile,
				PromtoolPath: promtoolFile,
			},
		},
		{
			name: "valid with skip-tests and empty promtool path",
			opts: RawOptions{
				ConfigFile: configFile,
				SkipTests:  true,
			},
		},
		{
			name: "empty config file",
			opts: RawOptions{
				PromtoolPath: promtoolFile,
			},
			expectError: "--config-file is required",
		},
		{
			name: "empty promtool path with tests enabled",
			opts: RawOptions{
				ConfigFile:   configFile,
				PromtoolPath: "",
			},
			expectError: "--promtool-path cannot be empty when tests are enabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validated, err := tc.opts.Validate()
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				assert.Nil(t, validated)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, validated)
			}
		})
	}
}

func TestComplete(t *testing.T) {
	configContent := `
prometheusRules:
  rulesFolders: []
  untestedRules: []
  outputBicep: zzz_generated_AlertingRules.bicep
`
	t.Run("valid config", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

		opts := &RawOptions{
			ConfigFile: configPath,
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.NoError(t, err)
		assert.NotNil(t, completed)
	})

	t.Run("promtool binary not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0644))

		opts := &RawOptions{
			ConfigFile:   configPath,
			PromtoolPath: "definitely-not-a-real-binary-abc123",
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to find promtool binary")
		assert.Nil(t, completed)
	})

	t.Run("config file not found", func(t *testing.T) {
		opts := &RawOptions{
			ConfigFile: "/nonexistent/path/config.yaml",
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "configuration file")
		assert.Nil(t, completed)
	})

	t.Run("invalid config content", func(t *testing.T) {
		badConfig := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(badConfig, []byte("not: valid: yaml: ["), 0644))

		opts := &RawOptions{
			ConfigFile: badConfig,
			SkipTests:  true,
		}
		validated, err := opts.Validate()
		require.NoError(t, err)

		completed, err := validated.Complete()
		require.Error(t, err)
		assert.Nil(t, completed)
	})
}

// writeRuleFixture creates a config plus an alerts folder holding one alerting
// rule and its promtool test file. A fixture with real rules is what gives
// RunTests actual work to do; with an empty rulesFolders the generator never
// shells out to promtool and skip-tests assertions pass vacuously.
func writeRuleFixture(t *testing.T) string {
	t.Helper()

	return writeRuleFixtureIn(t, t.TempDir())
}

// writeRuleFixtureIn builds the fixture under an explicit base directory, so
// tests can control the surrounding path.
func writeRuleFixtureIn(t *testing.T, tmpDir string) string {
	t.Helper()

	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "alerts"), 0755))

	config := `
prometheusRules:
  rulesFolders:
  - ./alerts
  untestedRules: []
  outputBicep: zzz_generated_AlertingRules.bicep
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(config), 0644))

	rule := `apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: testing
spec:
  groups:
  - name: TestGroup
    rules:
    - alert: TestAlert
      expr: up == 0
      labels:
        severity: critical
        component: testing
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "alerts", "testing-prometheusRule.yaml"), []byte(rule), 0644))

	ruleTest := `rule_files:
- testing-prometheusRule.yaml
tests: []
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "alerts", "testing-prometheusRule_test.yaml"), []byte(ruleTest), 0644))

	return tmpDir
}

// fakePromtool writes an executable stub that appends its arguments to a marker
// file and exits with exitCode, so tests can assert whether promtool ran at all
// rather than relying on a real binary being present on PATH.
func fakePromtool(t *testing.T, exitCode int) (binary string, marker string) {
	t.Helper()

	dir := t.TempDir()
	marker = filepath.Join(dir, "invocations")
	binary = filepath.Join(dir, "promtool")

	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %q\nexit %d\n", marker, exitCode)
	require.NoError(t, os.WriteFile(binary, []byte(script), 0755))

	return binary, marker
}

func completeOptions(t *testing.T, opts *RawOptions) *Options {
	t.Helper()

	validated, err := opts.Validate()
	require.NoError(t, err)
	completed, err := validated.Complete()
	require.NoError(t, err)
	return completed
}

func TestRun(t *testing.T) {
	t.Run("skip-tests generates bicep without invoking promtool", func(t *testing.T) {
		tmpDir := writeRuleFixture(t)
		promtool, marker := fakePromtool(t, 0)

		completed := completeOptions(t, &RawOptions{
			ConfigFile:   filepath.Join(tmpDir, "config.yaml"),
			PromtoolPath: promtool,
			SkipTests:    true,
		})
		require.NoError(t, completed.Run())

		_, err := os.Stat(marker)
		assert.True(t, os.IsNotExist(err), "promtool was invoked even though --skip-tests was set")

		_, err = os.Stat(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
		assert.NoError(t, err, "bicep output should still be generated when tests are skipped")
	})

	t.Run("promtool runs when tests are not skipped", func(t *testing.T) {
		tmpDir := writeRuleFixture(t)
		promtool, marker := fakePromtool(t, 0)

		completed := completeOptions(t, &RawOptions{
			ConfigFile:   filepath.Join(tmpDir, "config.yaml"),
			PromtoolPath: promtool,
			SkipTests:    false,
		})
		require.NoError(t, completed.Run())

		invocations, err := os.ReadFile(marker)
		require.NoError(t, err, "promtool was never invoked")
		assert.Contains(t, string(invocations), "test rules")

		_, err = os.Stat(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
		assert.NoError(t, err)
	})

	t.Run("failing promtool fails the run", func(t *testing.T) {
		tmpDir := writeRuleFixture(t)
		promtool, _ := fakePromtool(t, 1)

		completed := completeOptions(t, &RawOptions{
			ConfigFile:   filepath.Join(tmpDir, "config.yaml"),
			PromtoolPath: promtool,
			SkipTests:    false,
		})
		err := completed.Run()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "testing rules failed")
	})
}

func TestRunRuleFolderBelowTestNamedDirectory(t *testing.T) {
	// Rule discovery skips promtool test files by name. Matching that against
	// the whole path would silently drop every rule in a checkout that happens
	// to live under a directory such as "my_test_workspace", generating an
	// empty bicep file and running no tests at all.
	parent := filepath.Join(t.TempDir(), "my_test_workspace")
	require.NoError(t, os.Mkdir(parent, 0755))

	tmpDir := writeRuleFixtureIn(t, parent)
	promtool, marker := fakePromtool(t, 0)

	completed := completeOptions(t, &RawOptions{
		ConfigFile:   filepath.Join(tmpDir, "config.yaml"),
		PromtoolPath: promtool,
		SkipTests:    false,
	})
	require.NoError(t, completed.Run())

	_, err := os.Stat(marker)
	require.NoError(t, err, "promtool was never invoked, so the rule file was skipped")

	generated, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated_AlertingRules.bicep"))
	require.NoError(t, err)
	assert.Contains(t, string(generated), "alert: 'TestAlert'", "rule was dropped during discovery")
}
