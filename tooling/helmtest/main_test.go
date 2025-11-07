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
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/release"

	"sigs.k8s.io/yaml"

	"github.com/Azure/ARO-Tools/pkg/config"

	"github.com/Azure/ARO-HCP/tooling/helmtest/internal"
)

func runTest(ctx context.Context, testCase internal.TestCase) (string, error) {
	settings := cli.New()
	cfg := action.Configuration{}
	err := cfg.Init(settings.RESTClientGetter(), testCase.Namespace, "memory")
	if err != nil {
		return "", fmt.Errorf("error initializing config, %v", err)
	}

	in := action.NewInstall(&cfg)
	in.DryRunStrategy = action.DryRunClient
	in.ReleaseName = testCase.Name
	in.Namespace = testCase.Namespace
	in.DisableHooks = false
	in.IncludeCRDs = true
	in.KubeVersion = &common.KubeVersion{
		Version: "v1.30.0",
		Major:   "1",
		Minor:   "30",
	}

	cfgYaml, err := internal.LoadConfigAndMerge(filepath.Join(internal.RepoRoot, "config/rendered/dev/dev/westus3.yaml"), testCase.TestData)
	if err != nil {
		return "", fmt.Errorf("error loading config, %v", err)
	}

	values, err := config.PreprocessFile(testCase.Values, cfgYaml)
	if err != nil {
		return "", fmt.Errorf("error preprocessing values file, %v", err)

	}
	var cval map[string]any
	err = yaml.Unmarshal(values, &cval)
	if err != nil {
		return "", fmt.Errorf("error unmarshalling config, %v", err)

	}
	chart, err := loader.Load(testCase.HelmChartDir)
	if err != nil {
		return "", fmt.Errorf("error loading chart, %v", err)
	}

	rel, err := in.RunWithContext(ctx, chart, cval)
	if err != nil {
		return "", fmt.Errorf("error running helm, %v", err)
	}
	accessor, err := release.NewAccessor(rel)
	if err != nil {
		return "", fmt.Errorf("error creating accessor, %v", err)
	}

	allHooks := ""
	for _, hook := range accessor.Hooks() {
		ha, err := release.NewHookAccessor(hook)
		if err != nil {
			return "", fmt.Errorf("error creating hook accessor, %v", err)
		}
		allHooks = fmt.Sprintf("%s---\n# Source: %s\n%s\n", allHooks, ha.Path(), ha.Manifest())
	}
	return fmt.Sprintf("%s\n%s", accessor.Manifest(), allHooks), nil
}

func TestHelmTemplateFromTestFiles(t *testing.T) {
	testCases, err := internal.FindHelmtestFiles()
	assert.NoError(t, err)
	assert.NotNil(t, testCases)

	for _, testCasePath := range testCases {
		testCaseRaw, err := os.ReadFile(testCasePath)
		assert.NoError(t, err)

		var testCase internal.TestCase
		err = yaml.Unmarshal(testCaseRaw, &testCase)
		assert.NoError(t, err)

		// Override paths from config, need to prefix with file-containing directory
		testCase.Values = filepath.Join(filepath.Dir(testCasePath), testCase.Values)
		testCase.HelmChartDir = filepath.Join(filepath.Dir(testCasePath), testCase.HelmChartDir)

		t.Run(testCase.Name, func(t *testing.T) {
			manifest, err := runTest(t.Context(), testCase)
			assert.NoError(t, err)
			CompareWithFixture(t, manifest, WithGoldenDir(filepath.Dir(testCasePath)))
		})
	}

}

func TestHelmTemplateFromHelmSteps(t *testing.T) {
	helmSteps, err := internal.FindHelmSteps()
	assert.NoError(t, err)
	assert.NotNil(t, helmSteps)

	for _, helmStep := range helmSteps {
		fmt.Println(filepath.Join(internal.RepoRoot, helmStep.ChartDirFromRoot()))

		testCase := internal.TestCase{
			Name:         helmStep.HelmStep.ReleaseName,
			Namespace:    helmStep.HelmStep.ReleaseNamespace,
			Values:       helmStep.ValuesFileFromRoot(),
			HelmChartDir: helmStep.ChartDirFromRoot(),
			TestData:     map[string]any{},
		}
		t.Run(helmStep.HelmStep.Name, func(t *testing.T) {
			manifest, err := runTest(t.Context(), testCase)
			assert.NoError(t, err)
			CompareWithFixture(t, manifest, WithGoldenDir(filepath.Join(helmStep.ChartDirFromRoot(), "testdata")))
		})
	}
}
