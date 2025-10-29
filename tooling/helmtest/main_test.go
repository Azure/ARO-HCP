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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"sigs.k8s.io/yaml"
)

const (
	repoRootDir = "../../"
)

type testCases struct {
	Cases []testCase `yaml:"cases"`
}

type testCase struct {
	Name         string `yaml:"name"`
	Namespace    string `yaml:"namespace"`
	SetFile      string `yaml:"setFile"`
	HelmChartDir string `yaml:"helmChartDir"`
}

func getTestCases() (*testCases, error) {
	helmtestsPath := filepath.Join(repoRootDir, ".helmtests.yaml")
	testConfigBytes, err := os.ReadFile(helmtestsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read .helmtests.yaml: %w", err)
	}
	var testConfig *testCases
	err = yaml.Unmarshal(testConfigBytes, &testConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal .helmtests.yaml: %w", err)
	}
	return testConfig, nil
}

func runHelmTemplate(helmDeploy, helmChartDir, namespace string, setValues []string) (string, error) {
	args := []string{
		"helm",
		"template",
		strings.ReplaceAll(helmDeploy, " ", "-"),
		"--namespace",
		namespace,
		helmChartDir,
	}
	args = append(args, setValues...)
	stdout, err := exec.CommandContext(context.Background(), args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return string(stdout), fmt.Errorf("failed to run helm template: %w", err)
	}
	return string(stdout), nil
}

func generateSetParameters(preprocessedValuesFile map[string]string) []string {
	setValues := []string{}
	for key, value := range preprocessedValuesFile {
		setValues = append(setValues, fmt.Sprintf("--set=%s=%s", key, value))
	}
	return setValues
}

func TestHelmTemplate(t *testing.T) {
	testCases, err := getTestCases()
	assert.NoError(t, err)
	assert.NotNil(t, testCases)

	for _, testCase := range testCases.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			var preprocessedSetFile map[string]string
			setFileBytes, err := os.ReadFile(filepath.Join(repoRootDir, testCase.SetFile))
			assert.NoError(t, err)
			err = yaml.Unmarshal(setFileBytes, &preprocessedSetFile)
			assert.NoError(t, err)

			setValues := generateSetParameters(preprocessedSetFile)
			stdout, err := runHelmTemplate(testCase.Name, filepath.Join(repoRootDir, testCase.HelmChartDir), testCase.Namespace, setValues)
			if err != nil {
				fmt.Println(stdout)
			}
			assert.NoError(t, err)
			CompareWithFixture(t, stdout, WithGoldenDir(filepath.Join(repoRootDir, filepath.Dir(testCase.SetFile))))
		})

	}

}
