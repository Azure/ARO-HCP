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

package testrunner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func runTest(ctx context.Context, settings *internal.Settings, testCase internal.TestCase) (string, error) {
	helmSettings := cli.New()
	cfg := action.Configuration{}
	err := cfg.Init(helmSettings.RESTClientGetter(), testCase.Namespace, "memory")
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

	cfgYaml, err := internal.LoadConfigAndMerge(settings.ConfigPath, testCase.TestData)
	if err != nil {
		return "", fmt.Errorf("error loading config, %v", err)
	}

	cfgYaml = internal.ReplaceImageDigest(cfgYaml)

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

	manifest := accessor.Manifest()

	for _, replace := range settings.Replace {
		re, err := regexp.Compile(replace.Regex)
		if err != nil {
			return "", fmt.Errorf("error compiling regex, %v", err)
		}
		manifest = re.ReplaceAllString(manifest, replace.Replacement)
	}

	return fmt.Sprintf("%s\n%s", manifest, allHooks), nil
}

func getCustomTestCases(chartDir string) ([]internal.TestCase, error) {
	testCaseFiles, err := internal.FindHelmTestFiles(filepath.Join(chartDir, internal.TestDataFromChartDir))
	if err != nil {
		return nil, fmt.Errorf("error finding helmtest files, %v", err)
	}

	allCases := []internal.TestCase{}

	for _, testCasePath := range testCaseFiles {
		testCaseRaw, err := os.ReadFile(testCasePath)
		if err != nil {
			return nil, fmt.Errorf("error reading test case file, %v", err)
		}

		var testCase internal.TestCase
		err = yaml.Unmarshal(testCaseRaw, &testCase)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling test case file, %v", err)
		}

		testCase.Values = filepath.Join(chartDir, internal.TestDataFromChartDir, testCase.Values)
		if testCase.HelmChartDir == "" {
			testCase.HelmChartDir = chartDir
		}
		allCases = append(allCases, testCase)
	}
	return allCases, nil
}

func RunTestHelmTemplate(t *testing.T, settingsPath string) {
	settings, err := internal.LoadSettings(settingsPath)
	assert.NoError(t, err)
	assert.NotNil(t, settings)

	helmSteps, err := internal.FindHelmSteps(settings.TopologyDir, settings.ConfigPath)
	assert.NoError(t, err)
	assert.NotNil(t, helmSteps)

	chartDirsVisited := make(map[string]bool)

	for _, helmStep := range helmSteps {
		allCases := []internal.TestCase{}
		if _, ok := chartDirsVisited[helmStep.ChartDirFromRoot(settings.TopologyDir)]; !ok {
			// visit the chart directory only once. Some helm step definitions reference the directory, would cause duplicates.
			customTestCases, err := getCustomTestCases(helmStep.ChartDirFromRoot(settings.TopologyDir))
			assert.NoError(t, err)
			allCases = append(allCases, customTestCases...)
			chartDirsVisited[helmStep.ChartDirFromRoot(settings.TopologyDir)] = true
		}
		chartPath := helmStep.ChartDirFromRoot(settings.TopologyDir)
		if strings.Contains(chartPath, "oci:") {
			// get the full path to the values file
			fullValuesPath := helmStep.ValuesFileFromRoot(settings.TopologyDir)

			// extract filename
			fileName := filepath.Base(fullValuesPath)

			// split on "." to get chartDir
			chartName := strings.Split(fileName, ".")[0]

			// final path
			chartPath = filepath.Join(settings.TopologyDir, "acm/deploy/helm", chartName)
		}

		allCases = append(allCases, internal.TestCase{
			Name:         fmt.Sprintf("%s-%s", helmStep.AKSCluster, helmStep.HelmStep.ReleaseName),
			Namespace:    helmStep.HelmStep.ReleaseNamespace,
			Values:       helmStep.ValuesFileFromRoot(settings.TopologyDir),
			HelmChartDir: chartPath,
			TestData:     map[string]any{},
			Implicit:     true,
		})
		for _, testCase := range allCases {
			t.Run(testCase.Name, func(t *testing.T) {
				manifest, err := runTest(t.Context(), settings, testCase)
				assert.NoError(t, err)
				// we want to place implicit test cases by the pipelines that created them, not the chart they happened to render.
				// n.b. a more correct implementation would keep track of *where* the custom test case came from and use that dir
				// exactly as the output directory - an exercise left for the future
				outputDir := filepath.Join(helmStep.ChartDirFromRoot(settings.TopologyDir), internal.TestDataFromChartDir)
				if testCase.Implicit {
					outputDir = filepath.Join(settings.TopologyDir, filepath.Dir(helmStep.PipelinePath))
				}
				CompareWithFixture(t, manifest, WithGoldenDir(outputDir))
			})
		}
	}

}
