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
	"testing"

	"github.com/stretchr/testify/assert"
)

func setupTestFiles(tmpDir string) error {
	config := `
prometheusRules:
  rulesFolders:
  - ./alerts
  untestedRules: []
  outputBicep: zzz_generated.bicep
`

	err := os.WriteFile(filepath.Join(tmpDir, "config.yaml"), []byte(config), 0660)
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
	tmpDir := t.TempDir()
	assert.NoError(t, setupTestFiles(tmpDir))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule_test.yaml",
		"./testdata/alerts/testing-prometheusRule.yaml"} {
		assert.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}
	err := runGenerator(filepath.Join(tmpDir, "config.yaml"))
	assert.NoError(t, err)

	generatedFile, err := os.ReadFile(filepath.Join(tmpDir, "zzz_generated.bicep"))
	assert.NoError(t, err)

	expectedContent, err := os.ReadFile(filepath.Join("testdata", "generated.bicep"))
	assert.NoError(t, err)

	assert.Equal(t, string(generatedFile), string(expectedContent))
}

func TestPrometheusRulesMissingTest(t *testing.T) {
	tmpDir := t.TempDir()
	assert.NoError(t, setupTestFiles(tmpDir))

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule.yaml"} {
		assert.NoError(t, copyFile(testfile, filepath.Join(tmpDir, "alerts")))
	}
	err := runGenerator(filepath.Join(tmpDir, "config.yaml"))
	assert.ErrorContains(t, err, "missing testfile")
}
