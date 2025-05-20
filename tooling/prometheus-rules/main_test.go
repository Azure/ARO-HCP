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
	setupTestFiles(tmpDir)
	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule_test.yaml",
		"./testdata/alerts/testing-prometheusRule.yaml"} {
		copyFile(testfile, filepath.Join(tmpDir, "alerts"))
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
	setupTestFiles(tmpDir)

	for _, testfile := range []string{
		"./testdata/alerts/testing-prometheusRule.yaml"} {
		copyFile(testfile, filepath.Join(tmpDir, "alerts"))
	}
	err := runGenerator(filepath.Join(tmpDir, "config.yaml"))
	assert.ErrorContains(t, err, "missing testfile")
}
